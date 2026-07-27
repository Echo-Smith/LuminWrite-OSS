package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// ─── 短期记忆 PostgreSQL Store ──────────────────────────────

// PgShortTermStore 是 memory.ShortTermStore 的 PostgreSQL 实现
type PgShortTermStore struct {
	db *database.DB
}

// NewPgShortTermStore 创建 PostgreSQL 短期记忆存储
func NewPgShortTermStore(db *database.DB) *PgShortTermStore {
	return &PgShortTermStore{db: db}
}

// StoreMessage 存储一条对话消息，并回写数据库生成的 ID
func (s *PgShortTermStore) StoreMessage(ctx context.Context, msg *memory.ConversationMessage) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}

	embeddingStr := vectorToPgFormat(msg.Embedding)

	var userIDArg interface{}
	if msg.UserID != "" && msg.UserID != "anonymous" {
		userIDArg = msg.UserID
	}

	var returnedID string
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO conversation_messages (
			id, conversation_id, user_id, trace_id,
			role, content, content_type, intent,
			embedding, token_count, metadata, created_at
		) VALUES (
			COALESCE(NULLIF($1, '')::uuid, uuid_generate_v4()),
			$2, $3::uuid, $4,
			$5, $6, $7, $8,
			$9, $10, $11, COALESCE($12, NOW())
		)
		RETURNING id::text
	`,
		msg.ID, msg.ConversationID, userIDArg, msg.TraceID,
		msg.Role, msg.Content, msg.ContentType, msg.Intent,
		embeddingStr, msg.TokenCount, "{}", msg.CreatedAt,
	).Scan(&returnedID)
	if err != nil {
		return fmt.Errorf("failed to store conversation message: %w", err)
	}
	// 回写数据库生成的 ID，供后续异步 embedding 更新使用
	if msg.ID == "" {
		msg.ID = returnedID
	}
	return nil
}

// LoadHistory 加载会话的对话历史（按时间正序），包含 embedding 向量
func (s *PgShortTermStore) LoadHistory(ctx context.Context, conversationID string, limit int) ([]memory.ConversationMessage, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, conversation_id, COALESCE(user_id::text, ''), trace_id,
		       role, content, content_type, intent,
		       COALESCE(embedding::text, ''), token_count, created_at
		FROM conversation_messages
		WHERE conversation_id = $1
		ORDER BY created_at ASC
		LIMIT $2
	`, conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []memory.ConversationMessage
	for rows.Next() {
		var m memory.ConversationMessage
		var embeddingStr string
		if err := rows.Scan(
			&m.ID, &m.ConversationID, &m.UserID, &m.TraceID,
			&m.Role, &m.Content, &m.ContentType, &m.Intent,
			&embeddingStr, &m.TokenCount, &m.CreatedAt,
		); err != nil {
			continue
		}
		m.Embedding = pgVectorToFloat32(embeddingStr)
		messages = append(messages, m)
	}
	return messages, nil
}

// GenerateEmbedding 为消息生成 embedding
func (s *PgShortTermStore) GenerateEmbedding(ctx context.Context, messageID string) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}
	// embedding 由外部 embedder 生成，这里只负责持久化
	// 实际调用流程：service 层获取消息 → embedder.Embed → UPDATE 回写
	return nil
}

// UpdateEmbedding 更新消息的 embedding
func (s *PgShortTermStore) UpdateEmbedding(ctx context.Context, messageID string, embedding []float32) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}
	embeddingStr := vectorToPgFormat(embedding)
	_, err := s.db.ExecContext(ctx, `
		UPDATE conversation_messages SET embedding = $2 WHERE id = $1::uuid
	`, messageID, embeddingStr)
	return err
}

// ─── 工作记忆持久化 ──────────────────────────────────────────

