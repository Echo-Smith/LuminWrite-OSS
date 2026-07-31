package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// ─── GraphRAG: Entity Extraction + Relation Graph ──────
// This module implements GraphRAG functionality that was previously
// provided by WeKnora's graph extraction pipeline.
//
// It uses LLM to extract entities and relationships from text,
// stores them in kb_entities and kb_relations tables, and provides
// 2-hop graph traversal for knowledge-augmented retrieval.
//
// The entity extraction prompt is based on WeKnora's config.yaml
// extract_graph.description, adapted for Chinese content.

// GraphEntity represents a named entity extracted from a document.
type GraphEntity struct {
	ID         string                 `json:"id"`
	DocID      string                 `json:"doc_id"`
	ChunkID    string                 `json:"chunk_id,omitempty"`
	EntityName string                 `json:"entity_name"`
	EntityType string                 `json:"entity_type"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
}

// GraphRelation represents a relationship between two entities.
type GraphRelation struct {
	ID           string                 `json:"id"`
	DocID        string                 `json:"doc_id"`
	SourceEntity string                 `json:"source_entity"`
	TargetEntity string                 `json:"target_entity"`
	RelationType string                 `json:"relation_type"`
	Attributes   map[string]interface{} `json:"attributes,omitempty"`
}

// GraphRAGManager manages entity extraction and graph traversal.
type GraphRAGManager struct {
	db        *sql.DB
	embedding *tools.EmbeddingClient
	llm       *tools.LLMClient
}

// NewGraphRAGManager creates a new GraphRAG manager.
func NewGraphRAGManager(db *sql.DB, embedding *tools.EmbeddingClient, llm *tools.LLMClient) *GraphRAGManager {
	return &GraphRAGManager{
		db:        db,
		embedding: embedding,
		llm:       llm,
	}
}

// IsConfigured returns true if the required clients are available.
func (g *GraphRAGManager) IsConfigured() bool {
	return g != nil && g.db != nil && g.llm != nil
}

// ExtractAndStore extracts entities and relations from a chunk
// and stores them in the database.
func (g *GraphRAGManager) ExtractAndStore(ctx context.Context, docID, chunkID, userID, content string) error {
	if !g.IsConfigured() {
		return fmt.Errorf("GraphRAG not configured")
	}

	// Use LLM to extract entities and relations
	entities, relations, err := g.extractWithLLM(ctx, content)
	if err != nil {
		slog.Warn("GraphRAG extraction failed", "doc_id", docID, "error", err)
		return err
	}

	if len(entities) == 0 {
		return nil
	}

	// Store entities and build name→ID map for relation linking
	entityMap := make(map[string]string) // entityName → entityID

	for _, entity := range entities {
		entity.DocID = docID
		entity.ChunkID = chunkID

		// Generate embedding for entity name (for entity linking)
		var embeddingVec string
		if g.embedding != nil && g.embedding.IsConfigured() {
			vec, _, err := g.embedding.EmbedSingle(ctx, entity.EntityName)
			if err == nil {
				embeddingVec = tools.FormatVectorForPG(vec)
			}
		}

		attrsJSON, _ := json.Marshal(entity.Attributes)
		var entityID string

		// Handle embedding insertion conditionally — empty string cast to vector fails.
		if embeddingVec != "" {
			err := g.db.QueryRowContext(ctx, `
				INSERT INTO kb_entities (doc_id, chunk_id, user_id, entity_name, entity_type, attributes, embedding)
				VALUES ($1, $2, $3, $4, $5, $6, $7::vector)
				RETURNING id::text
			`, docID, chunkID, nullIfEmpty(userID), entity.EntityName, entity.EntityType, string(attrsJSON), embeddingVec).Scan(&entityID)
			if err != nil {
				slog.Debug("failed to insert entity with embedding", "name", entity.EntityName, "error", err)
				continue
			}
		} else {
			err := g.db.QueryRowContext(ctx, `
				INSERT INTO kb_entities (doc_id, chunk_id, user_id, entity_name, entity_type, attributes)
				VALUES ($1, $2, $3, $4, $5, $6)
				RETURNING id::text
			`, docID, chunkID, nullIfEmpty(userID), entity.EntityName, entity.EntityType, string(attrsJSON)).Scan(&entityID)
			if err != nil {
				slog.Debug("failed to insert entity without embedding", "name", entity.EntityName, "error", err)
				continue
			}
		}
		entityMap[entity.EntityName] = entityID
	}

	// Store relations
	for _, rel := range relations {
		rel.DocID = docID

		sourceID, ok1 := entityMap[rel.SourceEntity]
		targetID, ok2 := entityMap[rel.TargetEntity]
		if !ok1 || !ok2 {
			continue
		}

		attrsJSON, _ := json.Marshal(rel.Attributes)
		_, err := g.db.ExecContext(ctx, `
			INSERT INTO kb_relations (doc_id, source_entity_id, target_entity_id, relation_type, attributes)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT DO NOTHING
		`, docID, sourceID, targetID, rel.RelationType, string(attrsJSON))
		if err != nil {
			slog.Debug("failed to insert relation", "type", rel.RelationType, "error", err)
		}
	}

	slog.Info("GraphRAG extraction completed",
		"doc_id", docID, "entities", len(entities), "relations", len(relations))
	return nil
}

// extractWithLLM uses the LLM to extract entities and relations from text.
func (g *GraphRAGManager) extractWithLLM(ctx context.Context, content string) ([]GraphEntity, []GraphRelation, error) {
	// Truncate to reasonable length for LLM
	runes := []rune(content)
	if len(runes) > 3000 {
		content = string(runes[:3000])
	}

	prompt := fmt.Sprintf(`你是一个信息抽取助手。请从以下文本中提取核心实体和关系。

## 提取规则

### 实体类型（只提取以下类型）：
- person（人物）
- organization（组织/机构）
- location（地点/位置）
- event（事件）
- concept（概念/政策/法律/技术术语）
- product（产品/项目）

### 关系类型（只提取以下类型）：
- Author（作者/创造者）
- Alias（别名/别称）
- Member_of（属于/成员）
- Located_in（位于）
- Participated_in（参与）
- Created（创建/发起）
- Related_to（相关）
- Caused（导致/引发）
- Target_of（对象）

### 输出格式
只输出 JSON，不要输出任何解释：
{
  "entities": [
    {"name": "实体名", "type": "person", "attributes": ["属性1", "属性2"]}
  ],
  "relations": [
    {"source": "源实体名", "target": "目标实体名", "type": "Author", "attributes": ["关系属性"]}
  ]
}

## 文本内容
%s`, content)

	content, _, err := g.llm.Chat(ctx, []tools.LLMMessage{
		{Role: "system", Content: "你是中文信息抽取专家。只输出纯JSON，不输出markdown代码块。"},
		{Role: "user", Content: prompt},
	}, tools.WithTemperature(0.1), tools.WithJSONResponse())
	if err != nil {
		return nil, nil, fmt.Errorf("LLM extraction failed: %w", err)
	}

	// Parse JSON response
	var result struct {
		Entities  []struct {
			Name       string   `json:"name"`
			Type       string   `json:"type"`
			Attributes []string `json:"attributes"`
		} `json:"entities"`
		Relations []struct {
			Source     string   `json:"source"`
			Target     string   `json:"target"`
			Type       string   `json:"type"`
			Attributes []string `json:"attributes"`
		} `json:"relations"`
	}

	// Clean markdown code blocks if present
	cleaned := strings.TrimSpace(content)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return nil, nil, fmt.Errorf("failed to parse extraction result: %w", err)
	}

	// Convert to domain types
	entities := make([]GraphEntity, 0, len(result.Entities))
	for _, e := range result.Entities {
		attrs := make(map[string]interface{})
		for i, a := range e.Attributes {
			attrs[fmt.Sprintf("attr_%d", i)] = a
		}
		entities = append(entities, GraphEntity{
			EntityName: e.Name,
			EntityType: e.Type,
			Attributes: attrs,
		})
	}

	relations := make([]GraphRelation, 0, len(result.Relations))
	for _, r := range result.Relations {
		attrs := make(map[string]interface{})
		for i, a := range r.Attributes {
			attrs[fmt.Sprintf("attr_%d", i)] = a
		}
		relations = append(relations, GraphRelation{
			SourceEntity: r.Source,
			TargetEntity: r.Target,
			RelationType: r.Type,
			Attributes:   attrs,
		})
	}

	return entities, relations, nil
}

// GraphSearch performs 2-hop graph traversal from query entities.
// It finds entities that match the query, then traverses their relations
// to find related entities and documents.
func (g *GraphRAGManager) GraphSearch(ctx context.Context, userID, query string, limit int) ([]*KbSearchResult, error) {
	if !g.IsConfigured() {
		return []*KbSearchResult{}, nil
	}
	if limit <= 0 {
		limit = 5
	}

	// Step 1: Find entities matching the query via embedding similarity
	entityIDs, err := g.findEntities(ctx, userID, query, limit*2)
	if err != nil || len(entityIDs) == 0 {
		return []*KbSearchResult{}, nil
	}

	// Step 2: 2-hop traversal — find related entities and their documents
	docScores := make(map[string]float64) // docID → score

	// 1-hop: direct relations from matched entities
	hop1Docs, err := g.traverseHop(ctx, entityIDs, 1)
	if err == nil {
		for docID, score := range hop1Docs {
			docScores[docID] += score
		}
	}

	// 2-hop: relations of relations
	hop2Docs, err := g.traverseHop(ctx, entityIDs, 2)
	if err == nil {
		for docID, score := range hop2Docs {
			docScores[docID] += score * 0.5 // Decay for 2-hop
		}
	}

	// Convert to results
	results := make([]*KbSearchResult, 0, len(docScores))
	for docID, score := range docScores {
		// Get document title and content preview
		var title, content, source string
		err := g.db.QueryRowContext(ctx, `
			SELECT title, LEFT(content, 500), COALESCE(source, '')
			FROM knowledge_base
			WHERE id = $1 AND status != 'deleted'
		`, docID).Scan(&title, &content, &source)
		if err != nil {
			continue
		}
		results = append(results, &KbSearchResult{
			DocID:  docID,
			Title:  title,
			Content: content,
			Score:  score,
			Source: source,
			UserID: userID,
		})
	}

	// Sort by score and limit
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// findEntities finds entities that match the query via embedding similarity.
func (g *GraphRAGManager) findEntities(ctx context.Context, userID, query string, limit int) ([]string, error) {
	if g.embedding == nil || !g.embedding.IsConfigured() {
		return nil, fmt.Errorf("embedding not configured")
	}

	queryVec, _, err := g.embedding.EmbedSingle(ctx, query)
	if err != nil {
		return nil, err
	}
	vecStr := tools.FormatVectorForPG(queryVec)

	rows, err := g.db.QueryContext(ctx, `
		SELECT id::text FROM kb_entities
		WHERE embedding IS NOT NULL
		  AND (user_id = $1 OR user_id IS NULL)
		ORDER BY embedding <=> $2::vector
		LIMIT $3
	`, userID, vecStr, limit)
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

// traverseHop performs N-hop graph traversal from the given entity IDs.
// Returns a map of docID → score.
func (g *GraphRAGManager) traverseHop(ctx context.Context, entityIDs []string, hops int) (map[string]float64, error) {
	if len(entityIDs) == 0 {
		return map[string]float64{}, nil
	}

	docScores := make(map[string]float64)
	currentIDs := entityIDs
	visited := make(map[string]bool)
	for _, id := range entityIDs {
		visited[id] = true
	}

	for hop := 0; hop < hops; hop++ {
		nextIDs := make([]string, 0)
		decay := 1.0 / float64(hop+1)

		for _, entityID := range currentIDs {
			// Find relations where this entity is source or target
			rows, err := g.db.QueryContext(ctx, `
				SELECT
					CASE WHEN source_entity_id = $1 THEN target_entity_id ELSE source_entity_id END as related_id,
					COALESCE(doc_id::text, '') as doc_id
				FROM kb_relations
				WHERE source_entity_id = $1 OR target_entity_id = $1
			`, entityID)
			if err != nil {
				continue
			}

			for rows.Next() {
				var relatedID string
				var docID string
				if err := rows.Scan(&relatedID, &docID); err != nil {
					continue
				}

				if docID != "" {
					docScores[docID] += decay
				}

				if !visited[relatedID] {
					visited[relatedID] = true
					nextIDs = append(nextIDs, relatedID)
				}
			}
			rows.Close()
		}

		currentIDs = nextIDs
		if len(currentIDs) == 0 {
			break
		}
	}

	return docScores, nil
}

// ─── Document-level Entity/Relation queries ────────────

// GetDocEntities returns all entities extracted from a given document.
func (g *GraphRAGManager) GetDocEntities(ctx context.Context, docID string) ([]GraphEntity, error) {
	if g.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	rows, err := g.db.QueryContext(ctx, `
		SELECT id::text, doc_id::text, COALESCE(chunk_id::text, ''), entity_name, entity_type, COALESCE(attributes, '{}')
		FROM kb_entities
		WHERE doc_id = $1
		ORDER BY entity_name ASC
	`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entities []GraphEntity
	for rows.Next() {
		var e GraphEntity
		var attrsJSON []byte
		if err := rows.Scan(&e.ID, &e.DocID, &e.ChunkID, &e.EntityName, &e.EntityType, &attrsJSON); err != nil {
			continue
		}
		if len(attrsJSON) > 0 {
			json.Unmarshal(attrsJSON, &e.Attributes)
		}
		entities = append(entities, e)
	}

	return entities, nil
}

// GetDocRelations returns all relations for a given document.
func (g *GraphRAGManager) GetDocRelations(ctx context.Context, docID string) ([]map[string]interface{}, error) {
	if g.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	rows, err := g.db.QueryContext(ctx, `
		SELECT r.id::text, r.doc_id::text,
		       e1.entity_name AS source_entity,
		       e2.entity_name AS target_entity,
		       r.relation_type,
		       COALESCE(r.attributes, '{}')
		FROM kb_relations r
		JOIN kb_entities e1 ON r.source_entity_id = e1.id
		JOIN kb_entities e2 ON r.target_entity_id = e2.id
		WHERE r.doc_id = $1
		ORDER BY r.relation_type ASC
	`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var relations []map[string]interface{}
	for rows.Next() {
		var id, docIDStr, source, target, relType string
		var attrsJSON []byte
		if err := rows.Scan(&id, &docIDStr, &source, &target, &relType, &attrsJSON); err != nil {
			continue
		}
		attrs := map[string]interface{}{}
		if len(attrsJSON) > 0 {
			json.Unmarshal(attrsJSON, &attrs)
		}
		relations = append(relations, map[string]interface{}{
			"id":             id,
			"doc_id":         docIDStr,
			"source_entity":  source,
			"target_entity":  target,
			"relation_type":  relType,
			"attributes":     attrs,
		})
	}

	return relations, nil
}

// ─── Global Graph queries ───────────────────────────────

// GraphNode is a node in the entity graph (for visualization).
type GraphNode struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	DocCount   int    `json:"doc_count"`
}

// GraphEdge is an edge in the entity graph.
type GraphEdge struct {
	Source      string `json:"source"`
	Target      string `json:"target"`
	RelType     string `json:"rel_type"`
}

// GetGlobalGraph returns the top entities and their relations for visualization.
// limit: max number of entities to return (sorted by connection count).
func (g *GraphRAGManager) GetGlobalGraph(ctx context.Context, limit int) ([]GraphNode, []GraphEdge, error) {
	if g.db == nil {
		return nil, nil, fmt.Errorf("database not available")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	// Get top entities by connection count
	rows, err := g.db.QueryContext(ctx, `
		SELECT e.id::text, e.entity_name, e.entity_type,
		       COUNT(DISTINCT r.id) AS conn_count,
		       COUNT(DISTINCT e.doc_id) AS doc_count
		FROM kb_entities e
		LEFT JOIN kb_relations r ON r.source_entity_id = e.id OR r.target_entity_id = e.id
		GROUP BY e.id, e.entity_name, e.entity_type
		ORDER BY conn_count DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var nodes []GraphNode
	nodeIDs := make(map[string]bool)
	for rows.Next() {
		var n GraphNode
		var connCount int
		if err := rows.Scan(&n.ID, &n.Name, &n.Type, &connCount, &n.DocCount); err != nil {
			continue
		}
		// Skip isolated nodes (no connections) for a cleaner graph
		if connCount == 0 {
			continue
		}
		nodes = append(nodes, n)
		nodeIDs[n.ID] = true
	}

	if len(nodes) == 0 {
		return nodes, []GraphEdge{}, nil
	}

	// Get relations between the top entities
	// Build a parameter list for the IN clause
	args := make([]interface{}, len(nodes))
	placeholders := ""
	for i, n := range nodes {
		if i > 0 {
			placeholders += ","
		}
		placeholders += fmt.Sprintf("$%d", i+1)
		args[i] = n.ID
	}

	edgeRows, err := g.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT DISTINCT
			r.source_entity_id::text,
			r.target_entity_id::text,
			r.relation_type
		FROM kb_relations r
		WHERE r.source_entity_id IN (%s)
		  AND r.target_entity_id IN (%s)
	`, placeholders, placeholders), args...)
	if err != nil {
		return nodes, nil, err
	}
	defer edgeRows.Close()

	var edges []GraphEdge
	for edgeRows.Next() {
		var e GraphEdge
		if err := edgeRows.Scan(&e.Source, &e.Target, &e.RelType); err != nil {
			continue
		}
		edges = append(edges, e)
	}

	return nodes, edges, nil
}
