package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/routing"
)

// ─── Metric Types ────────────────────────────────────────

// Counter is an atomic counter metric.
type Counter struct {
	name   string
	help   string
	labels []string
	values sync.Map // labelKey → *atomic.Int64
}

// NewCounter creates a new counter metric.
func NewCounter(name, help string, labels ...string) *Counter {
	return &Counter{name: name, help: help, labels: labels}
}

// Inc increments the counter by 1 for the given label values.
func (c *Counter) Inc(labelValues ...string) {
	c.Add(1, labelValues...)
}

// Add increments the counter by delta for the given label values.
func (c *Counter) Add(delta int64, labelValues ...string) {
	key := labelKey(labelValues)
	val, _ := c.values.LoadOrStore(key, &atomic.Int64{})
	val.(*atomic.Int64).Add(delta)
}

// Histogram tracks bucketed distributions of values.
type Histogram struct {
	name    string
	help    string
	labels  []string
	buckets []float64
	data    sync.Map // labelKey → *histogramData
}

type histogramData struct {
	count    atomic.Int64
	sum      atomic.Int64 // sum in nanoseconds, converted to seconds on export
	buckets  []atomic.Int64
}

// NewHistogram creates a new histogram metric with the given buckets (in seconds).
func NewHistogram(name, help string, buckets []float64, labels ...string) *Histogram {
	return &Histogram{
		name:    name,
		help:    help,
		labels:  labels,
		buckets: buckets,
	}
}

// Observe records a duration for the given label values.
func (h *Histogram) Observe(d time.Duration, labelValues ...string) {
	key := labelKey(labelValues)
	val, _ := h.data.LoadOrStore(key, &histogramData{
		buckets: make([]atomic.Int64, len(h.buckets)),
	})
	hd := val.(*histogramData)
	hd.count.Add(1)
	hd.sum.Add(int64(d))

	seconds := d.Seconds()
	for i, upper := range h.buckets {
		if seconds <= upper {
			hd.buckets[i].Add(1)
		}
	}
}

// Gauge is an atomic gauge metric.
type Gauge struct {
	name   string
	help   string
	labels []string
	values sync.Map // labelKey → *atomic.Int64
}

// NewGauge creates a new gauge metric.
func NewGauge(name, help string, labels ...string) *Gauge {
	return &Gauge{name: name, help: help, labels: labels}
}

// Set sets the gauge value for the given label values.
func (g *Gauge) Set(value int64, labelValues ...string) {
	key := labelKey(labelValues)
	val, _ := g.values.LoadOrStore(key, &atomic.Int64{})
	val.(*atomic.Int64).Store(value)
}

// Inc increments the gauge by 1.
func (g *Gauge) Inc(labelValues ...string) {
	g.Add(1, labelValues...)
}

// Dec decrements the gauge by 1.
func (g *Gauge) Dec(labelValues ...string) {
	g.Add(-1, labelValues...)
}

// Add adds delta to the gauge value.
func (g *Gauge) Add(delta int64, labelValues ...string) {
	key := labelKey(labelValues)
	val, _ := g.values.LoadOrStore(key, &atomic.Int64{})
	val.(*atomic.Int64).Add(delta)
}

// ─── Metrics Registry ───────────────────────────────────

// MetricsRegistry holds all application metrics.
type MetricsRegistry struct {
	mu sync.Mutex

	// HTTP metrics
	HTTPRequestsTotal    *Counter
	HTTPRequestDuration  *Histogram

	// WebSocket metrics
	WSConnectionsActive  *Gauge
	WSErrorsTotal        *Counter

	// Agent metrics
	AgentExecutionsTotal *Counter
	AgentDuration        *Histogram

	// LLM metrics
	LLMCallsTotal        *Counter
	LLMDuration          *Histogram
	LLMErrorsTotal       *Counter

	// Profile cache metrics
	ProfileCacheHits     *Counter
	ProfileCacheMisses   *Counter

	// Evaluation metrics
	EvalRunsTotal        *Counter
	EvalRunsActive       *Gauge

	// Database metrics
	DBQueriesTotal       *Counter
	DBErrorsTotal       *Counter

	// Grayscale routing metrics
	GrayscaleRequestsTotal  *Counter
	GrayscaleNewVersionHits *Counter
	GrayscaleFallbackHits   *Counter
	GrayscaleErrors         *Counter

	// All metrics for export
	counters   []*Counter
	histograms []*Histogram
	gauges     []*Gauge
}