// SaveWorkingSummary 持久化工作记忆摘要（按 conversation_id upsert）
func (s *PgShortTermStore) SaveWorkingSummary(ctx context.Context, ws *memory.WorkingSummary) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}
	if ws == nil || ws.ConversationID == "" {
		return nil
	}

	summaryJSON, err := json.Marshal(ws)
	if err != nil {
		return fmt.Errorf("failed to marshal working summary: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO working_summaries (conversation_id, trace_id, summary, last_updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (conversation_id) DO UPDATE SET
			trace_id = EXCLUDED.trace_id,
			summary = EXCLUDED.summary,
			last_updated_at = NOW()
	`, ws.ConversationID, ws.TraceID, summaryJSON)
	if err != nil {
		return fmt.Errorf("failed to save working summary: %w", err)
	}

	slog.Debug("working summary: saved",
		"conversation_id", ws.ConversationID,
		"trace_id", ws.TraceID,
		"step_summaries", len(ws.StepSummaries),
	)
	return nil
}

// LoadWorkingSummary 加载会话最近的工作记忆摘要
func (s *PgShortTermStore) LoadWorkingSummary(ctx context.Context, conversationID string) (*memory.WorkingSummary, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	if conversationID == "" {
		return nil, nil
	}

	var summaryJSON []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT summary FROM working_summaries WHERE conversation_id = $1
	`, conversationID).Scan(&summaryJSON)
	if err != nil {
		// 没有历史摘要是正常情况，返回 nil
		return nil, nil
	}

	var ws memory.WorkingSummary
	if err := json.Unmarshal(summaryJSON, &ws); err != nil {
		return nil, fmt.Errorf("failed to unmarshal working summary: %w", err)
	}

	// 重置 SummarizedSteps（json:"-" 不会被序列化）
	ws.SummarizedSteps = make(map[string]bool)
	for _, ss := range ws.StepSummaries {
		ws.SummarizedSteps[ss.Step] = true
	}

	return &ws, nil
}

// ─── 实体记忆网络 PostgreSQL Store ────────────────────────────

// PgEntityStore 是 memory.EntityStore 的 PostgreSQL 实现
type PgEntityStore struct {
	db *database.DB
}

// NewPgEntityStore 创建 PostgreSQL 实体存储
func NewPgEntityStore(db *database.DB) *PgEntityStore {
	return &PgEntityStore{db: db}
}

