package memory

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
)

// ─── 注入遥测（P3）─────────────────────────────────────────
//
// 记忆系统历史上的三类静默失败（检索窗口、NULL scan 吞行、证据死锁）
// 共同教训：没有注入内容级观测，故障可以存活多个版本。TelemetryStore
// 以"只追加、不阻塞、可丢弃"的方式记录每次门控/写回事件的摘要。

// TelemetryEntry 一次记忆事件的遥测摘要。
type TelemetryEntry struct {
	UserID           string
	TraceID          string
	ConversationID   string
	Intent           string
	Source           string // pipeline | harness | tool | api
	Event            string // gate_inject | gate_refusal | explicit_capture | session_extract | dismiss
	InjectedCount    int
	ReviewGuardCount int
	InjectedIDs      []string
	Tiers            []string
	RefusalReason    string
	LatencyMS        int64
	Meta             map[string]any
}

// TelemetrySummary 汇总端点的响应体。
type TelemetrySummary struct {
	WindowDays        int            `json:"window_days"`
	TotalGates        int64          `json:"total_gates"`
	InjectedGates     int64          `json:"injected_gates"`
	RefusedGates      int64          `json:"refused_gates"`
	InjectRate        float64        `json:"inject_rate"`
	AvgInjected       float64        `json:"avg_injected"`
	MaxInjected       int            `json:"max_injected"`
	AvgLatencyMS      float64        `json:"avg_latency_ms"`
	TierDistribution  map[string]int `json:"tier_distribution"`
	RefusalBreakdown  map[string]int `json:"refusal_breakdown"`
	SourceBreakdown   map[string]int `json:"source_breakdown"`
	ExplicitCaptures  int64          `json:"explicit_captures"`
	SessionExtracts   int64          `json:"session_extracts"`
	Dismissals        int64          `json:"dismissals"`
}

// TelemetryStore 异步批量写 memory_telemetry。队列满即丢弃并计数——
// 遥测永远不能拖垮写作主链路。
type TelemetryStore struct {
	db      *database.DB
	queue   chan TelemetryEntry
	dropped atomic.Int64
	wg      sync.WaitGroup
}

// NewTelemetryStore 创建并启动后台写线程。
func NewTelemetryStore(db *database.DB) *TelemetryStore {
	if db == nil {
		return nil
	}
	ts := &TelemetryStore{
		db:    db,
		queue: make(chan TelemetryEntry, 512),
	}
	ts.wg.Add(1)
	go ts.writer()
	return ts
}

// Record 非阻塞入队。
func (ts *TelemetryStore) Record(entry TelemetryEntry) {
	if ts == nil {
		return
	}
	select {
	case ts.queue <- entry:
	default:
		n := ts.dropped.Add(1)
		if n%100 == 1 {
			slog.Warn("memory telemetry: queue full, dropping", "dropped_total", n)
		}
	}
}

// Close 停止写线程（排空队列）。
func (ts *TelemetryStore) Close() {
	if ts == nil {
		return
	}
	close(ts.queue)
	ts.wg.Wait()
}

