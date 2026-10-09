package packages

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
)

// Installer installs and uninstalls packages under the fixed install root,
// dispatching to the kind-specific registration:
//   - style:   the manifest entry (style config JSON) is applied to
//              user_style_profiles via an immutable version; the package
//              body stays on disk as the reinstall source;
//   - skill:   the package body on disk is the runtime form (SKILL.md +
//              scripts); the registry row is the listing anchor;
//   - service: the manifest entry (MCP server manifest) registers an
//              mcp_servers row; credentials stay user-supplied in the MCP UI.
type Installer struct {
	root      string
	repo      *database.InstalledPackageRepo
	styleRepo *database.UserStyleStore
	adminRepo *database.AdminRepo
}

// NewInstaller wires the installer. styleRepo/adminRepo may be nil in
// degraded deployments; the corresponding kinds then refuse installation
// with an explicit error rather than half-registering.
func NewInstaller(root string, repo *database.InstalledPackageRepo, styleRepo *database.UserStyleStore, adminRepo *database.AdminRepo) *Installer {
	if strings.TrimSpace(root) == "" {
		root = "data/packages"
	}
	return &Installer{root: root, repo: repo, styleRepo: styleRepo, adminRepo: adminRepo}
}

// Root returns the configured install root.
func (i *Installer) Root() string { return i.root }

