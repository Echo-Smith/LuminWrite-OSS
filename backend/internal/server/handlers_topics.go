package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Topic Detail + AI Writing Angles ────────────────────

// handleTopicDetail returns topic details plus AI-generated writing angle suggestions.
func (s *Server) handleTopicDetail(w http.ResponseWriter, r *http.Request) {
	topicID := chi.URLParam(r, "id")
	if topicID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "topic id is required")
		return
	}

	if s.traces == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	topic, err := s.traces.GetTopicByID(r.Context(), topicID)
	if err != nil {
		response.Err(w, http.StatusNotFound, "not_found", fmt.Sprintf("topic not found: %v", err))
		return
	}

	// Get related completed traces (articles written for this topic)
	title, _ := topic["title"].(string)
	relatedTraces, _ := s.traces.ListRelatedTraces(r.Context(), title, 5)

	// Check if user has favorited this topic
	userID := s.getUserIDFromRequest(r)
	favorited := false
	if userID != "" && userID != "anonymous" {
		favorited, _ = s.traces.IsTopicFavorited(r.Context(), userID, topicID)
	}

	// Generate AI writing angles
	var writingAngles []map[string]interface{}
	if s.llm != nil {
		angles, err := s.generateWritingAngles(r.Context(), topic)
		if err != nil {
			slog.Warn("failed to generate writing angles", "error", err, "topic_id", topicID)
		} else {
			writingAngles = angles
		}
	}

	response.OK(w, map[string]interface{}{
		"topic":            topic,
		"writing_angles":   writingAngles,
		"related_articles": relatedTraces,
		"favorited":        favorited,
	})
}

// generateWritingAngles uses the LLM to suggest 3-5 writing angles for a topic.
func (s *Server) generateWritingAngles(ctx context.Context, topic map[string]interface{}) ([]map[string]interface{}, error) {
	title, _ := topic["title"].(string)
	description, _ := topic["description"].(string)
	platform, _ := topic["platform"].(string)

	prompt := fmt.Sprintf(`你是一位资深新闻评论编辑。请为以下选题生成 3-5 个不同的写作角度建议。

选题标题: %s
选题描述: %s
来源平台: %s

请以 JSON 数组格式返回，每个角度包含以下字段：
- angle: 写作角度名称（简洁有力）
- style: 推荐写作风格（yinyue/shenlun/xiaohongshu 之一）
- word_count: 推荐字数
- rationale: 为什么从这个角度写的理由（1-2句话）

只返回 JSON 数组，不要其他文字。`, title, description, platform)

	messages := []tools.LLMMessage{
		{Role: "system", Content: "你是一位资深新闻评论编辑，擅长从多角度分析选题。"},
		{Role: "user", Content: prompt},
	}

	content, _, err := s.llm.Chat(ctx, messages, tools.WithTemperature(0.7))
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	// Extract JSON array from response
	jsonStr := tools.ExtractJSONArray(content)
	if jsonStr == "" {
		return nil, fmt.Errorf("failed to extract JSON from LLM response")
	}

	var angles []map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &angles); err != nil {
		return nil, fmt.Errorf("failed to parse angles JSON: %w", err)
	}

	return angles, nil
}

// ─── AI Topic Recommendation ──────────────────────────────

// handleTopicRecommend returns AI-curated topic recommendations for the user.
//
// Caching strategy:
// - First request: generates recommendations via LLM and caches to DB (1h TTL)
// - Subsequent requests: returns cached result (fast, no LLM call)
// - ?force=1: bypasses cache and regenerates (user clicks "换一批")
func (s *Server) handleTopicRecommend(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserIDFromRequest(r)

	if s.traces == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	forceRefresh := r.URL.Query().Get("force") == "1"

	// 1. Try DB cache first (unless force refresh)
	if !forceRefresh && userID != "" && userID != "anonymous" {
		cached, ok, err := s.traces.GetCachedRecommendations(r.Context(), userID)
		if err != nil {
			slog.Warn("failed to get cached recommendations", "error", err)
		}
		if ok && cached != nil {
			response.OK(w, map[string]interface{}{
				"recommendations": cached,
				"reason":          "cached",
				"cached":          true,
			})
			return
		}
	}

	// 2. Cache miss / expired / force — generate fresh recommendations
	// Get user's recent writing history to understand preferences
	var recentTitles []string
	if userID != "" && userID != "anonymous" {
		recentTraces, err := s.traces.ListUserRecentTraces(r.Context(), userID, 10)
		if err == nil {
			for _, t := range recentTraces {
				if title, ok := t["article_title"].(string); ok && title != "" {
					recentTitles = append(recentTitles, title)
				}
			}
		}
	}

	// Get recent active topics (not yet written by user)
	topics, _, err := s.traces.ListTopics(r.Context(), "", 1, 20)
	if err != nil {
		slog.Warn("failed to list topics for recommendation", "error", err)
		topics = []map[string]interface{}{}
	}

	// If LLM is available, use it to rank/recommend topics
	var recommendations []map[string]interface{}
	if s.llm != nil && len(topics) > 0 {
		recs, err := s.generateTopicRecommendations(r.Context(), topics, recentTitles)
		if err != nil {
			slog.Warn("failed to generate recommendations, returning top topics", "error", err)
			// Fallback: return first 5 topics
			limit := 5
			if len(topics) < limit {
				limit = len(topics)
			}
			recommendations = topics[:limit]
		} else {
			recommendations = recs
		}
	} else {
		limit := 5
		if len(topics) < limit {
			limit = len(topics)
		}
		recommendations = topics[:limit]
	}

	// 3. Save to DB cache
	if userID != "" && userID != "anonymous" {
		if err := s.traces.SaveRecommendations(r.Context(), userID, recommendations); err != nil {
			slog.Warn("failed to cache recommendations", "error", err)
		}
	}

	response.OK(w, map[string]interface{}{
		"recommendations": recommendations,
		"reason":          "based on trending topics and your writing history",
		"cached":          false,
	})
}