// StoreEntity 存储或更新实体，并回写数据库生成的 ID
func (s *PgEntityStore) StoreEntity(ctx context.Context, entity *memory.Entity) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}

	embeddingStr := vectorToPgFormat(entity.Embedding)

	var returnedID string
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO memory_entities (
			id, user_id, entity_type, name, description, embedding,
			confidence, occurrence_count, source_trace_id,
			status, superseded_by, first_seen, last_seen
		) VALUES (
			COALESCE(NULLIF($1, '')::uuid, uuid_generate_v4()),
			$2::uuid, $3, $4, $5, $6,
			$7, $8, $9,
			$10, NULLIF($11, '')::uuid,
			COALESCE($12, NOW()), COALESCE($13, NOW())
		)
		ON CONFLICT (user_id, entity_type, name, status) DO UPDATE SET
			description = EXCLUDED.description,
			embedding = COALESCE(EXCLUDED.embedding, memory_entities.embedding),
			confidence = EXCLUDED.confidence,
			occurrence_count = EXCLUDED.occurrence_count,
			last_seen = NOW(),
			updated_at = NOW()
		RETURNING id::text
	`,
		entity.ID, entity.UserID, entity.EntityType, entity.Name, entity.Description, embeddingStr,
		entity.Confidence, entity.OccurrenceCount, entity.SourceTraceID,
		entity.Status, entity.SupersededBy,
		entity.FirstSeen, entity.LastSeen,
	).Scan(&returnedID)
	if err != nil {
		return fmt.Errorf("failed to store entity: %w", err)
	}
	// 回写数据库生成的 ID（INSERT 和 ON CONFLICT UPDATE 都会返回）
	if entity.ID == "" {
		entity.ID = returnedID
	}
	return nil
}

// FindEntity 按 user+type+name 查找实体
func (s *PgEntityStore) FindEntity(ctx context.Context, userID string, entityType memory.EntityType, name string) (*memory.Entity, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var e memory.Entity
	err := s.db.QueryRowContext(ctx, `
		SELECT id::text, user_id::text, entity_type, name, description,
		       confidence, occurrence_count, source_trace_id,
		       status, COALESCE(superseded_by::text, ''),
		       first_seen, last_seen, created_at, updated_at
		FROM memory_entities
		WHERE user_id = $1::uuid AND entity_type = $2 AND name = $3
		  AND status = 'active'
	`, userID, entityType, name).Scan(
		&e.ID, &e.UserID, &e.EntityType, &e.Name, &e.Description,
		&e.Confidence, &e.OccurrenceCount, &e.SourceTraceID,
		&e.Status, &e.SupersededBy,
		&e.FirstSeen, &e.LastSeen, &e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// SearchEntities 语义检索实体
func (s *PgEntityStore) SearchEntities(ctx context.Context, userID string, queryVector []float32, limit int) ([]memory.Entity, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	if limit <= 0 {
		limit = 10
	}

	vecStr := vectorToPgFormat(queryVector)

	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, user_id::text, entity_type, name, description,
		       confidence, occurrence_count, source_trace_id,
		       status, COALESCE(superseded_by::text, ''),
		       first_seen, last_seen, created_at, updated_at,
		       1 - (embedding <=> $2::vector) AS similarity
		FROM memory_entities
		WHERE user_id = $1::uuid AND status = 'active' AND embedding IS NOT NULL
		ORDER BY embedding <=> $2::vector
		LIMIT $3
	`, userID, vecStr, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entities []memory.Entity
	for rows.Next() {
		var e memory.Entity
		var similarity float64
		if err := rows.Scan(
			&e.ID, &e.UserID, &e.EntityType, &e.Name, &e.Description,
			&e.Confidence, &e.OccurrenceCount, &e.SourceTraceID,
			&e.Status, &e.SupersededBy,
			&e.FirstSeen, &e.LastSeen, &e.CreatedAt, &e.UpdatedAt,
			&similarity,
		); err != nil {
			continue
		}
		entities = append(entities, e)
	}
	return entities, nil
}

