package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/luminbuddy/writing-agent-v2/internal/database"
	"github.com/luminbuddy/writing-agent-v2/pkg/memory"
)

// PgStore 是 memory.Store 的 PostgreSQL 实现
type PgStore struct {
	db *database.DB
}

// NewPgStore 创建 PostgreSQL 存储
func NewPgStore(db *database.DB) *PgStore {
	return &PgStore{db: db}
}

// Save 保存或更新一条记忆
func (s *PgStore) Save(ctx context.Context, m *memory.Memory) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}

	// 将 embedding 转为 pgvector 格式字符串
	embeddingStr := vectorToPgFormat(m.Embedding)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO user_memories (
			id, user_id, tier, category, key, value, embedding,
			confidence, occurrences, source_trace_id,
			quality_source, quality_weight, status, superseded_by,
			first_seen, last_seen, created_at, updated_at
		) VALUES (
			COALESCE(NULLIF($1, '')::uuid, uuid_generate_v4()),
			$2::uuid, $3, $4, $5, $6, $7,
			$8, $9, $10,
			$11, $12, $13, NULLIF($14, '')::uuid,
			COALESCE($15, NOW()), COALESCE($16, NOW()),
			COALESCE($17, NOW()), NOW()
		)
		ON CONFLICT (user_id, category, key, status) DO UPDATE SET
			value = EXCLUDED.value,
			embedding = COALESCE(EXCLUDED.embedding, user_memories.embedding),
			confidence = EXCLUDED.confidence,
			occurrences = EXCLUDED.occurrences,
			quality_source = EXCLUDED.quality_source,
			quality_weight = EXCLUDED.quality_weight,
			status = EXCLUDED.status,
			last_seen = NOW(),
			updated_at = NOW()
		RETURNING id::text
	`,
		m.ID, m.UserID, m.Tier, m.Category, m.Key, m.Value, embeddingStr,
		m.Confidence, m.Occurrences, m.SourceTraceID,
		m.QualitySource, m.QualityWeight, m.Status, m.SupersededBy,
		m.FirstSeen, m.LastSeen, m.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save memory: %w", err)
	}

	return nil
}

// Get 按 ID 获取记忆
func (s *PgStore) Get(ctx context.Context, id string) (*memory.Memory, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var m memory.Memory
	err := s.db.QueryRowContext(ctx, `
		SELECT id::text, user_id::text, tier, category, key, value,
		       confidence, occurrences, source_trace_id,
		       quality_source, quality_weight, status,
		       COALESCE(superseded_by::text, ''),
		       first_seen, last_seen, created_at, updated_at
		FROM user_memories WHERE id = $1::uuid
	`, id).Scan(
		&m.ID, &m.UserID, &m.Tier, &m.Category, &m.Key, &m.Value,
		&m.Confidence, &m.Occurrences, &m.SourceTraceID,
		&m.QualitySource, &m.QualityWeight, &m.Status,
		&m.SupersededBy,
		&m.FirstSeen, &m.LastSeen, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// List 列出用户记忆
func (s *PgStore) List(ctx context.Context, userID string, opts memory.ListOptions) ([]*memory.Memory, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	query := `
		SELECT id::text, user_id::text, tier, category, key, value,
		       confidence, occurrences, source_trace_id,
		       quality_source, quality_weight, status,
		       COALESCE(superseded_by::text, ''),
		       first_seen, last_seen, created_at, updated_at
		FROM user_memories
		WHERE user_id = $1::uuid
	`
	args := []interface{}{userID}
	argIdx := 2

	if opts.Tier != nil {
		query += fmt.Sprintf(" AND tier = $%d", argIdx)
		args = append(args, *opts.Tier)
		argIdx++
	}
	if opts.Status != nil {
		query += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, *opts.Status)
		argIdx++
	}

	query += " ORDER BY updated_at DESC"

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	} else {
		query += " LIMIT 50"
	}
	if opts.Offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", opts.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []*memory.Memory
	for rows.Next() {
		var m memory.Memory
		if err := rows.Scan(
			&m.ID, &m.UserID, &m.Tier, &m.Category, &m.Key, &m.Value,
			&m.Confidence, &m.Occurrences, &m.SourceTraceID,
			&m.QualitySource, &m.QualityWeight, &m.Status,
			&m.SupersededBy,
			&m.FirstSeen, &m.LastSeen, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			continue
		}
		memories = append(memories, &m)
	}
	return memories, nil
}

// Search 语义检索用户活跃记忆
func (s *PgStore) Search(ctx context.Context, userID string, queryVector []float32, limit int) ([]*memory.Memory, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	if limit <= 0 {
		limit = 20
	}

	vecStr := vectorToPgFormat(queryVector)

	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, user_id::text, tier, category, key, value,
		       confidence, occurrences, source_trace_id,
		       quality_source, quality_weight, status,
		       COALESCE(superseded_by::text, ''),
		       first_seen, last_seen, created_at, updated_at,
		       1 - (embedding <=> $2::vector) AS similarity
		FROM user_memories
		WHERE user_id = $1::uuid
		  AND status IN ('active', 'candidate')
		  AND embedding IS NOT NULL
		ORDER BY embedding <=> $2::vector
		LIMIT $3
	`, userID, vecStr, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []*memory.Memory
	for rows.Next() {
		var m memory.Memory
		var similarity float64
		if err := rows.Scan(
			&m.ID, &m.UserID, &m.Tier, &m.Category, &m.Key, &m.Value,
			&m.Confidence, &m.Occurrences, &m.SourceTraceID,
			&m.QualitySource, &m.QualityWeight, &m.Status,
			&m.SupersededBy,
			&m.FirstSeen, &m.LastSeen, &m.CreatedAt, &m.UpdatedAt,
			&similarity,
		); err != nil {
			continue
		}
		memories = append(memories, &m)
	}
	return memories, nil
}

