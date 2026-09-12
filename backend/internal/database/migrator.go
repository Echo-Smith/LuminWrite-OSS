package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// migrationDB is the subset of database operations the migration engine
// needs. Both *DB (the pooled wrapper) and *sql.Conn (a single pinned
// connection) satisfy it, so MigrateDB can run the whole migration on one
// connection while tests keep calling the helpers with the pool.
type migrationDB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// migrationAdvisoryLockKey is a fixed application-wide advisory-lock key that
// serializes concurrent migration runs — multiple test binaries sharing one
// fresh TEST_DATABASE_URL, or multiple backend instances migrating at startup.
// Without it, two migrators both read an empty schema_migrations, both apply
// the same migration, and collide on the primary key or on non-idempotent DDL.
const migrationAdvisoryLockKey int64 = 7258234

// ─── 迁移引擎 ─────────────────────────────────────────────
//
// 设计目标：
//   1. 自动发现 — 扫描 embed.FS 中的 migrations/*.up.sql，无需手动维护列表
//   2. 追踪表 — schema_migrations 记录已应用的迁移版本和校验和
//   3. 事务包裹 — 每条迁移在独立事务中执行，失败则回滚，数据库不留半成品
//   4. 校验和 — SHA-256 验证已应用迁移未被篡改
//   5. 幂等 — 多次运行只执行未应用的迁移；已应用的跳过
//   6. 向后兼容 — 现有数据库（无追踪表）首次运行时自动 baseline

// migrationRecord 追踪表中的一条记录
type migrationRecord struct {
	Version   string    // 迁移版本号，如 "041_knowledge_lifecycle"
	Checksum  string    // SQL 内容的 SHA-256
	AppliedAt time.Time // 应用时间
}

// ensureMigrationsTable 创建 schema_migrations 追踪表（如果不存在）
func ensureMigrationsTable(ctx context.Context, db migrationDB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version     VARCHAR(256) PRIMARY KEY,
			checksum    VARCHAR(64)  NOT NULL,
			applied_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}
	return nil
}

// getAppliedMigrations 查询已应用的迁移记录
func getAppliedMigrations(ctx context.Context, db migrationDB) (map[string]migrationRecord, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT version, checksum, applied_at FROM schema_migrations
	`)
	if err != nil {
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]migrationRecord)
	for rows.Next() {
		var rec migrationRecord
		if err := rows.Scan(&rec.Version, &rec.Checksum, &rec.AppliedAt); err != nil {
			return nil, fmt.Errorf("scan schema_migrations row: %w", err)
		}
		applied[rec.Version] = rec
	}
	return applied, rows.Err()
}

// computeChecksum 计算迁移 SQL 内容的 SHA-256
func computeChecksum(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

func verifyMigrationChecksum(version, appliedChecksum, currentChecksum string) error {
	if appliedChecksum == currentChecksum {
		return nil
	}
	return fmt.Errorf("migration checksum mismatch for %s: applied=%s current=%s",
		version, appliedChecksum, currentChecksum)
}

// discoverMigrations 从 embed.FS 自动发现所有迁移文件，按文件名排序
func discoverMigrations(fs embed.FS) ([]string, error) {
	entries, err := fs.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations directory: %w", err)
	}

	var upFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		// 去掉 .up.sql 后缀，得到版本号
		version := strings.TrimSuffix(name, ".up.sql")
		upFiles = append(upFiles, version)
	}

	sort.Strings(upFiles)
	return upFiles, nil
}

// runMigration 在事务中执行单条迁移
func runMigration(ctx context.Context, db migrationDB, version, sqlContent, checksum string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx for migration %s: %w", version, err)
	}
	defer func() {
		if tx != nil {
			tx.Rollback()
		}
	}()

	// 执行迁移 SQL
	if _, err := tx.ExecContext(ctx, sqlContent); err != nil {
		return fmt.Errorf("execute migration %s: %w", version, err)
	}

	// 记录到追踪表
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO schema_migrations (version, checksum) VALUES ($1, $2)
	`, version, checksum); err != nil {
		return fmt.Errorf("record migration %s: %w", version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", version, err)
	}
	tx = nil
	return nil
}

// MigrateDB 是新的迁移入口，替代旧的 Migrate 函数。
//
// 流程：
//  1. 确保 schema_migrations 追踪表存在
//  2. 自动发现所有 *.up.sql 迁移文件
//  3. 查询已应用的迁移
//  4. 对每个迁移：
//     - 已应用 → 校验 checksum（防止篡改）
//     - 未应用 → 在事务中执行 + 记录
func MigrateDB(ctx context.Context, db *DB, fs embed.FS) error {
	// Serialize concurrent migrators on a session-level advisory lock held by
	// one pinned connection for the whole run. The lock is taken BEFORE the
	// applied-migrations snapshot so a second migrator waits, then re-reads a
	// complete schema and skips everything. Running the migrations on the same
	// pinned connection means a pool of size 1 cannot deadlock (the lock
	// holder never needs a second connection).
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationAdvisoryLockKey); err != nil {
		return fmt.Errorf("acquire migration advisory lock: %w", err)
	}
	defer func() {
		// Unlock on the live session before the connection returns to the
		// pool; a fresh context so a cancelled caller ctx cannot strand the
		// lock (session end would release it anyway).
		unlockCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", migrationAdvisoryLockKey); err != nil {
			slog.Warn("release migration advisory lock failed", "error", err)
		}
	}()

	// 1. 确保追踪表存在
	if err := ensureMigrationsTable(ctx, conn); err != nil {
		return err
	}
	slog.Info("schema_migrations table ready")

	// 2. 自动发现迁移文件
	versions, err := discoverMigrations(fs)
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		return fmt.Errorf("no migration files found in embed.FS")
	}
	slog.Info("discovered migrations", "count", len(versions))

	// 3. 查询已应用的迁移
	applied, err := getAppliedMigrations(ctx, conn)
	if err != nil {
		return err
	}
	slog.Info("already applied migrations", "count", len(applied))

	// 4. 逐个处理
	pendingCount := 0
	skippedCount := 0
	for _, version := range versions {
		sqlBytes, err := fs.ReadFile("migrations/" + version + ".up.sql")
		if err != nil {
			return fmt.Errorf("read migration file %s: %w", version, err)
		}
		sqlContent := string(sqlBytes)
		checksum := computeChecksum(sqlContent)

		if existing, ok := applied[version]; ok {
			// 已应用 — 校验 checksum
			if err := verifyMigrationChecksum(version, existing.Checksum, checksum); err != nil {
				return err
			}
			skippedCount++
			continue
		}

		// 未应用 — 执行
		start := time.Now()
		if err := runMigration(ctx, conn, version, sqlContent, checksum); err != nil {
			return fmt.Errorf("migration %s failed: %w", version, err)
		}
		duration := time.Since(start)
		slog.Info("migration applied",
			"version", version,
			"checksum", checksum[:12]+"...",
			"duration_ms", duration.Milliseconds())
		pendingCount++
	}

	slog.Info("migrations completed",
		"total", len(versions),
		"applied", pendingCount,
		"skipped", skippedCount)
	return nil
}