// GetEntityNeighbors 获取实体的邻居（1跳关系）
func (s *PgEntityStore) GetEntityNeighbors(ctx context.Context, userID, entityID string) ([]memory.Entity, []memory.Relation, error) {
	if s.db == nil {
		return nil, nil, fmt.Errorf("database not available")
	}

	// 查询该实体的所有关系
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, user_id::text, source_entity_id::text, target_entity_id::text,
		       relation_type, weight, evidence_count, source_trace_id,
		       first_seen, last_seen
		FROM memory_relations
		WHERE user_id = $1::uuid
		  AND (source_entity_id = $2::uuid OR target_entity_id = $2::uuid)
	`, userID, entityID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var relations []memory.Relation
	neighborIDs := make(map[string]bool)
	for rows.Next() {
		var r memory.Relation
		if err := rows.Scan(
			&r.ID, &r.UserID, &r.SourceEntityID, &r.TargetEntityID,
			&r.RelationType, &r.Weight, &r.EvidenceCount, &r.SourceTraceID,
			&r.FirstSeen, &r.LastSeen,
		); err != nil {
			continue
		}
		relations = append(relations, r)
		// 收集邻居 ID（排除自身）
		if r.SourceEntityID != entityID {
			neighborIDs[r.SourceEntityID] = true
		}
		if r.TargetEntityID != entityID {
			neighborIDs[r.TargetEntityID] = true
		}
	}

	if len(neighborIDs) == 0 {
		return nil, relations, nil
	}

	// 批量获取邻居实体
	ids := make([]string, 0, len(neighborIDs))
	for id := range neighborIDs {
		ids = append(ids, id)
	}
	entitiesMap, err := s.GetEntitiesByIDs(ctx, ids)
	if err != nil {
		return nil, relations, nil
	}

	var neighbors []memory.Entity
	for _, e := range entitiesMap {
		neighbors = append(neighbors, *e)
	}

	return neighbors, relations, nil
}

// GetNeighborsBatch 批量获取多个实体的邻居（1跳关系），避免 N+1 查询。
// 返回 map[entityID][]Entity 和所有涉及的关系。
// 只需 2 次 DB 查询：1 次批量查关系 + 1 次批量查邻居实体。
func (s *PgEntityStore) GetNeighborsBatch(ctx context.Context, userID string, entityIDs []string) (map[string][]memory.Entity, []memory.Relation, error) {
	if s.db == nil {
		return nil, nil, fmt.Errorf("database not available")
	}
	if len(entityIDs) == 0 {
		return make(map[string][]memory.Entity), nil, nil
	}

	// 1. 批量查询所有涉及的关系 — DB 查询 #1
	// 使用 ANY($2::uuid[]) 替代 IN，避免动态拼接 placeholder
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, user_id::text, source_entity_id::text, target_entity_id::text,
		       relation_type, weight, evidence_count, source_trace_id,
		       first_seen, last_seen
		FROM memory_relations
		WHERE user_id = $1::uuid
		  AND (source_entity_id = ANY($2::uuid[]) OR target_entity_id = ANY($2::uuid[]))
	`, userID, entityIDs)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	queryIDSet := make(map[string]bool)
	for _, id := range entityIDs {
		queryIDSet[id] = true
	}

	var allRelations []memory.Relation
	neighborIDs := make(map[string]bool)          // 所有需要获取的邻居实体 ID
	relationByEntity := make(map[string][]memory.Relation) // 按 entityID 索引的关系

	for rows.Next() {
		var r memory.Relation
		if err := rows.Scan(
			&r.ID, &r.UserID, &r.SourceEntityID, &r.TargetEntityID,
			&r.RelationType, &r.Weight, &r.EvidenceCount, &r.SourceTraceID,
			&r.FirstSeen, &r.LastSeen,
		); err != nil {
			continue
		}
		allRelations = append(allRelations, r)

		// 将关系关联到两端实体
		relationByEntity[r.SourceEntityID] = append(relationByEntity[r.SourceEntityID], r)
		relationByEntity[r.TargetEntityID] = append(relationByEntity[r.TargetEntityID], r)

		// 收集不在查询集中的邻居 ID
		if !queryIDSet[r.SourceEntityID] {
			neighborIDs[r.SourceEntityID] = true
		}
		if !queryIDSet[r.TargetEntityID] {
			neighborIDs[r.TargetEntityID] = true
		}
	}

	// 2. 批量获取所有不认识的邻居实体 — DB 查询 #2
	var neighborEntities map[string]*memory.Entity
	if len(neighborIDs) > 0 {
		ids := make([]string, 0, len(neighborIDs))
		for id := range neighborIDs {
			ids = append(ids, id)
		}
		neighborEntities, err = s.GetEntitiesByIDs(ctx, ids)
		if err != nil {
			slog.Warn("entity store: batch get neighbor entities failed", "error", err)
			neighborEntities = make(map[string]*memory.Entity)
		}
	} else {
		neighborEntities = make(map[string]*memory.Entity)
	}

	// 3. 构建结果 map: entityID → []Entity（邻居列表）
	result := make(map[string][]memory.Entity)
	for _, entityID := range entityIDs {
		rels := relationByEntity[entityID]
		seen := make(map[string]bool)
		for _, rel := range rels {
			// 对每条关系，找出另一端的实体
			var otherID string
			if rel.SourceEntityID == entityID {
				otherID = rel.TargetEntityID
			} else {
				otherID = rel.SourceEntityID
			}
			if seen[otherID] {
				continue
			}
			seen[otherID] = true

			// 从已获取的实体中查找
			if e, ok := neighborEntities[otherID]; ok {
				result[entityID] = append(result[entityID], *e)
			} else if queryIDSet[otherID] {
				// 另一端也在查询集中，可能已被包含在 scoredEntities 中
				// 这里仍然返回它（调用方通过 visited 去重）
				// 需要通过 GetEntitiesByIDs 获取
				// 但为避免额外查询，让调用方自行处理
				continue
			}
		}
	}

	return result, allRelations, nil
}

