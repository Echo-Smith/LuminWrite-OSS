package profile

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lib/pq"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/routing"
)

// StyleProfile is a complete style configuration.
type StyleProfile struct {
	Slug             string          `json:"slug"`
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	Version          int             `json:"version"`
	Tags             []string        `json:"tags"`
	WordRange        WordRange       `json:"word_range"`
	Structure        Structure       `json:"structure"`
	Rhetoric         Rhetoric        `json:"rhetoric"`
	ValueOrientation ValueOrientation `json:"value_orientation"`
	TitleGuidelines  TitleGuidelines `json:"title_guidelines"`
	SystemPrompt     string          `json:"system_prompt"`
	WritingStandard  string          `json:"writing_standard"`
	FactGuard        FactGuard       `json:"fact_guard"`
	OutputFormat     OutputFormat    `json:"output_format"`
	LengthProfiles   map[string]WordRange `json:"length_profiles"`
}

type WordRange struct {
	Min       int  `json:"min"`
	Max       int  `json:"max"`
	HardLimit bool `json:"hard_limit"`
}

type Structure struct {
	Type           string      `json:"type"` // three_part | free_form | custom
	Opening        string      `json:"opening"`
	Body           string      `json:"body"`
	Conclusion     string      `json:"conclusion"`
	ArgumentPattern string     `json:"argument_pattern"`
	ArgumentVariations []string `json:"argument_variations"`
	ArgumentInstruction string  `json:"argument_instruction"`
	ArgumentCount  CountRange  `json:"argument_count"`
}

type CountRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type Rhetoric struct {
	RequiredMetaphor            bool   `json:"required_metaphor"`
	RequiredParallelism         bool   `json:"required_parallelism"`
	RequiredRhetoricalQuestion  bool   `json:"required_rhetorical_question"`
	MetaphorDescription         string `json:"metaphor_description"`
}

type ValueOrientation struct {
	Type             string   `json:"type"`
	EmotionalGradient string  `json:"emotional_gradient"`
	Keywords         []string `json:"keywords"`
}

type TitleGuidelines struct {
	Length           CountRange `json:"length"`
	Style            string     `json:"style"`
	ForbiddenPatterns []string  `json:"forbidden_patterns"`
	Examples         []string   `json:"examples"`
}

type FactGuard struct {
	FutureTenseRequired []string `json:"future_tense_required"`
	ForbiddenResults    []string `json:"forbidden_results"`
	UserMaterialPriority bool    `json:"user_material_priority"`
}

type OutputFormat struct {
	UseMarkdown          bool   `json:"use_markdown"`
	TitlePrefix          string `json:"title_prefix"`
	Separator            string `json:"separator"`
	IncludeModificationNotes bool `json:"include_modification_notes"`
	NoteLabel            string `json:"note_label"`
}

// StyleOption is the summary shown in the style picker.
type StyleOption struct {
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     int      `json:"version"`
	WordRange   [2]int   `json:"word_range"`
	Tags        []string `json:"tags"`
}