// FindByCategoryKey 按 category+key 查找
func (s *PgStore) FindByCategoryKey(ctx context.Context, userID, category, key string) ([]*memory.Memory, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, user_id::text, tier, category, key, value,
		       confidence, occurrences, source_trace_id,
		       quality_source, quality_weight, status,
		       COALESCE(superseded_by::text, ''),
		       first_seen, last_seen, created_at, updated_at
		FROM user_memories
		WHERE user_id = $1::uuid AND category = $2 AND key = $3
		  AND status IN ('active', 'candidate')
	`, userID, category, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []*memory.Memory
	for rows.Next() {
		var m memory.Memory
		if err := rows.Scan(
			&m.ID, &m.UserID, &m.Tier, &m.Category, &m.Key, &m.Value,
			&m.Confidence, &m.Occurrences, &m.SourceTraceID,
			&m.QualitySource, &m.QualityWeight, &m.Status,
			&m.SupersededBy,
			&m.FirstSeen, &m.LastSeen, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			continue
		}
		memories = append(memories, &m)
	}
	return memories, nil
}

// UpdateStatus 更新记忆状态
func (s *PgStore) UpdateStatus(ctx context.Context, id string, status memory.MemoryStatus) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE user_memories SET status = $2, updated_at = NOW() WHERE id = $1::uuid`,
		id, status)
	return err
}

// IncrementOccurrence 增加出现次数并更新 last_seen
func (s *PgStore) IncrementOccurrence(ctx context.Context, id string) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE user_memories SET occurrences = occurrences + 1, last_seen = NOW(), updated_at = NOW() WHERE id = $1::uuid`,
		id)
	return err
}

// Supersede 将旧记忆标记为 superseded
func (s *PgStore) Supersede(ctx context.Context, oldID, newID string) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}
	if newID != "" {
		_, err := s.db.ExecContext(ctx,
			`UPDATE user_memories SET status = 'superseded', superseded_by = $2::uuid, updated_at = NOW() WHERE id = $1::uuid`,
			oldID, newID)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE user_memories SET status = 'superseded', updated_at = NOW() WHERE id = $1::uuid`,
		oldID)
	return err
}

// Delete 删除记忆
func (s *PgStore) Delete(ctx context.Context, id string) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM user_memories WHERE id = $1::uuid`, id)
	return err
}

// DismissForSession 记录会话级关闭
func (s *PgStore) DismissForSession(ctx context.Context, memoryID, sessionID string) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memory_session_dismissals (memory_id, session_id)
		VALUES ($1::uuid, $2)
		ON CONFLICT (memory_id, session_id) DO NOTHING
	`, memoryID, sessionID)
	return err
}

// GetDismissals 获取会话中已关闭的记忆 ID
func (s *PgStore) GetDismissals(ctx context.Context, sessionID string) ([]string, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT memory_id::text FROM memory_session_dismissals WHERE session_id = $1`,
		sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// vectorToPgFormat 将 []float32 转为 pgvector 格式字符串
func vectorToPgFormat(vec []float32) interface{} {
	if len(vec) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteByte('[')
	for i, v := range vec {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(fmt.Sprintf("%g", v))
	}
	sb.WriteByte(']')
	return sb.String()
}
