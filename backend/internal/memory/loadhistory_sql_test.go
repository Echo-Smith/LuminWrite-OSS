package memory

import (
	"strings"
	"testing"
)

// Layer-0 止血①回归锁：LoadHistory 必须取"最近 N 条"再正序回排。
// 无 DB 测试基建，这里对 SQL 常量做语义断言——防止有人把查询改回
// 朴素的 ORDER BY created_at ASC LIMIT（会话超过 limit 时拿到最早
// N 条，会话越长模型越失忆）。
func TestLoadHistoryQuerySemantics(t *testing.T) {
	q := loadHistoryQuery
	if !strings.Contains(q, "ORDER BY created_at DESC") {
		t.Error("inner query must select newest-first (DESC) before limiting")
	}
	if !strings.Contains(q, "LIMIT $2") {
		t.Error("inner query must apply LIMIT $2 after DESC ordering")
	}
	if !strings.Contains(q, "ORDER BY recent.created_at ASC") {
		t.Error("outer query must re-sort ascending for chronological consumption")
	}
	// 禁止朴素写法回归：WHERE 之后直接 ASC + LIMIT
	naive := "WHERE conversation_id = $1\n\t\tORDER BY created_at ASC\n\t\tLIMIT"
	if strings.Contains(q, naive) {
		t.Error("naive ASC+LIMIT pattern regressed — this returns the OLDEST n messages")
	}
}