func (ts *TelemetryStore) writer() {
	defer ts.wg.Done()
	batch := make([]TelemetryEntry, 0, 64)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := ts.insertBatch(context.Background(), batch); err != nil {
			slog.Warn("memory telemetry: batch insert failed", "error", err, "rows", len(batch))
		}
		batch = batch[:0]
	}
	for {
		select {
		case entry, ok := <-ts.queue:
			if !ok {
				flush()
				return
			}
			batch = append(batch, entry)
			if len(batch) >= 64 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func telemetryJSON(v []string) []byte {
	if len(v) == 0 {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

func (ts *TelemetryStore) insertBatch(ctx context.Context, batch []TelemetryEntry) error {
	var firstErr error
	for _, e := range batch {
		meta, _ := json.Marshal(e.Meta)
		if e.Meta == nil {
			meta = nil
		}
		if _, err := ts.db.ExecContext(ctx, `
			INSERT INTO memory_telemetry (
				user_id, trace_id, conversation_id, intent, source, event,
				injected_count, review_guard_count, injected_ids, tiers,
				refusal_reason, latency_ms, metadata
			) VALUES (
				NULLIF($1,'')::uuid, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10::jsonb, $11, $12, $13::jsonb
			)`,
			e.UserID, e.TraceID, e.ConversationID, e.Intent, e.Source, e.Event,
			e.InjectedCount, e.ReviewGuardCount, telemetryJSON(e.InjectedIDs), telemetryJSON(e.Tiers),
			e.RefusalReason, e.LatencyMS, meta,
		); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Summary 聚合最近 days 天的遥测。
func (ts *TelemetryStore) Summary(ctx context.Context, days int) (*TelemetrySummary, error) {
	if days <= 0 {
		days = 7
	}
	since := time.Now().AddDate(0, 0, -days)
	s := &TelemetrySummary{
		WindowDays:       days,
		TierDistribution: map[string]int{},
		RefusalBreakdown: map[string]int{},
		SourceBreakdown:  map[string]int{},
	}

	if err := ts.db.QueryRowContext(ctx, `
		SELECT COALESCE(avg(injected_count) FILTER (WHERE event = 'gate_inject'), 0),
		       COALESCE(max(injected_count) FILTER (WHERE event = 'gate_inject'), 0),
		       COALESCE(avg(latency_ms) FILTER (WHERE event IN ('gate_inject','gate_refusal')), 0)
		FROM memory_telemetry WHERE created_at >= $1
	`, since).Scan(&s.AvgInjected, &s.MaxInjected, &s.AvgLatencyMS); err != nil {
		return nil, err
	}

	var injectRows, refusalRows int64
	if err := ts.db.QueryRowContext(ctx, `
		SELECT count(*) FILTER (WHERE event='gate_inject'),
		       count(*) FILTER (WHERE event='gate_refusal')
		FROM memory_telemetry WHERE created_at >= $1
	`, since).Scan(&injectRows, &refusalRows); err != nil {
		return nil, err
	}
	s.TotalGates = injectRows + refusalRows
	s.InjectedGates = injectRows
	s.RefusedGates = refusalRows
	if s.TotalGates > 0 {
		s.InjectRate = float64(injectRows) / float64(s.TotalGates)
	}

	tierRows, err := ts.db.QueryContext(ctx, `
		SELECT t.value, count(*) FROM memory_telemetry te,
		       jsonb_array_elements_text(COALESCE(te.tiers, '[]'::jsonb)) t
		WHERE te.created_at >= $1 GROUP BY 1 ORDER BY 2 DESC
	`, since)
	if err != nil {
		return nil, err
	}
	defer tierRows.Close()
	for tierRows.Next() {
		var tier string
		var n int
		if err := tierRows.Scan(&tier, &n); err == nil {
			s.TierDistribution[tier] = n
		}
	}

	refusalRowsQ, err := ts.db.QueryContext(ctx, `
		SELECT refusal_reason, count(*) FROM memory_telemetry
		WHERE created_at >= $1 AND event = 'gate_refusal' AND refusal_reason <> ''
		GROUP BY 1 ORDER BY 2 DESC
	`, since)
	if err != nil {
		return nil, err
	}
	defer refusalRowsQ.Close()
	for refusalRowsQ.Next() {
		var reason string
		var n int
		if err := refusalRowsQ.Scan(&reason, &n); err == nil {
			s.RefusalBreakdown[reason] = n
		}
	}

	sourceRows, err := ts.db.QueryContext(ctx, `
		SELECT source, count(*) FROM memory_telemetry
		WHERE created_at >= $1 AND event IN ('gate_inject','gate_refusal')
		GROUP BY 1 ORDER BY 2 DESC
	`, since)
	if err != nil {
		return nil, err
	}
	defer sourceRows.Close()
	for sourceRows.Next() {
		var src string
		var n int
		if err := sourceRows.Scan(&src, &n); err == nil {
			s.SourceBreakdown[src] = n
		}
	}

	if err := ts.db.QueryRowContext(ctx, `
		SELECT count(*) FILTER (WHERE event='explicit_capture'),
		       count(*) FILTER (WHERE event='session_extract'),
		       count(*) FILTER (WHERE event='dismiss')
		FROM memory_telemetry WHERE created_at >= $1
	`, since).Scan(&s.ExplicitCaptures, &s.SessionExtracts, &s.Dismissals); err != nil {
		return nil, err
	}

	return s, nil
}