// NewMetricsRegistry creates and initializes all metrics.
func NewMetricsRegistry() *MetricsRegistry {
	defaultBuckets := []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0}
	llmBuckets := []float64{0.1, 0.5, 1.0, 2.0, 5.0, 10.0, 30.0, 60.0, 120.0}

	r := &MetricsRegistry{
		HTTPRequestsTotal:    NewCounter("http_requests_total", "Total HTTP requests", "method", "path", "status"),
		HTTPRequestDuration:  NewHistogram("http_request_duration_seconds", "HTTP request duration", defaultBuckets, "method", "path"),
		WSConnectionsActive:  NewGauge("websocket_connections_active", "Active WebSocket connections"),
		WSErrorsTotal:        NewCounter("websocket_errors_total", "Total WebSocket errors", "type"),
		AgentExecutionsTotal: NewCounter("agent_executions_total", "Total agent executions", "style", "status"),
		AgentDuration:        NewHistogram("agent_execution_duration_seconds", "Agent execution duration", defaultBuckets, "style"),
		LLMCallsTotal:        NewCounter("llm_calls_total", "Total LLM API calls", "model", "type"),
		LLMDuration:          NewHistogram("llm_call_duration_seconds", "LLM call duration", llmBuckets, "model"),
		LLMErrorsTotal:       NewCounter("llm_errors_total", "Total LLM API errors", "model", "error_type"),
		ProfileCacheHits:     NewCounter("profile_cache_hits_total", "Profile cache hits"),
		ProfileCacheMisses:   NewCounter("profile_cache_misses_total", "Profile cache misses"),
		EvalRunsTotal:        NewCounter("evaluation_runs_total", "Total evaluation runs", "status"),
		EvalRunsActive:       NewGauge("evaluation_runs_active", "Active evaluation runs"),
		DBQueriesTotal:       NewCounter("db_queries_total", "Total database queries"),
		DBErrorsTotal:       NewCounter("db_errors_total", "Total database errors"),
		GrayscaleRequestsTotal:  NewCounter("grayscale_requests_total", "Total grayscale routing decisions", "slug", "result"),
		GrayscaleNewVersionHits: NewCounter("grayscale_new_version_hits_total", "Times new version was served", "slug"),
		GrayscaleFallbackHits:   NewCounter("grayscale_fallback_hits_total", "Times fallback version was served", "slug"),
		GrayscaleErrors:         NewCounter("grayscale_errors_total", "Grayscale routing errors", "slug", "type"),
	}

	r.counters = []*Counter{
		r.HTTPRequestsTotal,
		r.WSErrorsTotal,
		r.AgentExecutionsTotal,
		r.LLMCallsTotal,
		r.LLMErrorsTotal,
		r.ProfileCacheHits,
		r.ProfileCacheMisses,
		r.EvalRunsTotal,
		r.DBQueriesTotal,
		r.DBErrorsTotal,
		r.GrayscaleRequestsTotal,
		r.GrayscaleNewVersionHits,
		r.GrayscaleFallbackHits,
		r.GrayscaleErrors,
	}

	r.histograms = []*Histogram{
		r.HTTPRequestDuration,
		r.AgentDuration,
		r.LLMDuration,
	}

	r.gauges = []*Gauge{
		r.WSConnectionsActive,
		r.EvalRunsActive,
	}

	return r
}

