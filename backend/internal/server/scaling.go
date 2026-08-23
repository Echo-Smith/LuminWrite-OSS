package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	redislib "github.com/redis/go-redis/v9"
)

// ─── Horizontal Scaling: Session State Externalization ───
//
// When running multiple backend instances behind a load balancer, the in-memory
// sync.Map (s.sessions) is not shared across instances. This Redis-backed adapter
// provides a distributed session registry so that any instance can:
//
// 1. Check if a session is active on another instance (for resume)
// 2. Discover which instance owns a session (for sticky routing)
// 3. Clean up orphaned sessions when an instance crashes
//
// Design:
//   - Each instance registers its active sessions in Redis with a TTL
//   - The heartbeat goroutine refreshes TTL for active sessions
//   - On graceful shutdown, sessions are explicitly deregistered
//   - On crash, sessions expire after TTL and are marked as failed

// SessionMeta is the metadata stored in Redis for each active session.
type SessionMeta struct {
	TraceID       string `json:"trace_id"`
	UserID        string `json:"user_id"`
	InstanceID    string `json:"instance_id"`
	Status        string `json:"status"`
	StartedAt     int64  `json:"started_at"`
	LastHeartbeat int64  `json:"last_heartbeat"`
}

// RedisSessionAdapter manages session state in Redis for multi-instance deployments.
// When Redis is not available (single-instance mode), all methods are no-ops and
// the system falls back to the in-memory sync.Map.
type RedisSessionAdapter struct {
	redis      *RedisClient // nil = disabled (single-instance mode)
	instanceID string
	ttl        time.Duration
}

// RedisClient wraps a real go-redis client for session tracking,
// verification code storage, and rate limiting.
type RedisClient struct {
	client RedisCmdable
}

// NewRedisClient creates a real Redis connection from a URL string.
// Returns nil and logs a warning if connection fails.
func NewRedisClient(redisURL string) *RedisClient {
	opts, err := redislib.ParseURL(redisURL)
	if err != nil {
		slog.Error("failed to parse Redis URL", "error", err, "url", redisURL)
		return nil
	}

	client := redislib.NewClient(opts)

	// Ping to verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		slog.Error("failed to connect to Redis", "error", err, "url", redisURL)
		return nil
	}

	slog.Info("Redis connected successfully", "addr", opts.Addr)
	return &RedisClient{client: &redisCmdableAdapter{client: client}}
}

// Close closes the underlying Redis connection.
func (rc *RedisClient) Close() error {
	if rc == nil {
		return nil
	}
	// Type assert to *redislib.Client to access Close()
	if closer, ok := rc.client.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

// ─── Verification Code Storage ────────────────────────────

// CodeRateLimitKey returns the Redis key for rate limiting verification code sends.
const codeRateLimitPrefix = "luminbuddy:code:ratelimit:"
const codeStoragePrefix = "luminbuddy:code:"

// StoreVerificationCode stores a verification code in Redis, bound to the email address.
// The code expires after 5 minutes. Returns an error if the email has been rate-limited.
func (rc *RedisClient) StoreVerificationCode(ctx context.Context, email, code string) error {
	if rc == nil {
		return errors.New("redis not available")
	}

	// Rate limit: 60 seconds between code sends for the same email
	rateLimitKey := codeRateLimitPrefix + email
	set, err := rc.client.SetNX(ctx, rateLimitKey, "1", 60*time.Second)
	if err != nil {
		return fmt.Errorf("rate limit check failed: %w", err)
	}
	if !set {
		return errors.New("rate_limited")
	}

	// Store the code with 5-minute TTL
	codeKey := codeStoragePrefix + email
	return rc.client.Set(ctx, codeKey, code, 5*time.Minute)
}

// VerifyCode checks if the provided code matches the stored code for the email.
// If verified, the code is deleted (single-use). Returns false if code doesn't match or expired.
func (rc *RedisClient) VerifyCode(ctx context.Context, email, code string) (bool, error) {
	if rc == nil {
		return false, errors.New("redis not available")
	}

	codeKey := codeStoragePrefix + email
	storedCode, err := rc.client.Get(ctx, codeKey)
	if err != nil {
		return false, nil // code not found or expired
	}

	if storedCode != code {
		return false, nil
	}

	// Delete the code after successful verification (single-use)
	_, _ = rc.client.Del(ctx, codeKey)
	return true, nil
}

// DeleteVerificationCode removes a verification code from Redis (e.g., after registration).
func (rc *RedisClient) DeleteVerificationCode(ctx context.Context, email string) {
	if rc == nil {
		return
	}
	_, _ = rc.client.Del(ctx, codeStoragePrefix+email, codeRateLimitPrefix+email)
}

// RedisCmdable defines the minimal Redis command set we need.
type RedisCmdable interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, ttl time.Duration) error
	SetNX(ctx context.Context, key string, value string, ttl time.Duration) (bool, error)
	Del(ctx context.Context, keys ...string) (int64, error)
	Keys(ctx context.Context, pattern string) ([]string, error)
	HSet(ctx context.Context, key, field, value string) error
	HGet(ctx context.Context, key, field string) (string, error)
	HGetAll(ctx context.Context, key string) (map[string]string, error)
	HDel(ctx context.Context, key string, fields ...string) (int64, error)
	Expire(ctx context.Context, key string, ttl time.Duration) error
}

