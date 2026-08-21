package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
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

// RedisClient is a minimal Redis client interface.
// Uses github.com/redis/go-redis/v9 when Redis is enabled.
type RedisClient struct {
	// In production, this wraps *redis.Client.
	// For now, we use a simplified interface that can be implemented
	// with any Redis-compatible client.
	client RedisCmdable
}

// RedisCmdable defines the minimal Redis command set we need.
type RedisCmdable interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, ttl time.Duration) error
	Del(ctx context.Context, keys ...string) (int64, error)
	Keys(ctx context.Context, pattern string) ([]string, error)
	HSet(ctx context.Context, key, field, value string) error
	HGet(ctx context.Context, key, field string) (string, error)
	HGetAll(ctx context.Context, key string) (map[string]string, error)
	HDel(ctx context.Context, key string, fields ...string) (int64, error)
	Expire(ctx context.Context, key string, ttl time.Duration) error
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