// CatalogEntry is one builtin catalog item.
type CatalogEntry struct {
	Kind        string `json:"kind"`
	Slug        string `json:"slug"`
	Version     string `json:"version"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// Catalog is the builtin catalog document.
type Catalog struct {
	Packages []CatalogEntry `json:"packages"`
}

// InstallRequest selects a package source.
type InstallRequest struct {
	Source string // builtin | url
	URL    string // required when Source=url
	// Slug is required when Source=builtin (the catalog is keyed by slug).
	Slug string
}

// InstallResult reports the installed package.
type InstallResult struct {
	Package *database.InstalledPackage `json:"package"`
	// StyleProfileID is set for kind=style (the created profile row).
	StyleProfileID string `json:"style_profile_id,omitempty"`
	// MCPServerID is set for kind=service (the registered server row).
	MCPServerID string `json:"mcp_server_id,omitempty"`
}

// Install stages the package body, validates its manifest, moves it into the
// fixed install location, and registers it. Registration failures roll the
// install back (directory removed, no registry row).
func (i *Installer) Install(ctx context.Context, userID string, req InstallRequest) (*InstallResult, error) {
	if i.repo == nil {
		return nil, fmt.Errorf("package registry unavailable")
	}
	if userID == "" {
		return nil, fmt.Errorf("authentication required")
	}

	// Stage → validate → move → register.
	staging, manifest, cleanup, err := i.stage(ctx, req)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	if req.Source == SourceBuiltin && req.Slug != "" && manifest.Slug != req.Slug {
		return nil, fmt.Errorf("catalog slug %q does not match package manifest %q", req.Slug, manifest.Slug)
	}

	existing, err := i.repo.GetBySlug(ctx, userID, manifest.Kind, manifest.Slug)
	if err != nil && err != database.ErrInstalledPackageNotFound {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("%w: %s/%s (uninstall first)", ErrAlreadyInstalled, manifest.Kind, manifest.Slug)
	}

	target := InstallRoot(i.root, manifest.Kind, manifest.Slug, manifest.Version)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, fmt.Errorf("prepare install location: %w", err)
	}
	if _, err := os.Stat(target); err == nil {
		return nil, fmt.Errorf("install location already exists: %s", target)
	}
	if err := os.Rename(staging, target); err != nil {
		return nil, fmt.Errorf("move package into place: %w", err)
	}
	// From here on the directory exists; any failure must remove it.
	rollbackDir := func() { _ = RemoveTree(i.root, target) }

	result := &InstallResult{}
	switch manifest.Kind {
	case KindStyle:
		profileID, err := i.registerStyle(ctx, userID, target, manifest)
		if err != nil {
			rollbackDir()
			return nil, err
		}
		result.StyleProfileID = profileID
	case KindSkill:
		// The directory is the runtime form; nothing to register.
	case KindService:
		serverID, err := i.registerService(ctx, target, manifest)
		if err != nil {
			rollbackDir()
			return nil, err
		}
		result.MCPServerID = serverID
	default:
		rollbackDir()
		return nil, fmt.Errorf("unsupported package kind %q", manifest.Kind)
	}

	manifestJSON, _ := json.Marshal(manifestMap(manifest))
	row := &database.InstalledPackage{
		UserID:      userID,
		Kind:        manifest.Kind,
		Slug:        manifest.Slug,
		Version:     manifest.Version,
		Title:       manifest.Title,
		Description: manifest.Description,
		Source:      req.Source,
		InstallPath: target,
		Manifest:    manifestMap(manifest),
	}
	created, err := i.repo.Create(ctx, row)
	if err != nil {
		rollbackDir()
		if result.StyleProfileID != "" {
			_ = i.styleRepo.DeleteProfile(ctx, result.StyleProfileID)
		}
		if result.MCPServerID != "" {
			_ = i.adminRepo.DeleteMCPServer(ctx, result.MCPServerID)
		}
		return nil, fmt.Errorf("register package: %w", err)
	}
	_ = manifestJSON
	result.Package = created
	slog.Info("package installed", "user_id", userID, "kind", manifest.Kind, "slug", manifest.Slug, "version", manifest.Version, "source", req.Source)
	return result, nil
}

// stage produces a staged package directory plus its manifest.
func (i *Installer) stage(ctx context.Context, req InstallRequest) (dir string, manifest *Manifest, cleanup func(), err error) {
	switch req.Source {
	case SourceBuiltin:
		if req.Slug == "" {
			return "", nil, nil, fmt.Errorf("builtin install requires a slug")
		}
		return i.stageBuiltin(req.Slug)
	case SourceURL:
		if strings.TrimSpace(req.URL) == "" {
			return "", nil, nil, fmt.Errorf("url install requires a package url")
		}
		return i.stageURL(ctx, strings.TrimSpace(req.URL))
	default:
		return "", nil, nil, fmt.Errorf("source must be builtin or url")
	}
}

// stageBuiltin copies the embedded package body into a staging directory.
func (i *Installer) stageBuiltin(slug string) (string, *Manifest, func(), error) {
	entry, ok := builtinBySlug(slug)
	if !ok {
		return "", nil, nil, fmt.Errorf("builtin package %q is not in the catalog", slug)
	}
	staging, err := os.MkdirTemp(i.root, ".staging-")
	if err != nil {
		return "", nil, nil, fmt.Errorf("create staging dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(staging) }
	if err := copyBuiltin(entry.Kind, entry.Slug, entry.Version, staging); err != nil {
		cleanup()
		return "", nil, nil, err
	}
	manifest, err := readManifest(staging)
	if err != nil {
		cleanup()
		return "", nil, nil, err
	}
	return staging, manifest, cleanup, nil
}

// stageURL downloads and extracts a zip package into a staging directory.
func (i *Installer) stageURL(ctx context.Context, rawURL string) (string, *Manifest, func(), error) {
	body, err := FetchPackage(rawURL)
	if err != nil {
		return "", nil, nil, err
	}
	staging, err := os.MkdirTemp(i.root, ".staging-")
	if err != nil {
		return "", nil, nil, fmt.Errorf("create staging dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(staging) }
	if err := ExtractZip(body, staging); err != nil {
		cleanup()
		return "", nil, nil, err
	}
	manifest, err := readManifest(staging)
	if err != nil {
		cleanup()
		return "", nil, nil, err
	}
	return staging, manifest, cleanup, nil
}

// readManifest loads and parses package.json from a staged directory.
func readManifest(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil, fmt.Errorf("package.json missing: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("package.json invalid: %w", err)
	}
	return &manifest, nil
}

// registerStyle applies a style package: create the user profile and save
// the package's style config as its first immutable version.
func (i *Installer) registerStyle(ctx context.Context, userID, dir string, manifest *Manifest) (string, error) {
	if i.styleRepo == nil {
		return "", fmt.Errorf("style installation is unavailable on this deployment")
	}
	if manifest.Entry == "" {
		return "", fmt.Errorf("style package has no entry (style config)")
	}
	config, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(manifest.Entry)))
	if err != nil {
		return "", fmt.Errorf("read style config: %w", err)
	}
	if !json.Valid(config) {
		return "", fmt.Errorf("style config is not valid JSON")
	}
	profile, err := i.styleRepo.CreateProfile(ctx, userID, manifest.Slug, manifest.Title, manifest.Description)
	if err != nil {
		return "", fmt.Errorf("create style profile: %w", err)
	}
	if _, err := i.styleRepo.SaveVersion(ctx, profile.ID, json.RawMessage(config),
		fmt.Sprintf("installed from package %s@%s", manifest.Slug, manifest.Version)); err != nil {
		// Roll the profile back so a failed install leaves no shell.
		_ = i.styleRepo.DeleteProfile(ctx, profile.ID)
		return "", fmt.Errorf("save style config: %w", err)
	}
	return profile.ID, nil
}

// registerService registers an MCP server from the package manifest entry.
// Credentials are intentionally not part of the package: the user supplies
// keys in the MCP service UI afterwards.
func (i *Installer) registerService(ctx context.Context, dir string, manifest *Manifest) (string, error) {
	if i.adminRepo == nil {
		return "", fmt.Errorf("service installation is unavailable on this deployment")
	}
	if manifest.Entry == "" {
		return "", fmt.Errorf("service package has no entry (server manifest)")
	}
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(manifest.Entry)))
	if err != nil {
		return "", fmt.Errorf("read server manifest: %w", err)
	}
	var spec struct {
		Name        string   `json:"name"`
		Transport   string   `json:"transport"`
		Command     string   `json:"command"`
		Args        []string `json:"args"`
		URL         string   `json:"url"`
		Description string   `json:"description"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		return "", fmt.Errorf("server manifest invalid: %w", err)
	}
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		name = manifest.Slug
	}
	transport := strings.TrimSpace(spec.Transport)
	if transport == "" {
		transport = "stdio"
	}
	created, err := i.adminRepo.CreateMCPServer(ctx, &database.MCPServerConfig{
		Name:        name,
		Transport:   transport,
		Command:     spec.Command,
		Args:        spec.Args,
		URL:         spec.URL,
		Description: spec.Description,
		IsActive:    true,
	})
	if err != nil {
		return "", fmt.Errorf("register MCP server: %w", err)
	}
	return created.ID, nil
}