// redisCmdableAdapter wraps *redis.Client to conform to RedisCmdable.
// go-redis methods return *redis.StringCmd, *redis.BoolCmd, etc. —
// this adapter converts them to plain Go types.
type redisCmdableAdapter struct {
	client *redislib.Client
}

func (a *redisCmdableAdapter) Get(ctx context.Context, key string) (string, error) {
	val, err := a.client.Get(ctx, key).Result()
	if err == redislib.Nil {
		return "", nil
	}
	return val, err
}

func (a *redisCmdableAdapter) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	return a.client.Set(ctx, key, value, ttl).Err()
}

func (a *redisCmdableAdapter) SetNX(ctx context.Context, key string, value string, ttl time.Duration) (bool, error) {
	return a.client.SetNX(ctx, key, value, ttl).Result()
}

func (a *redisCmdableAdapter) Del(ctx context.Context, keys ...string) (int64, error) {
	return a.client.Del(ctx, keys...).Result()
}

func (a *redisCmdableAdapter) Keys(ctx context.Context, pattern string) ([]string, error) {
	return a.client.Keys(ctx, pattern).Result()
}

func (a *redisCmdableAdapter) HSet(ctx context.Context, key, field, value string) error {
	return a.client.HSet(ctx, key, field, value).Err()
}

func (a *redisCmdableAdapter) HGet(ctx context.Context, key, field string) (string, error) {
	val, err := a.client.HGet(ctx, key, field).Result()
	if err == redislib.Nil {
		return "", nil
	}
	return val, err
}

func (a *redisCmdableAdapter) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return a.client.HGetAll(ctx, key).Result()
}

func (a *redisCmdableAdapter) HDel(ctx context.Context, key string, fields ...string) (int64, error) {
	return a.client.HDel(ctx, key, fields...).Result()
}

func (a *redisCmdableAdapter) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return a.client.Expire(ctx, key, ttl).Err()
}

// NewRedisSessionAdapter creates a new adapter. If redis is nil, all methods are no-ops.
func NewRedisSessionAdapter(redis *RedisClient, instanceID string, ttl time.Duration) *RedisSessionAdapter {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &RedisSessionAdapter{
		redis:      redis,
		instanceID: instanceID,
		ttl:        ttl,
	}
}

// IsEnabled returns true if Redis-backed session tracking is active.
func (a *RedisSessionAdapter) IsEnabled() bool {
	return a.redis != nil
}

// RegisterSession registers a new session in Redis.
func (a *RedisSessionAdapter) RegisterSession(ctx context.Context, execCtx *engine.ExecutionContext) {
	if !a.IsEnabled() {
		return
	}

	meta := SessionMeta{
		TraceID:       execCtx.TraceID,
		UserID:        execCtx.UserID,
		InstanceID:    a.instanceID,
		Status:        string(execCtx.Status),
		StartedAt:     time.Now().Unix(),
		LastHeartbeat: time.Now().Unix(),
	}

	data, err := json.Marshal(meta)
	if err != nil {
		slog.Warn("redis: failed to marshal session meta", "error", err)
		return
	}

	key := fmt.Sprintf("luminbuddy:sessions:%s", execCtx.TraceID)
	if err := a.redis.client.Set(ctx, key, string(data), a.ttl); err != nil {
		slog.Warn("redis: failed to register session", "error", err, "trace_id", execCtx.TraceID)
	}
}