// generateTopicRecommendations uses LLM to pick and rank the best topics for the user.
func (s *Server) generateTopicRecommendations(ctx context.Context, topics []map[string]interface{}, recentTitles []string) ([]map[string]interface{}, error) {
	// Build a compact topic list for the prompt
	type topicItem struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Rank  int    `json:"rank"`
	}

	var items []topicItem
	for i, t := range topics {
		id, _ := t["id"].(string)
		title, _ := t["title"].(string)
		rank := 0
		if r, ok := t["hot_rank"].(int); ok {
			rank = r
		}
		items = append(items, topicItem{ID: id, Title: title, Rank: rank})
		_ = i
	}

	topicsJSON, _ := json.Marshal(items)
	recentStr := "无"
	if len(recentTitles) > 0 {
		recentJSON, _ := json.Marshal(recentTitles)
		recentStr = string(recentJSON)
	}

	prompt := fmt.Sprintf(`你是一位选题编辑。请从以下热门选题中为用户推荐 5 个最值得写的选题。

当前热门选题:
%s

用户最近写过的文章标题:
%s

推荐原则：
1. 优先推荐热度高、话题性强的选题
2. 避免与用户已写过的选题重复
3. 考虑选题的深度和可评论性

请以 JSON 数组格式返回推荐的选题 id 列表，并附上推荐理由：
[{"id": "选题id", "reason": "推荐理由（1句话）"}]

只返回 JSON 数组。`, string(topicsJSON), recentStr)

	messages := []tools.LLMMessage{
		{Role: "system", Content: "你是一位资深选题编辑，擅长挑选最有价值的评论选题。"},
		{Role: "user", Content: prompt},
	}

	content, _, err := s.llm.Chat(ctx, messages, tools.WithTemperature(0.5))
	if err != nil {
		return nil, err
	}

	jsonStr := tools.ExtractJSONArray(content)
	if jsonStr == "" {
		return nil, fmt.Errorf("failed to extract JSON from LLM response")
	}

	var recs []struct {
		ID     string `json:"id"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &recs); err != nil {
		return nil, fmt.Errorf("failed to parse recommendations: %w", err)
	}

	// Build a lookup map for topics
	topicMap := make(map[string]map[string]interface{})
	for _, t := range topics {
		if id, ok := t["id"].(string); ok {
			topicMap[id] = t
		}
	}

	// Merge recommendation reasons into topic data
	var result []map[string]interface{}
	for _, rec := range recs {
		if t, ok := topicMap[rec.ID]; ok {
			t["recommendation_reason"] = rec.Reason
			result = append(result, t)
		}
	}

	if len(result) == 0 {
		// Fallback: return first 5 topics
		limit := 5
		if len(topics) < limit {
			limit = len(topics)
		}
		return topics[:limit], nil
	}

	return result, nil
}

// ─── Topic Favorites ──────────────────────────────────────

// handleFavoriteTopic adds a topic to the user's favorites.
func (s *Server) handleFavoriteTopic(w http.ResponseWriter, r *http.Request) {
	topicID := chi.URLParam(r, "id")
	userID := s.getUserIDFromRequest(r)

	if userID == "" || userID == "anonymous" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "please login to favorite topics")
		return
	}

	if s.traces == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	if err := s.traces.FavoriteTopic(r.Context(), userID, topicID); err != nil {
		slog.Warn("failed to favorite topic", "error", err, "topic_id", topicID, "user_id", userID)
		response.Err(w, http.StatusInternalServerError, "db_error", "failed to favorite topic")
		return
	}

	response.OK(w, map[string]interface{}{
		"topic_id":  topicID,
		"favorited": true,
	})
}

// handleUnfavoriteTopic removes a topic from the user's favorites.
func (s *Server) handleUnfavoriteTopic(w http.ResponseWriter, r *http.Request) {
	topicID := chi.URLParam(r, "id")
	userID := s.getUserIDFromRequest(r)

	if userID == "" || userID == "anonymous" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "please login to unfavorite topics")
		return
	}

	if s.traces == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	if err := s.traces.UnfavoriteTopic(r.Context(), userID, topicID); err != nil {
		slog.Warn("failed to unfavorite topic", "error", err, "topic_id", topicID, "user_id", userID)
		response.Err(w, http.StatusInternalServerError, "db_error", "failed to unfavorite topic")
		return
	}

	response.OK(w, map[string]interface{}{
		"topic_id":  topicID,
		"favorited": false,
	})
}

// handleListFavoriteTopics lists the user's favorited topics.
func (s *Server) handleListFavoriteTopics(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserIDFromRequest(r)
	page := 1
	pageSize := 20
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			page = v
		}
	}
	if ps := r.URL.Query().Get("page_size"); ps != "" {
		if v, err := strconv.Atoi(ps); err == nil {
			pageSize = v
		}
	}

	if userID == "" || userID == "anonymous" {
		response.OK(w, map[string]interface{}{"topics": []interface{}{}, "total": 0})
		return
	}

	if s.traces == nil {
		response.OK(w, map[string]interface{}{"topics": []interface{}{}, "total": 0})
		return
	}

	topics, total, err := s.traces.ListFavoriteTopics(r.Context(), userID, page, pageSize)
	if err != nil {
		slog.Warn("failed to list favorite topics", "error", err)
		topics = []map[string]interface{}{}
	}
	if topics == nil {
		topics = []map[string]interface{}{}
	}

	response.OK(w, map[string]interface{}{"topics": topics, "total": total})
}

// ─── Platform Aggregation ────────────────────────────────

// handlePlatformStats returns topic counts grouped by platform.
func (s *Server) handlePlatformStats(w http.ResponseWriter, r *http.Request) {
	if s.traces == nil {
		response.OK(w, map[string]interface{}{"platforms": []interface{}{}})
		return
	}

	stats, err := s.traces.GetPlatformStats(r.Context())
	if err != nil {
		slog.Warn("failed to get platform stats", "error", err)
		stats = nil
	}
	if stats == nil {
		stats = []database.PlatformStat{}
	}

	response.OK(w, map[string]interface{}{"platforms": stats})
}

// handleListTopicsByPlatform lists topics filtered by platform.
func (s *Server) handleListTopicsByPlatform(w http.ResponseWriter, r *http.Request) {
	platform := chi.URLParam(r, "platform")
	page := 1
	pageSize := 20
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			page = v
		}
	}
	if ps := r.URL.Query().Get("page_size"); ps != "" {
		if v, err := strconv.Atoi(ps); err == nil {
			pageSize = v
		}
	}

	if s.traces == nil {
		response.OK(w, map[string]interface{}{"topics": []interface{}{}, "total": 0})
		return
	}

	topics, total, err := s.traces.ListTopicsByPlatform(r.Context(), platform, page, pageSize)
	if err != nil {
		slog.Warn("failed to list topics by platform", "error", err)
		topics = []map[string]interface{}{}
	}
	if topics == nil {
		topics = []map[string]interface{}{}
	}

	response.OK(w, map[string]interface{}{"topics": topics, "total": total})
}

// ─── Topic Trend ─────────────────────────────────────────

// handleTopicTrend returns the hot-rank trend data for a topic.
func (s *Server) handleTopicTrend(w http.ResponseWriter, r *http.Request) {
	topicID := chi.URLParam(r, "id")
	hours := 24
	if h := r.URL.Query().Get("hours"); h != "" {
		if v, err := strconv.Atoi(h); err == nil && v > 0 {
			hours = v
		}
	}

	if s.traces == nil {
		response.OK(w, map[string]interface{}{"trend": []interface{}{}, "topic_id": topicID})
		return
	}

	points, err := s.traces.GetTopicTrend(r.Context(), topicID, hours)
	if err != nil {
		slog.Warn("failed to get topic trend", "error", err, "topic_id", topicID)
		points = nil
	}
	if points == nil {
		points = []database.TrendPoint{}
	}

	response.OK(w, map[string]interface{}{
		"topic_id": topicID,
		"hours":    hours,
		"trend":    points,
	})
}

// ─── Helper: extract user ID from JWT in request ─────────

func (s *Server) getUserIDFromRequest(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "anonymous"
	}

	// Strip "Bearer " prefix
	token := authHeader
	if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
		token = authHeader[7:]
	}

	payload, err := s.ValidateJWT(token)
	if err != nil {
		return "anonymous"
	}
	return payload.Sub
}

// ─── Cron: Record Topic Trends ───────────────────────────

// cronRecordTopicTrends is a cron job that snapshots current hot topic rankings.
func (s *Server) cronRecordTopicTrends(ctx context.Context) error {
	if s.traces == nil {
		return fmt.Errorf("database not available")
	}
	return s.traces.RecordTopicTrends(ctx)
}

// ─── Hot Topics Fetch (Tencent + Weibo) ──────────────────

// handleDeleteTopic deletes a user-created topic by ID.
// DELETE /api/v2/topics/{id}
func (s *Server) handleDeleteTopic(w http.ResponseWriter, r *http.Request) {
	topicID := chi.URLParam(r, "id")
	if topicID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "topic id is required")
		return
	}

	if s.traces == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	if err := s.traces.DeleteTopic(r.Context(), topicID); err != nil {
		slog.Warn("failed to delete topic", "error", err, "topic_id", topicID)
		response.Err(w, http.StatusInternalServerError, "db_error", "failed to delete topic")
		return
	}

	response.OK(w, map[string]interface{}{
		"topic_id": topicID,
		"deleted":  true,
	})
}

// handleUpdateTopic updates the title and description of a user-created topic.
// PUT /api/v2/topics/{id}
func (s *Server) handleUpdateTopic(w http.ResponseWriter, r *http.Request) {
	topicID := chi.URLParam(r, "id")
	if topicID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "topic id is required")
		return
	}

	var req struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if req.Title == nil || *req.Title == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "title is required")
		return
	}

	if s.traces == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	if err := s.traces.UpdateTopic(r.Context(), topicID, *req.Title, *req.Description); err != nil {
		slog.Warn("failed to update topic", "error", err, "topic_id", topicID)
		response.Err(w, http.StatusInternalServerError, "db_error", "failed to update topic")
		return
	}

	response.OK(w, map[string]interface{}{
		"topic_id":    topicID,
		"title":       *req.Title,
		"description": *req.Description,
		"updated":     true,
	})
}

// handleFetchHotTopics fetches hot topics from external sources (Tencent, Weibo),
// saves them to the database, and returns the fetched topics.
// POST /api/v2/topics/hot
func (s *Server) handleFetchHotTopics(w http.ResponseWriter, r *http.Request) {
	if s.search == nil {
		response.Err(w, http.StatusServiceUnavailable, "search_unavailable", "search client not configured")
		return
	}

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 100 {
			limit = v
		}
	}

	// Fetch hot topics from all configured sources
	topics := s.search.FetchHotTopics(r.Context(), limit)

	saved := 0
	if s.traces != nil && len(topics) > 0 {
		n, err := s.traces.UpsertHotTopics(r.Context(), topics)
		if err != nil {
			slog.Warn("failed to save hot topics", "error", err)
		} else {
			saved = n
		}
	}

	// Broadcast to SSE clients
	for _, topic := range topics {
		title, _ := topic["title"].(string)
		if title == "" {
			continue
		}
		s.sseHub.Broadcast(&SSEEvent{
			Event: "topic:new",
			Data:  topic,
		})
	}

	response.OK(w, map[string]interface{}{
		"topics":    topics,
		"fetched":   len(topics),
		"saved":     saved,
		"sources":   s.search.ActiveSources(),
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// cronFetchHotTopics is a cron job that periodically fetches hot topics from external sources.
func (s *Server) cronFetchHotTopics(ctx context.Context) error {
	if s.search == nil {
		return fmt.Errorf("search client not configured")
	}
	if s.traces == nil {
		return fmt.Errorf("database not available")
	}

	topics := s.search.FetchHotTopics(ctx, 20)
	if len(topics) == 0 {
		slog.Info("cron: no hot topics fetched")
		return nil
	}

	saved, err := s.traces.UpsertHotTopics(ctx, topics)
	if err != nil {
		return fmt.Errorf("failed to save hot topics: %w", err)
	}

	slog.Info("cron: hot topics fetched and saved", "fetched", len(topics), "saved", saved)

	// Also record trends for the newly fetched topics
	if err := s.traces.RecordTopicTrends(ctx); err != nil {
		slog.Warn("cron: failed to record topic trends", "error", err)
	}

	return nil
}