// StoreRelation 存储或更新关系
func (s *PgEntityStore) StoreRelation(ctx context.Context, relation *memory.Relation) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memory_relations (
			id, user_id, source_entity_id, target_entity_id,
			relation_type, weight, evidence_count, source_trace_id,
			first_seen, last_seen
		) VALUES (
			COALESCE(NULLIF($1, '')::uuid, uuid_generate_v4()),
			$2::uuid, $3::uuid, $4::uuid,
			$5, $6, $7, $8,
			COALESCE($9, NOW()), COALESCE($10, NOW())
		)
		ON CONFLICT (user_id, source_entity_id, target_entity_id, relation_type) DO UPDATE SET
			weight = EXCLUDED.weight,
			evidence_count = EXCLUDED.evidence_count,
			last_seen = NOW()
	`,
		relation.ID, relation.UserID, relation.SourceEntityID, relation.TargetEntityID,
		relation.RelationType, relation.Weight, relation.EvidenceCount, relation.SourceTraceID,
		relation.FirstSeen, relation.LastSeen,
	)
	return err
}

// GetRelations 获取用户的所有关系
func (s *PgEntityStore) GetRelations(ctx context.Context, userID string) ([]memory.Relation, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, user_id::text, source_entity_id::text, target_entity_id::text,
		       relation_type, weight, evidence_count, source_trace_id,
		       first_seen, last_seen
		FROM memory_relations
		WHERE user_id = $1::uuid
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var relations []memory.Relation
	for rows.Next() {
		var r memory.Relation
		if err := rows.Scan(
			&r.ID, &r.UserID, &r.SourceEntityID, &r.TargetEntityID,
			&r.RelationType, &r.Weight, &r.EvidenceCount, &r.SourceTraceID,
			&r.FirstSeen, &r.LastSeen,
		); err != nil {
			continue
		}
		relations = append(relations, r)
	}
	return relations, nil
}

// GetEntitiesByIDs 批量获取实体
func (s *PgEntityStore) GetEntitiesByIDs(ctx context.Context, ids []string) (map[string]*memory.Entity, error) {
	if s.db == nil || len(ids) == 0 {
		return make(map[string]*memory.Entity), nil
	}

	// 构建 IN 查询
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d::uuid", i+1)
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT id::text, user_id::text, entity_type, name, description,
		       confidence, occurrence_count, source_trace_id,
		       status, COALESCE(superseded_by::text, ''),
		       first_seen, last_seen, created_at, updated_at
		FROM memory_entities
		WHERE id IN (%s)
	`, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]*memory.Entity)
	for rows.Next() {
		var e memory.Entity
		if err := rows.Scan(
			&e.ID, &e.UserID, &e.EntityType, &e.Name, &e.Description,
			&e.Confidence, &e.OccurrenceCount, &e.SourceTraceID,
			&e.Status, &e.SupersededBy,
			&e.FirstSeen, &e.LastSeen, &e.CreatedAt, &e.UpdatedAt,
		); err != nil {
			continue
		}
		result[e.ID] = &e
	}
	return result, nil
}

// ─── 辅助函数 ──────────────────────────────────────────────

// vectorToPgFormat 将 float32 切片转为 pgvector 格式字符串
// 复用 pg_store.go 中的同名函数，如果存在则不重复定义
// 这里在 pg_store.go 中已有定义，此处通过包级共享