// Export writes all metrics in Prometheus text exposition format.
func (r *MetricsRegistry) Export(w io.Writer) {
	// Counters
	for _, c := range r.counters {
		fmt.Fprintf(w, "# HELP %s %s\n", c.name, c.help)
		fmt.Fprintf(w, "# TYPE %s counter\n", c.name)

		type entry struct {
			key   string
			value int64
		}
		var entries []entry
		c.values.Range(func(k, v interface{}) bool {
			entries = append(entries, entry{k.(string), v.(*atomic.Int64).Load()})
			return true
		})
		sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })

		if len(entries) == 0 {
			fmt.Fprintf(w, "%s 0\n", c.name)
		}
		for _, e := range entries {
			if e.key == "" {
				fmt.Fprintf(w, "%s %d\n", c.name, e.value)
			} else {
				fmt.Fprintf(w, "%s{%s} %d\n", c.name, e.key, e.value)
			}
		}
		fmt.Fprintln(w)
	}

	// Gauges
	for _, g := range r.gauges {
		fmt.Fprintf(w, "# HELP %s %s\n", g.name, g.help)
		fmt.Fprintf(w, "# TYPE %s gauge\n", g.name)

		type entry struct {
			key   string
			value int64
		}
		var entries []entry
		g.values.Range(func(k, v interface{}) bool {
			entries = append(entries, entry{k.(string), v.(*atomic.Int64).Load()})
			return true
		})
		sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })

		if len(entries) == 0 {
			fmt.Fprintf(w, "%s 0\n", g.name)
		}
		for _, e := range entries {
			if e.key == "" {
				fmt.Fprintf(w, "%s %d\n", g.name, e.value)
			} else {
				fmt.Fprintf(w, "%s{%s} %d\n", g.name, e.key, e.value)
			}
		}
		fmt.Fprintln(w)
	}

	// Histograms
	for _, h := range r.histograms {
		fmt.Fprintf(w, "# HELP %s %s\n", h.name, h.help)
		fmt.Fprintf(w, "# TYPE %s histogram\n", h.name)

		type entry struct {
			key string
			hd  *histogramData
		}
		var entries []entry
		h.data.Range(func(k, v interface{}) bool {
			entries = append(entries, entry{k.(string), v.(*histogramData)})
			return true
		})
		sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })

		if len(entries) == 0 {
			// Emit zero-value histogram
			for _, b := range h.buckets {
				fmt.Fprintf(w, "%s_bucket{le=\"%g\"} 0\n", h.name, b)
			}
			fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} 0\n", h.name)
			fmt.Fprintf(w, "%s_count 0\n", h.name)
			fmt.Fprintf(w, "%s_sum 0\n", h.name)
		}
		for _, e := range entries {
			labelPrefix := ""
			if e.key != "" {
				labelPrefix = e.key + ","
			}
			for i, b := range h.buckets {
				fmt.Fprintf(w, "%s_bucket{%sle=\"%g\"} %d\n", h.name, labelPrefix, b, e.hd.buckets[i].Load())
			}
			fmt.Fprintf(w, "%s_bucket{%sle=\"+Inf\"} %d\n", h.name, labelPrefix, e.hd.count.Load())
			fmt.Fprintf(w, "%s_count{%s} %d\n", h.name, e.key, e.hd.count.Load())
			fmt.Fprintf(w, "%s_sum{%s} %f\n", h.name, e.key, float64(e.hd.sum.Load())/float64(time.Second))
		}
		fmt.Fprintln(w)
	}
}

// ─── Helpers ────────────────────────────────────────────

// labelKey builds a Prometheus label key string from label values.
func labelKey(values []string) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = fmt.Sprintf("v%d=\"%s\"", i, escapeLabelValue(v))
	}
	return strings.Join(parts, ",")
}