// UpdateSessionStatus updates the status of a session in Redis.
func (a *RedisSessionAdapter) UpdateSessionStatus(ctx context.Context, traceID string, status string) {
	if !a.IsEnabled() {
		return
	}

	key := fmt.Sprintf("luminbuddy:sessions:%s", traceID)
	data, err := a.redis.client.Get(ctx, key)
	if err != nil || data == "" {
		// Session not in Redis — may have expired or never registered
		return
	}

	var meta SessionMeta
	if err := json.Unmarshal([]byte(data), &meta); err != nil {
		return
	}

	meta.Status = status
	meta.LastHeartbeat = time.Now().Unix()

	newData, _ := json.Marshal(meta)
	if err := a.redis.client.Set(ctx, key, string(newData), a.ttl); err != nil {
		slog.Warn("redis: failed to update session status", "error", err, "trace_id", traceID)
	}
}

// UnregisterSession removes a session from Redis.
func (a *RedisSessionAdapter) UnregisterSession(ctx context.Context, traceID string) {
	if !a.IsEnabled() {
		return
	}

	key := fmt.Sprintf("luminbuddy:sessions:%s", traceID)
	if _, err := a.redis.client.Del(ctx, key); err != nil {
		slog.Warn("redis: failed to unregister session", "error", err, "trace_id", traceID)
	}
}

// GetSessionMeta retrieves session metadata from Redis.
// Returns nil if the session is not found (either expired or on another instance
// that has crashed).
func (a *RedisSessionAdapter) GetSessionMeta(ctx context.Context, traceID string) *SessionMeta {
	if !a.IsEnabled() {
		return nil
	}

	key := fmt.Sprintf("luminbuddy:sessions:%s", traceID)
	data, err := a.redis.client.Get(ctx, key)
	if err != nil || data == "" {
		return nil
	}

	var meta SessionMeta
	if err := json.Unmarshal([]byte(data), &meta); err != nil {
		return nil
	}
	return &meta
}

// ListUserSessions lists all active sessions for a user across all instances.
func (a *RedisSessionAdapter) ListUserSessions(ctx context.Context, userID string) []SessionMeta {
	if !a.IsEnabled() {
		return nil
	}

	pattern := fmt.Sprintf("luminbuddy:sessions:user:%s:*", userID)
	keys, err := a.redis.client.Keys(ctx, pattern)
	if err != nil {
		return nil
	}

	var sessions []SessionMeta
	for _, key := range keys {
		data, err := a.redis.client.Get(ctx, key)
		if err != nil || data == "" {
			continue
		}
		var meta SessionMeta
		if err := json.Unmarshal([]byte(data), &meta); err == nil {
			sessions = append(sessions, meta)
		}
	}
	return sessions
}

// CountActiveSessions counts all active sessions across all instances.
func (a *RedisSessionAdapter) CountActiveSessions(ctx context.Context) int {
	if !a.IsEnabled() {
		return 0
	}

	keys, err := a.redis.client.Keys(ctx, "luminbuddy:sessions:*")
	if err != nil {
		return 0
	}
	return len(keys)
}

// StartHeartbeat launches a goroutine that refreshes TTL for all sessions
// owned by this instance. Should be called once at server startup.
func (a *RedisSessionAdapter) StartHeartbeat(ctx context.Context, sessions *sync.Map) {
	if !a.IsEnabled() {
		return
	}

	ticker := time.NewTicker(a.ttl / 3)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sessions.Range(func(_, value interface{}) bool {
				execCtx, ok := value.(*engine.ExecutionContext)
				if !ok {
					return true
				}
				// Only refresh sessions that are still running or paused
				if execCtx.Status == engine.StatusRunning || execCtx.Status == engine.StatusPaused {
					a.RegisterSession(ctx, execCtx)
				}
				return true
			})
		}
	}
}

// getEnvInstanceID generates a unique instance identifier for multi-instance deployments.
// Priority:
//  1. INSTANCE_ID env var (explicit override)
//  2. hostname:port (default)
//  3. "single" (fallback)
func getEnvInstanceID(_ string, port int) string {
	if id := os.Getenv("INSTANCE_ID"); id != "" {
		return id
	}
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown"
	}
	return fmt.Sprintf("%s:%d", hostname, port)
}
