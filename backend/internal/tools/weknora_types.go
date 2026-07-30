package tools

import "time"

// ─── WeKnora API Types ──────────────────────────────────
// These types map to the WeKnora REST API response structures.
// WeKnora API base path: /api/v1
// Auth: JWT Bearer token (obtained via /auth/login)
//
// Response envelope:
//   Success: {"data": <T>, "success": true}
//   Error:   {"error": {"code": N, "message": "...", "details": null}, "success": false}

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
	DocumentCount int       `json:"knowledge_count,omitempty"`
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
// All WeKnora API responses share a common { success, data } envelope.
// Error responses include an "error" object with code/message/details.

type weknoraAPIResponse[T any] struct {
	Data    T                 `json:"data"`
	Success bool              `json:"success"`
	Error   *weknoraAPIError  `json:"error,omitempty"`
}

type weknoraAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
}

// Search response data payload.
type weknoraSearchData struct {
	Results []WeKnoraSearchResult `json:"results"`
	Total   int                   `json:"total"`
}

// Knowledge list response data payload.
// WeKnora returns data as a direct array with separate total/page/page_size fields.
type weknoraKnowledgeListData struct {
	List  []WeKnoraKnowledge `json:"-"`
	Total int                `json:"total"`
}

// Create knowledge response data payload.
type weknoraCreateData struct {
	ID string `json:"id"`
}

// KB list response: data is a direct array of KB objects.
type weknoraKBListData struct {
	List  []WeKnoraKBInfo `json:"-"`
	Total int             `json:"-"`
}

// ─── Auth Types ─────────────────────────────────────────

type weknoraLoginResponse struct {
	Success bool   `json:"success"`
	Token   string `json:"token"`
	User    struct {
		ID       string `json:"id"`
		Email    string `json:"email"`
		Username string `json:"username"`
	} `json:"user"`
	ActiveTenant struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"active_tenant"`
}

// CreateKBRequest is the body for creating a new knowledge base.
type CreateKBRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// CreateKBResponse is the response from creating a knowledge base.
type CreateKBResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	TenantID    int    `json:"tenant_id"`
}
