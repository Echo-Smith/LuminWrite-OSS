package memory

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// ─── 长期记忆：实体记忆网络 ───────────────────────────────────
//
// 实体记忆网络将用户偏好、话题、风格等建模为图结构：
//  - 节点（Entity）：用户交互过的概念实体（如"科技写作"、"短文风格"）
//  - 边（Relation）：实体间的关系（如 prefers, related_to, co_occurs_with）
//
// 检索时通过图遍历找到与当前查询相关的实体网络，
// 提供比扁平 key-value 更丰富的上下文关联。

// ─── 实体 ──────────────────────────────────────────────────

// EntityType 实体类型
type EntityType string

const (
	EntityTopic       EntityType = "topic"       // 话题（如"人工智能"、"乡村振兴"）
	EntityStyle       EntityType = "style"       // 风格（如"音乐评论"、"时评"）
	EntityPreference  EntityType = "preference"  // 偏好（如"短文"、"幽默语气"）
	EntityConcept     EntityType = "concept"     // 概念（如"结构化论证"、"数据驱动"）
	EntityTone        EntityType = "tone"        // 语气（如"严肃"、"轻松"）
	EntityStructure   EntityType = "structure"   // 结构（如"三段论"、"递进式"）
)

// Entity 记忆实体
type Entity struct {
	ID             string     `json:"id"`
	UserID         string     `json:"user_id"`
	EntityType     EntityType `json:"entity_type"`
	Name           string     `json:"name"`
	Description    string     `json:"description,omitempty"`
	Embedding      []float32  `json:"-"`
	Confidence     float64    `json:"confidence"`
	OccurrenceCount int       `json:"occurrence_count"`
	SourceTraceID  string     `json:"source_trace_id,omitempty"`
	Status         string     `json:"status"` // active | superseded | archived
	SupersededBy   string     `json:"superseded_by,omitempty"`
	FirstSeen      time.Time  `json:"first_seen"`
	LastSeen       time.Time  `json:"last_seen"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// ─── 关系 ──────────────────────────────────────────────────

// RelationType 关系类型
type RelationType string

const (
	RelPrefers      RelationType = "prefers"        // 用户偏好 A 胜过 B
	RelDislikes     RelationType = "dislikes"       // 用户不喜欢
	RelRelatedTo    RelationType = "related_to"     // A 与 B 相关
	RelEvolvedFrom  RelationType = "evolved_from"   // A 由 B 演化而来
	RelCoOccursWith RelationType = "co_occurs_with" // A 与 B 共现
	RelContrastsWith RelationType = "contrasts_with" // A 与 B 对比
)

// Relation 实体间关系
type Relation struct {
	ID             string       `json:"id"`
	UserID         string       `json:"user_id"`
	SourceEntityID string       `json:"source_entity_id"`
	TargetEntityID string       `json:"target_entity_id"`
	RelationType   RelationType `json:"relation_type"`
	Weight         float64      `json:"weight"`
	EvidenceCount  int          `json:"evidence_count"`
	SourceTraceID  string       `json:"source_trace_id,omitempty"`
	FirstSeen      time.Time    `json:"first_seen"`
	LastSeen       time.Time    `json:"last_seen"`
}

// ─── 图检索 ────────────────────────────────────────────────

// EntityGraphQuery 图检索查询
type EntityGraphQuery struct {
	UserID       string    `json:"user_id"`
	QueryText    string    `json:"query_text"`
	QueryVector  []float32 `json:"query_vector,omitempty"`
	MaxHops      int       `json:"max_hops"`      // 图遍历跳数（默认 2）
	MaxEntities  int       `json:"max_entities"`  // 返回的最大实体数
	MinRelevance float64   `json:"min_relevance"` // 最小语义相关性
}

// EntityGraphResult 图检索结果
type EntityGraphResult struct {
	// Entities 检索到的相关实体（按相关性排序）
	Entities []ScoredEntity `json:"entities"`
	// Relations 实体间的关系
	Relations []Relation `json:"relations"`
	// FormattedContext 格式化后的 prompt 文本
	FormattedContext string `json:"formatted_context,omitempty"`
}

// ScoredEntity 带评分的实体
type ScoredEntity struct {
	Entity    Entity  `json:"entity"`
	Score     float64 `json:"score"`      // 综合评分
	HopDistance int   `json:"hop_distance"` // 与查询实体的图距离
}

// EntityStore 实体记忆网络存储接口
type EntityStore interface {
	// StoreEntity 存储或更新实体
	StoreEntity(ctx context.Context, entity *Entity) error

	// FindEntity 按 user+type+name 查找实体
	FindEntity(ctx context.Context, userID string, entityType EntityType, name string) (*Entity, error)

	// SearchEntities 语义检索实体
	SearchEntities(ctx context.Context, userID string, queryVector []float32, limit int) ([]Entity, error)

	// GetEntityNeighbors 获取实体的邻居（1跳关系）
	GetEntityNeighbors(ctx context.Context, userID, entityID string) ([]Entity, []Relation, error)

	// GetNeighborsBatch 批量获取多个实体的邻居（1跳关系），避免 N+1 查询
	GetNeighborsBatch(ctx context.Context, userID string, entityIDs []string) (map[string][]Entity, []Relation, error)

	// StoreRelation 存储或更新关系
	StoreRelation(ctx context.Context, relation *Relation) error

	// GetRelations 获取用户的所有关系
	GetRelations(ctx context.Context, userID string) ([]Relation, error)

	// GetEntitiesByIDs 批量获取实体
	GetEntitiesByIDs(ctx context.Context, ids []string) (map[string]*Entity, error)
}

// EntityGraphRetriever 实体记忆网络检索器
type EntityGraphRetriever struct {
	store EntityStore
}

// NewEntityGraphRetriever 创建实体图检索器
func NewEntityGraphRetriever(store EntityStore) *EntityGraphRetriever {
	return &EntityGraphRetriever{store: store}
}

// Retrieve 图检索：从当前查询出发，通过语义匹配 + 图遍历找到相关实体网络
//
// 优化：使用 GetNeighborsBatch 批量查询邻居，避免 N+1 查询爆炸。
// 最多 3 次 DB 查询：1 次语义检索 + 1 次 Hop-1 批量邻居 + 1 次 Hop-2 批量邻居
func (r *EntityGraphRetriever) Retrieve(ctx context.Context, req EntityGraphQuery) (*EntityGraphResult, error) {
	if req.MaxHops <= 0 {
		req.MaxHops = 2
	}
	if req.MaxEntities <= 0 {
		req.MaxEntities = 10
	}
	if req.MinRelevance <= 0 {
		req.MinRelevance = 0.3
	}

	// 1. 语义检索：找到与查询最相似的实体（Hop 0）— DB 查询 #1
	seedEntities, err := r.store.SearchEntities(ctx, req.UserID, req.QueryVector, req.MaxEntities)
	if err != nil {
		slog.Warn("entity graph: semantic search failed", "error", err)
		return &EntityGraphResult{}, nil
	}

	if len(seedEntities) == 0 {
		return &EntityGraphResult{}, nil
	}

	visited := make(map[string]bool)
	var scoredEntities []ScoredEntity
	var allRelations []Relation

	// 处理种子实体（Hop 0）
	seedIDs := make([]string, 0, len(seedEntities))
	for _, e := range seedEntities {
		if visited[e.ID] {
			continue
		}
		visited[e.ID] = true
		scoredEntities = append(scoredEntities, ScoredEntity{
			Entity:     e,
			Score:      1.0, // 种子实体满分
			HopDistance: 0,
		})
		seedIDs = append(seedIDs, e.ID)
	}

	// Hop 1: 批量获取种子实体的邻居 — DB 查询 #2
	hop1Neighbors, hop1Relations, err := r.store.GetNeighborsBatch(ctx, req.UserID, seedIDs)
	if err != nil {
		slog.Warn("entity graph: batch get hop-1 neighbors failed", "error", err)
	} else {
		allRelations = append(allRelations, hop1Relations...)

		var hop1IDs []string
		for _, seedID := range seedIDs {
			neighbors := hop1Neighbors[seedID]
			for _, n := range neighbors {
				if visited[n.ID] {
					continue
				}
				visited[n.ID] = true
				scoredEntities = append(scoredEntities, ScoredEntity{
					Entity:     n,
					Score:      0.6, // 1跳邻居评分
					HopDistance: 1,
				})
				hop1IDs = append(hop1IDs, n.ID)
			}
		}

		// Hop 2: 批量获取 Hop-1 邻居的邻居 — DB 查询 #3
		if req.MaxHops >= 2 && len(hop1IDs) > 0 {
			hop2Neighbors, hop2Relations, err := r.store.GetNeighborsBatch(ctx, req.UserID, hop1IDs)
			if err != nil {
				slog.Warn("entity graph: batch get hop-2 neighbors failed", "error", err)
			} else {
				allRelations = append(allRelations, hop2Relations...)
				for _, hop1ID := range hop1IDs {
					neighbors := hop2Neighbors[hop1ID]
					for _, e2 := range neighbors {
						if visited[e2.ID] {
							continue
						}
						visited[e2.ID] = true
						scoredEntities = append(scoredEntities, ScoredEntity{
							Entity:     e2,
							Score:      0.3, // 2跳邻居评分
							HopDistance: 2,
						})
					}
				}
			}
		}
	}

	// 3. 去重关系
	allRelations = dedupRelations(allRelations)

	// 4. 按评分排序，截取 Top-N
	sort.Slice(scoredEntities, func(i, j int) bool {
		return scoredEntities[i].Score > scoredEntities[j].Score
	})
	if len(scoredEntities) > req.MaxEntities {
		scoredEntities = scoredEntities[:req.MaxEntities]
	}

	// 5. 格式化为 prompt 文本
	formatted := formatEntityContext(scoredEntities, allRelations)

	slog.Info("entity graph: retrieval completed",
		"user_id", req.UserID,
		"seed_entities", len(seedEntities),
		"total_entities", len(scoredEntities),
		"relations", len(allRelations),
		"db_queries", 3, // 固定 3 次 DB 查询
	)

	return &EntityGraphResult{
		Entities:        scoredEntities,
		Relations:       allRelations,
		FormattedContext: formatted,
	}, nil
}

// formatEntityContext 将实体网络格式化为 prompt 文本
func formatEntityContext(entities []ScoredEntity, relations []Relation) string {
	if len(entities) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\n--- 用户画像网络 ---\n")

	// 按类型分组展示实体
	byType := make(map[EntityType][]ScoredEntity)
	for _, se := range entities {
		byType[se.Entity.EntityType] = append(byType[se.Entity.EntityType], se)
	}

	typeLabels := map[EntityType]string{
		EntityTopic:      "关注话题",
		EntityStyle:      "写作风格",
		EntityPreference: "写作偏好",
		EntityConcept:    "核心概念",
		EntityTone:       "语气偏好",
		EntityStructure:  "结构偏好",
	}

	for et, items := range byType {
		label := typeLabels[et]
		if label == "" {
			label = string(et)
		}
		sb.WriteString(fmt.Sprintf("[%s] ", label))
		names := make([]string, 0, len(items))
		for _, se := range items {
			names = append(names, se.Entity.Name)
		}
		sb.WriteString(strings.Join(names, "、"))
		sb.WriteString("\n")
	}

	// 展示关键关系（偏好类）
	entityNames := make(map[string]string)
	for _, se := range entities {
		entityNames[se.Entity.ID] = se.Entity.Name
	}
	prefCount := 0
	for _, rel := range relations {
		if rel.RelationType == RelPrefers || rel.RelationType == RelDislikes {
			source := entityNames[rel.SourceEntityID]
			target := entityNames[rel.TargetEntityID]
			if source == "" || target == "" {
				continue
			}
			if prefCount == 0 {
				sb.WriteString("[偏好关系] ")
			}
			verb := "偏好"
			if rel.RelationType == RelDislikes {
				verb = "不喜欢"
			}
			sb.WriteString(fmt.Sprintf("%s→%s ", source, verb+" "+target))
			prefCount++
		}
	}
	if prefCount > 0 {
		sb.WriteString("\n")
	}

	return sb.String()
}

// dedupRelations 去重关系
func dedupRelations(relations []Relation) []Relation {
	seen := make(map[string]bool)
	var result []Relation
	for _, r := range relations {
		key := fmt.Sprintf("%s:%s:%s", r.SourceEntityID, r.TargetEntityID, r.RelationType)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, r)
	}
	return result
}

// ─── 实体提取 ──────────────────────────────────────────────

// EntityExtractor 实体提取接口
type EntityExtractor interface {
	// ExtractFromArticle 从文章中提取实体和关系
	ExtractFromArticle(ctx context.Context, article, styleSlug string) ([]ExtractedEntity, error)

	// ExtractFromQuery 从用户查询中提取实体
	ExtractFromQuery(ctx context.Context, query string) ([]ExtractedEntity, error)
}

// ExtractedEntity 提取出的候选实体
type ExtractedEntity struct {
	EntityType  EntityType     `json:"entity_type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Relations   []ExtractedRelation `json:"relations,omitempty"`
}

// ExtractedRelation 提取出的候选关系
type ExtractedRelation struct {
	TargetName   string       `json:"target_name"`
	TargetType   EntityType   `json:"target_type"`
	RelationType RelationType `json:"relation_type"`
	Weight       float64      `json:"weight"`
}