// Uninstall removes a package and everything its install registered.
func (i *Installer) Uninstall(ctx context.Context, userID, id string) error {
	if i.repo == nil {
		return fmt.Errorf("package registry unavailable")
	}
	pkg, err := i.repo.GetForUser(ctx, userID, id)
	if err != nil {
		return err // ErrInstalledPackageNotFound maps to 404
	}

	switch pkg.Kind {
	case KindStyle:
		if i.styleRepo != nil {
			if profile, err := i.styleRepo.GetProfileBySlugAndOwner(ctx, pkg.Slug, userID); err == nil && profile != nil {
				if err := i.styleRepo.DeleteProfile(ctx, profile.ID); err != nil {
					return fmt.Errorf("remove installed style: %w", err)
				}
			}
		}
	case KindService:
		if i.adminRepo != nil {
			servers, err := i.adminRepo.ListMCPServers(ctx)
			if err == nil {
				for _, server := range servers {
					if server.Name == pkg.Slug || server.ID == fmt.Sprint(pkg.Manifest["mcp_server_id"]) {
						_ = i.adminRepo.DeleteMCPServer(ctx, server.ID)
						break
					}
				}
			}
		}
	case KindSkill:
		// Directory-only form.
	}

	if err := RemoveTree(i.root, pkg.InstallPath); err != nil {
		slog.Warn("package uninstall: directory removal failed", "error", err, "path", pkg.InstallPath)
	}
	if err := i.repo.Delete(ctx, userID, id); err != nil {
		return err
	}
	slog.Info("package uninstalled", "user_id", userID, "kind", pkg.Kind, "slug", pkg.Slug)
	return nil
}

// manifestMap projects the manifest for JSONB storage.
func manifestMap(m *Manifest) map[string]interface{} {
	return map[string]interface{}{
		"kind":        m.Kind,
		"slug":        m.Slug,
		"version":     m.Version,
		"title":       m.Title,
		"description": m.Description,
		"entry":       m.Entry,
	}
}