// AdminProfileInfo is the admin-facing profile info (includes status and metadata).
type AdminProfileInfo struct {
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     int      `json:"version"`
	Status      string   `json:"status"` // published | draft | archived
	Tags        []string `json:"tags"`
	WordRange   [2]int   `json:"word_range"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// Loader loads style profiles with caching.
type Loader struct {
	profiles map[string]*StyleProfile
	mu       sync.RWMutex
	ttl      time.Duration
	lastLoad time.Time

	// Profile statuses (slug → status)
	statuses map[string]string

	// Rollout configs (slug → RolloutConfig)
	rolloutConfigs map[string]routing.RolloutConfig

	// DB connection (optional — if nil, uses built-in profiles only)
	db *database.DB

	// fallbackCache caches fallback version profiles loaded from DB (LRU)
	fallbackCache *profileLRUCache

	// L2 cache (optional — Redis or in-process LRU for cross-instance caching)
	l2Cache *ProfileL2Cache

	// PublishHook is called when a profile is published (for auto-evaluation trigger)
	PublishHook func(slug string, version int, detail string)

	// Metrics (lightweight atomic counters, read by server /metrics endpoint)
	L1Hits atomic.Int64
	L2Hits atomic.Int64
	Misses atomic.Int64
}

// NewLoader creates a new profile loader with built-in profiles.
func NewLoader() *Loader {
	l := &Loader{
		profiles:       getBuiltinProfiles(),
		statuses:       make(map[string]string),
		rolloutConfigs: make(map[string]routing.RolloutConfig),
		ttl:            5 * time.Minute,
		fallbackCache:  newProfileLRUCache(64),
	}
	// Mark built-in profiles as published with full rollout
	for slug := range l.profiles {
		l.statuses[slug] = "published"
		l.rolloutConfigs[slug] = routing.RolloutConfig{
			Type:           "full",
			RolloutPercent: 100,
		}
	}
	return l
}

// WithDB sets the database connection and attempts to load profiles from DB.
// If the DB has no profiles, it seeds the built-in profiles first.
func (l *Loader) WithDB(db *database.DB) *Loader {
	l.db = db
	if db != nil {
		l.LoadFromDB()
	}
	return l
}

// WithL2Cache sets the L2 cache backend (e.g., Redis or in-process LRU).
// This is optional — if not set, only the in-memory L1 cache is used.
func (l *Loader) WithL2Cache(cache *ProfileL2Cache) *Loader {
	l.l2Cache = cache
	return l
}

// LoadFromDB reads all style profiles from the database and replaces
// the in-memory cache. If the DB has no profiles, it seeds built-in profiles.
func (l *Loader) LoadFromDB() {
	if l.db == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := l.db.QueryContext(ctx, `
		SELECT slug, name, description, version, status, config,
		       rollout_type, whitelist_uids, rollout_percent
		FROM style_profiles
		ORDER BY updated_at DESC
	`)
	if err != nil {
		slog.Warn("failed to load profiles from DB, using built-in", "error", err)
		return
	}
	defer rows.Close()

	dbProfiles := make(map[string]*StyleProfile)
	dbStatuses := make(map[string]string)
	dbRolloutConfigs := make(map[string]routing.RolloutConfig)

	for rows.Next() {
		var (
			slug            string
			name            string
			description     string
			version         int
			status          string
			configJSON      []byte
			rolloutType     string
			whitelistUIDs   []string
			rolloutPercent  int
		)

		if err := rows.Scan(&slug, &name, &description, &version, &status,
			&configJSON, &rolloutType, pq.Array(&whitelistUIDs), &rolloutPercent); err != nil {
			slog.Warn("failed to scan profile row", "error", err)
			continue
		}

		var p StyleProfile
		if err := json.Unmarshal(configJSON, &p); err != nil {
			slog.Warn("failed to unmarshal profile config", "slug", slug, "error", err)
			continue
		}

		// Ensure slug matches
		if p.Slug == "" {
			p.Slug = slug
		}
		if p.Name == "" {
			p.Name = name
		}
		if p.Description == "" {
			p.Description = description
		}
		if p.Version == 0 {
			p.Version = version
		}

		dbProfiles[slug] = &p
		dbStatuses[slug] = status

		// Store rollout config
		dbRolloutConfigs[slug] = routing.RolloutConfig{
			Type:           rolloutType,
			WhitelistUIDs:  whitelistUIDs,
			RolloutPercent: rolloutPercent,
		}
	}

	if len(dbProfiles) == 0 {
		// DB is empty — seed built-in profiles
		slog.Info("no profiles in DB, seeding built-in profiles")
		l.seedBuiltinToDB(ctx)
		return
	}

	l.mu.Lock()
	l.profiles = dbProfiles
	l.statuses = dbStatuses
	l.rolloutConfigs = dbRolloutConfigs
	l.lastLoad = time.Now()
	l.mu.Unlock()

	slog.Info("profiles loaded from DB", "count", len(dbProfiles))
}

// seedBuiltinToDB writes the built-in profiles to the database.
func (l *Loader) seedBuiltinToDB(ctx context.Context) {
	builtins := getBuiltinProfiles()

	l.mu.Lock()
	l.profiles = builtins
	for slug := range builtins {
		l.statuses[slug] = "published"
	}
	l.mu.Unlock()

	for slug, p := range builtins {
		configJSON, _ := json.Marshal(p)
		_, err := l.db.ExecContext(ctx, `
			INSERT INTO style_profiles (id, slug, name, description, version, status, config,
				rollout_type, whitelist_uids, rollout_percent,
				published_at, published_by, created_at, updated_at)
			VALUES (uuid_generate_v4(), $1, $2, $3, $4, 'published', $5,
				'full', '{}', 100, NOW(), 'system', NOW(), NOW())
			ON CONFLICT (slug) DO NOTHING
		`, slug, p.Name, p.Description, p.Version, string(configJSON))
		if err != nil {
			slog.Warn("failed to seed profile to DB", "slug", slug, "error", err)
		} else {
			slog.Info("seeded profile to DB", "slug", slug, "version", p.Version)
		}

		// Also create a version record
		_, _ = l.db.ExecContext(ctx, `
			INSERT INTO profile_versions (id, profile_slug, version, config, changelog, status, published_at, created_at, created_by)
			VALUES (uuid_generate_v4(), $1, $2, $3, 'Initial seed', 'published', NOW(), NOW(), 'system')
			ON CONFLICT (profile_slug, version) DO NOTHING
		`, slug, p.Version, string(configJSON))
	}
}

// maybeRefresh checks if the cache TTL has expired and refreshes from DB.
func (l *Loader) maybeRefresh() {
	if l.db == nil {
		return
	}
	if time.Since(l.lastLoad) < l.ttl {
		return
	}
	go l.LoadFromDB()
}

// Publish marks a profile as published, persists to DB, and triggers the publish hook.
func (l *Loader) Publish(slug string, version int, detail string) error {
	l.mu.Lock()
	p, ok := l.profiles[slug]
	if !ok {
		l.mu.Unlock()
		return fmt.Errorf("profile not found: %s", slug)
	}

	// Validate before publishing
	if err := ValidateProfile(p); err != nil {
		l.mu.Unlock()
		return fmt.Errorf("profile validation failed: %w", err)
	}

	p.Version = version
	if p.Version == 0 {
		p.Version = 1
	}
	p.Version = version

	// Update status in memory
	l.statuses[slug] = "published"

	// Invalidate cached fallback versions for this slug
	l.fallbackCache.InvalidateSlug(slug)
	if l.l2Cache != nil {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
		l.l2Cache.Invalidate(ctx2, slug)
		cancel2()
	}

	// Persist to DB if available
	var dbErr error
	if l.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		configJSON, _ := json.Marshal(p)

		// Update style_profiles table
		_, err := l.db.ExecContext(ctx, `
			UPDATE style_profiles
			SET version = $2, status = 'published', config = $3,
			    published_at = NOW(), updated_at = NOW()
			WHERE slug = $1
		`, slug, p.Version, string(configJSON))
		if err != nil {
			slog.Warn("failed to update style_profiles on publish", "slug", slug, "error", err)
			dbErr = err
		} else {
			// Insert into profile_versions table
			changelog := detail
			if changelog == "" {
				changelog = fmt.Sprintf("Published version %d", p.Version)
			}
			_, err = l.db.ExecContext(ctx, `
				INSERT INTO profile_versions (id, profile_slug, version, config, changelog, status, published_at, created_at, created_by)
				VALUES (uuid_generate_v4(), $1, $2, $3, $4, 'published', NOW(), NOW(), 'system')
				ON CONFLICT (profile_slug, version) DO UPDATE SET
					config = EXCLUDED.config,
					changelog = EXCLUDED.changelog,
					status = 'published',
					published_at = NOW()
			`, slug, p.Version, string(configJSON), changelog)
			if err != nil {
				slog.Warn("failed to insert profile_version on publish", "slug", slug, "error", err)
				dbErr = err
			}
		}
	}

	l.mu.Unlock()

	slog.Info("profile published", "slug", slug, "version", version, "db_error", dbErr)

	// Trigger publish hook (for auto-evaluation)
	if l.PublishHook != nil {
		go l.PublishHook(slug, version, detail)
	}

	return dbErr
}

// ValidateProfile checks a profile config before publishing.
// Returns an error if any validation rule fails.
//
// Rules (from docs/04-style-profile.md §5.1):
//  1. system_prompt must not be empty
//  2. word_range.max > word_range.min
//  3. title_guidelines.forbidden_patterns regexes must compile
//  4. structure.type must be a valid value
//  5. JSON must be valid (implicit — struct already unmarshalled)
func ValidateProfile(p *StyleProfile) error {
	if p == nil {
		return fmt.Errorf("profile is nil")
	}
	// Rule 1: system_prompt not empty
	if p.SystemPrompt == "" {
		return fmt.Errorf("system_prompt must not be empty")
	}
	// Rule 2: word_range.max > word_range.min
	if p.WordRange.Max > 0 && p.WordRange.Max <= p.WordRange.Min {
		return fmt.Errorf("word_range.max (%d) must be greater than word_range.min (%d)", p.WordRange.Max, p.WordRange.Min)
	}
	// Rule 3: forbidden_patterns regexes must compile
	for _, pattern := range p.TitleGuidelines.ForbiddenPatterns {
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("title_guidelines.forbidden_patterns regex '%s' is invalid: %w", pattern, err)
		}
	}
	// Rule 4: structure.type must be valid
	validStructures := map[string]bool{"three_part": true, "free_form": true, "custom": true, "": true}
	if !validStructures[p.Structure.Type] {
		return fmt.Errorf("structure.type '%s' is not valid (must be three_part, free_form, or custom)", p.Structure.Type)
	}
	return nil
}

// RepublishVersion loads an old version's config and publishes it as a new version.
// This implements the version rollback / republish feature described in docs/04-style-profile.md §5.2.
// It:
//  1. Reads the old version's config from profile_versions table
//  2. Assigns a new version number
//  3. Updates the in-memory profile
//  4. Persists to style_profiles and profile_versions
//  5. Triggers the publish hook (for auto-evaluation)
func (l *Loader) RepublishVersion(slug string, oldVersion, newVersion int, changelog string) error {
	if l.db == nil {
		return fmt.Errorf("database not available for version republish")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Step 1: Load old version config from profile_versions
	var configJSON []byte
	err := l.db.QueryRowContext(ctx, `
		SELECT config FROM profile_versions
		WHERE profile_slug = $1 AND version = $2
	`, slug, oldVersion).Scan(&configJSON)
	if err != nil {
		return fmt.Errorf("failed to load version %d of profile '%s': %w", oldVersion, slug, err)
	}

	var p StyleProfile
	if err := json.Unmarshal(configJSON, &p); err != nil {
		return fmt.Errorf("failed to unmarshal old version config: %w", err)
	}
	if p.Slug == "" {
		p.Slug = slug
	}
	p.Version = newVersion

	// Validate the old config before republishing
	if err := ValidateProfile(&p); err != nil {
		return fmt.Errorf("old version config failed validation: %w", err)
	}

	l.mu.Lock()
	// Update in-memory profile
	l.profiles[slug] = &p
	l.statuses[slug] = "published"
	l.fallbackCache.InvalidateSlug(slug)
	if l.l2Cache != nil {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
		l.l2Cache.Invalidate(ctx2, slug)
		cancel2()
	}

	// Persist to DB
	newConfigJSON, _ := json.Marshal(p)
	changelogStr := changelog
	if changelogStr == "" {
		changelogStr = fmt.Sprintf("Republished from version %d", oldVersion)
	}

	_, dbErr := l.db.ExecContext(ctx, `
		UPDATE style_profiles
		SET version = $2, status = 'published', config = $3,
		    published_at = NOW(), updated_at = NOW()
		WHERE slug = $1
	`, slug, newVersion, string(newConfigJSON))
	if dbErr != nil {
		slog.Warn("failed to update style_profiles on republish", "slug", slug, "error", dbErr)
	} else {
		_, err := l.db.ExecContext(ctx, `
			INSERT INTO profile_versions (id, profile_slug, version, config, changelog, status, published_at, created_at, created_by)
			VALUES (uuid_generate_v4(), $1, $2, $3, $4, 'published', NOW(), NOW(), 'system')
			ON CONFLICT (profile_slug, version) DO UPDATE SET
				config = EXCLUDED.config,
				changelog = EXCLUDED.changelog,
				status = 'published',
				published_at = NOW()
		`, slug, newVersion, string(newConfigJSON), changelogStr)
		if err != nil {
			slog.Warn("failed to insert profile_version on republish", "slug", slug, "error", err)
			dbErr = err
		}
	}

	l.mu.Unlock()

	slog.Info("profile republished from old version",
		"slug", slug, "old_version", oldVersion, "new_version", newVersion, "db_error", dbErr)

	// Trigger publish hook
	if l.PublishHook != nil {
		go l.PublishHook(slug, newVersion, changelogStr)
	}

	return dbErr
}

// UpdateProfile updates a profile configuration in memory.
func (l *Loader) UpdateProfile(slug string, config *StyleProfile) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.profiles[slug] = config
	// Invalidate any cached fallback versions for this slug
	l.fallbackCache.InvalidateSlug(slug)
	if l.l2Cache != nil {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
		l.l2Cache.Invalidate(ctx2, slug)
		cancel2()
	}
	slog.Info("profile updated", "slug", slug, "version", config.Version)
}

// CreateProfile creates a new profile in memory.
func (l *Loader) CreateProfile(config *StyleProfile) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if config.Slug == "" {
		return fmt.Errorf("slug is required")
	}
	if _, exists := l.profiles[config.Slug]; exists {
		return fmt.Errorf("profile with slug '%s' already exists", config.Slug)
	}

	l.profiles[config.Slug] = config
	l.statuses[config.Slug] = "draft"
	slog.Info("profile created", "slug", config.Slug, "name", config.Name)
	return nil
}

// ArchiveProfile marks a profile as archived.
func (l *Loader) ArchiveProfile(slug string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.statuses[slug] = "archived"
	slog.Info("profile archived", "slug", slug)
}

// GetStatus returns the status of a profile.
func (l *Loader) GetStatus(slug string) string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if status, ok := l.statuses[slug]; ok {
		return status
	}
	return "published"
}

// ListAll returns all profiles with admin metadata (including drafts).
func (l *Loader) ListAll() []AdminProfileInfo {
	l.mu.RLock()
	defer l.mu.RUnlock()

	result := make([]AdminProfileInfo, 0, len(l.profiles))
	for _, p := range l.profiles {
		status := "published"
		if s, ok := l.statuses[p.Slug]; ok {
			status = s
		}
		result = append(result, AdminProfileInfo{
			Slug:        p.Slug,
			Name:        p.Name,
			Description: p.Description,
			Version:     p.Version,
			Status:      status,
			Tags:        p.Tags,
			WordRange:   [2]int{p.WordRange.Min, p.WordRange.Max},
		})
	}
	return result
}

// GetDetail returns the full profile configuration.
func (l *Loader) GetDetail(slug string) (*StyleProfile, bool) {
	return l.Get(slug)
}

// Get returns the profile for the given slug. Falls back to "yinyue" if not found.
func (l *Loader) Get(slug string) (*StyleProfile, bool) {
	l.maybeRefresh()

	// Try L2 cache first (if configured)
	if l.l2Cache != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if p, ok := l.l2Cache.Get(ctx, slug); ok {
			cancel()
			l.L2Hits.Add(1)
			return p, true
		}
		cancel()
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	if p, ok := l.profiles[slug]; ok {
		l.L1Hits.Add(1)
		// Populate L2 cache on L1 hit
		if l.l2Cache != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			l.l2Cache.Set(ctx, slug, p)
			cancel()
		}
		return p, true
	}

	// Fallback to yinyue
	if p, ok := l.profiles["yinyue"]; ok {
		l.Misses.Add(1)
		slog.Warn("style profile not found, falling back to yinyue", "requested", slug)
		return p, true
	}

	return nil, false
}

// GetForUser returns the profile for the given slug, applying grayscale routing.
// If the user is selected for the new version, they get the current profile.
// Otherwise, they get the fallback version (if configured).
func (l *Loader) GetForUser(slug, userID string) (*StyleProfile, bool) {
	l.maybeRefresh()

	l.mu.RLock()
	defer l.mu.RUnlock()

	p, ok := l.profiles[slug]
	if !ok {
		// Fallback to yinyue
		if p, ok := l.profiles["yinyue"]; ok {
			slog.Warn("style profile not found, falling back to yinyue", "requested", slug)
			return p, true
		}
		return nil, false
	}

	// If no user ID, return the profile as-is (anonymous user)
	if userID == "" || userID == "anonymous" {
		return p, true
	}

	// Check rollout config
	config, hasConfig := l.rolloutConfigs[slug]
	if !hasConfig {
		return p, true // Default: full rollout
	}

	// If full rollout or user is selected, return current version
	if routing.ShouldUseNewVersion(userID, config) {
		return p, true
	}

	// User is not selected — try to load the fallback version from DB
	if config.FallbackVersion > 0 && config.FallbackVersion < p.Version && l.db != nil {
		// Check LRU cache first
		if cached, ok := l.fallbackCache.Get(slug, config.FallbackVersion); ok {
			slog.Debug("serving fallback profile from LRU cache",
				"slug", slug, "user", userID,
				"current_version", p.Version, "fallback_version", config.FallbackVersion)
			return cached, true
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var configJSON []byte
		err := l.db.QueryRowContext(ctx, `
			SELECT config FROM profile_versions
			WHERE profile_slug = $1 AND version = $2 AND status = 'published'
		`, slug, config.FallbackVersion).Scan(&configJSON)
		if err == nil && len(configJSON) > 0 {
			var fallback StyleProfile
			if json.Unmarshal(configJSON, &fallback) == nil {
				if fallback.Slug == "" {
					fallback.Slug = slug
				}
				// Cache for future lookups
				l.fallbackCache.Put(slug, config.FallbackVersion, &fallback)
				slog.Info("serving fallback profile version",
					"slug", slug, "user", userID,
					"current_version", p.Version, "fallback_version", config.FallbackVersion)
				return &fallback, true
			}
		}
	}

	// If fallback fails, return current version (fail open)
	return p, true
}

// GetRolloutConfig returns the rollout configuration for a profile.
func (l *Loader) GetRolloutConfig(slug string) routing.RolloutConfig {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if config, ok := l.rolloutConfigs[slug]; ok {
		return config
	}
	return routing.RolloutConfig{
		Type:           "full",
		RolloutPercent: 100,
	}
}

// UpdateRolloutConfig updates the rollout configuration for a profile in memory and DB.
func (l *Loader) UpdateRolloutConfig(slug string, config routing.RolloutConfig) error {
	l.mu.Lock()
	l.rolloutConfigs[slug] = config
	l.mu.Unlock()

	if l.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := l.db.ExecContext(ctx, `
			UPDATE style_profiles
			SET rollout_type = $2, whitelist_uids = $3, rollout_percent = $4, updated_at = NOW()
			WHERE slug = $1
		`, slug, config.Type, pq.Array(config.WhitelistUIDs), config.RolloutPercent)
		if err != nil {
			slog.Warn("failed to persist rollout config", "slug", slug, "error", err)
			return err
		}
	}

	slog.Info("rollout config updated", "slug", slug, "type", config.Type, "percent", config.RolloutPercent)
	return nil
}

// List returns all available profiles as StyleOptions.
func (l *Loader) List() []StyleOption {
	l.maybeRefresh()

	l.mu.RLock()
	defer l.mu.RUnlock()

	options := make([]StyleOption, 0, len(l.profiles))
	for _, p := range l.profiles {
		options = append(options, StyleOption{
			Slug:        p.Slug,
			Name:        p.Name,
			Description: p.Description,
			Version:     p.Version,
			WordRange:   [2]int{p.WordRange.Min, p.WordRange.Max},
			Tags:        p.Tags,
		})
	}
	return options
}

// getBuiltinProfiles returns the built-in style profiles.
func getBuiltinProfiles() map[string]*StyleProfile {
	yinyueJSON := `{}`

	shenlunJSON := `{}`

	xiaohongshuJSON := `{}`

	profiles := make(map[string]*StyleProfile)
	for _, jsonStr := range []string{yinyueJSON, shenlunJSON, xiaohongshuJSON} {
		var p StyleProfile
		if err := json.Unmarshal([]byte(jsonStr), &p); err != nil {
			slog.Error("failed to parse builtin profile", "error", err)
			continue
		}
		profiles[p.Slug] = &p
	}

	return profiles
}