func escapeLabelValue(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

// RecordLLMCall records an LLM API call metric (implements tools.LLMMetricsRecorder).
func (r *MetricsRegistry) RecordLLMCall(model string, callType string, duration time.Duration) {
	r.LLMCallsTotal.Inc(model, callType)
	r.LLMDuration.Observe(duration, model)
}

// RecordLLMError records an LLM API error metric (implements tools.LLMMetricsRecorder).
func (r *MetricsRegistry) RecordLLMError(model string, errorType string) {
	r.LLMErrorsTotal.Inc(model, errorType)
}

// ─── HTTP Handler & Middleware ──────────────────────────

// handleMetrics exports all metrics in Prometheus format.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	if s.metrics != nil {
		// Sync DB metrics from the global DB counters
		if dbm := database.GetDBMetrics(); dbm != nil {
			s.metrics.DBQueriesTotal.Add(dbm.Queries.Load())
			s.metrics.DBErrorsTotal.Add(dbm.Errors.Load())
			dbm.Queries.Store(0)
			dbm.Errors.Store(0)
		}
		// Sync profile cache metrics
		if s.profiles != nil {
			s.metrics.ProfileCacheHits.Add(s.profiles.L1Hits.Load())
			s.metrics.ProfileCacheHits.Add(s.profiles.L2Hits.Load())
			s.metrics.ProfileCacheMisses.Add(s.profiles.Misses.Load())
			s.profiles.L1Hits.Store(0)
			s.profiles.L2Hits.Store(0)
			s.profiles.Misses.Store(0)
		}
		// Sync grayscale routing metrics (labels: slug="", result)
		nv := routing.RolloutMetrics.NewVersion.Load()
		ov := routing.RolloutMetrics.OldVersion.Load()
		for i := int64(0); i < nv; i++ {
			s.metrics.GrayscaleNewVersionHits.Inc("global")
		}
		for i := int64(0); i < ov; i++ {
			s.metrics.GrayscaleFallbackHits.Inc("global")
		}
		routing.RolloutMetrics.NewVersion.Store(0)
		routing.RolloutMetrics.OldVersion.Store(0)
		s.metrics.Export(w)
	} else {
		w.Write([]byte("# metrics not initialized\n"))
	}
}

// metricsMiddleware records HTTP request metrics.
func (s *Server) metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.metrics == nil {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()

		// Wrap the ResponseWriter to capture status code
		rw := &statusCaptureWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)

		duration := time.Since(start)
		path := normalizePath(r.URL.Path)
		s.metrics.HTTPRequestsTotal.Inc(r.Method, path, fmt.Sprintf("%d", rw.status))
		s.metrics.HTTPRequestDuration.Observe(duration, r.Method, path)
	})
}

// statusCaptureWriter wraps http.ResponseWriter to capture the status code.
// It also implements http.Hijacker and http.Flusher so that WebSocket
// upgrade requests and streaming responses work transparently.
type statusCaptureWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusCaptureWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Hijack delegates to the underlying ResponseWriter so that WebSocket
// connections can be upgraded through the metrics middleware.
func (w *statusCaptureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("ResponseWriter does not implement http.Hijacker")
	}
	return hj.Hijack()
}

// Flush delegates to the underlying ResponseWriter if it implements http.Flusher.
func (w *statusCaptureWriter) Flush() {
	if fl, ok := w.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

// Push delegates to the underlying ResponseWriter if it implements http.Pusher.
func (w *statusCaptureWriter) Push(target string, opts *http.PushOptions) error {
	if ps, ok := w.ResponseWriter.(http.Pusher); ok {
		return ps.Push(target, opts)
	}
	return http.ErrNotSupported
}

// normalizePath collapses path parameters for cardinality control.
// e.g. /api/v2/styles/yinyue → /api/v2/styles/{slug}
func normalizePath(path string) string {
	// Replace UUID-like segments
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		if seg == "" {
			continue
		}
		// Replace UUIDs and long IDs
		if len(seg) > 20 || isUUID(seg) {
			segments[i] = "{id}"
		}
	}
	return strings.Join(segments, "/")
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	return s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-'
}
