package tools

import "time"

// ─── WeKnora API Types ──────────────────────────────────
// These types map to the WeKnora REST API response structures.
// WeKnora API base path: /api/v1
// Auth: Bearer token (API Key with scoped permissions)

// WeKnoraKnowledge represents a knowledge entry in WeKnora.
type WeKnoraKnowledge struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content,omitempty"`
	Source    string    `json:"source,omitempty"`
	Status    string    `json:"status,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// WeKnoraKBInfo represents a knowledge base in WeKnora.
type WeKnoraKBInfo struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description,omitempty"`
	DocumentCount int       `json:"document_count,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
}

// WeKnoraSearchResult is a single search result item from WeKnora's hybrid search.
type WeKnoraSearchResult struct {
	ID        string  `json:"id"`
	Content   string  `json:"content"`
	Score     float64 `json:"score"`
	Title     string  `json:"title,omitempty"`
	Source    string  `json:"source,omitempty"`
	Knowledge string  `json:"knowledge_id,omitempty"`
}

// ─── WeKnora API Envelope ───────────────────────────────
// All WeKnora API responses share a common { code, msg, data } envelope.

type weknoraAPIResponse[T any] struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data T      `json:"data"`
}

// Search response data payload.
type weknoraSearchData struct {
	Results []WeKnoraSearchResult `json:"results"`
	Total   int                   `json:"total"`
}

// Knowledge list response data payload.
type weknoraKnowledgeListData struct {
	List  []WeKnoraKnowledge `json:"list"`
	Total int                `json:"total"`
}

// Create knowledge response data payload.
type weknoraCreateData struct {
	ID string `json:"id"`
}

// KB list response data payload.
type weknoraKBListData struct {
	List  []WeKnoraKBInfo `json:"list"`
	Total int             `json:"total"`
}
