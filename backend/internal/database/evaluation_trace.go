package database

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

// ─── Merged Trace Analysis (Phase 1: benchmark + WABench + agent trace) ──────
//
// One source of truth for all three evaluation surfaces:
//
//	wabench_runs.run_id
//	  -> wabench_outputs (per case, .trace_ref = agent_traces.trace_id)
//	    -> agent_traces.{ llm_calls, tool_calls, pipeline_metadata,  (heavy, lazy detail)
//	                      <summary cols>, cost_*, first_token_ms,   (flat, cheap)
//	                      output_tokens_per_sec, model, agent_mode }
//
// The heavy per-call arrays are parsed only when a run's trace is actually
// opened (step breakdown / timeline); run-level aggregation and A/B compare read
// the flat summary columns via SQL, mirroring llm-space's lazy-load design.

type TraceCostBreakdown struct {
	Input      float64 `json:"input,omitempty"`
	Output     float64 `json:"output,omitempty"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
	Total      float64 `json:"total"`
}

// StepBreakdown is the per-step resource summary.
type StepBreakdown struct {
	LLMCalls      int   `json:"llm_calls,omitempty"`
	Tokens        int   `json:"tokens,omitempty"`
	LatencyMs     int64 `json:"latency_ms,omitempty"`
	ToolCalls     int   `json:"tool_calls,omitempty"`
	ToolLatencyMs int64 `json:"tool_latency_ms,omitempty"`
}

// EvaluationTraceAnalysis is the merged trace view for one WABench output.
type EvaluationTraceAnalysis struct {
	ID                    string                 `json:"id"`
	SampleID              string                 `json:"sample_id"`
	RunID                 string                 `json:"run_id"`
	TraceID               string                 `json:"trace_id"`
	TotalLLMCalls         int                    `json:"total_llm_calls"`
	TotalPromptTokens     int                    `json:"total_prompt_tokens"`
	TotalCompletionTokens int                    `json:"total_completion_tokens"`
	TotalReasoningTokens  int                    `json:"total_reasoning_tokens"`
	TotalTokens           int                    `json:"total_tokens"`
	TotalLLMLatencyMs     int64                  `json:"total_llm_latency_ms"`
	AvgLLMLatencyMs       int64                  `json:"avg_llm_latency_ms"`
	TTFTMs                *int64                 `json:"ttft_ms,omitempty"`
	OutputTokensPerSec    *float64               `json:"output_tokens_per_sec,omitempty"`
	Model                 string                 `json:"model,omitempty"`
	TotalToolCalls        int                    `json:"total_tool_calls"`
	TotalToolLatencyMs    int64                  `json:"total_tool_latency_ms"`
	AvgToolLatencyMs      int64                  `json:"avg_tool_latency_ms"`
	StepBreakdown         map[string]StepBreakdown `json:"step_breakdown"`
	Cost                  TraceCostBreakdown     `json:"cost"`
	EstimatedCost         float64                `json:"estimated_cost"`
	AgentMode             string                 `json:"agent_mode"`
	ExecutionPlan         []string               `json:"execution_plan"`
	SkippedSteps          []string               `json:"skipped_steps"`
	CreatedAt             time.Time              `json:"created_at"`
}

// RunTraceSummary is the aggregate the Eval Center overview shows.
type RunTraceSummary struct {
	SamplesAnalyzed    int      `json:"samples_analyzed"`
	AvgLLMCalls        float64  `json:"avg_llm_calls"`
	AvgTokens          int      `json:"avg_tokens"`
	AvgTotalLatencyMs  float64  `json:"avg_total_latency_ms"`
	AvgCostPerSample   float64  `json:"avg_cost_per_sample"`
	TotalCost          float64  `json:"total_cost"`
	AvgTTFTMs          *float64 `json:"avg_ttft_ms,omitempty"`
	AvgOutputTokensPerSec *float64 `json:"avg_output_tokens_per_sec,omitempty"`
}

// TraceComparison is a baseline-vs-candidate delta report.
type TraceComparison struct {
	BaselineRunID  string          `json:"baseline_run_id"`
	CandidateRunID string          `json:"candidate_run_id"`
	Baseline       RunTraceSummary `json:"baseline"`
	Candidate      RunTraceSummary `json:"candidate"`
	Deltas         TraceDeltas     `json:"deltas"`
}

type TraceDeltas struct {
	LLMCallDelta   float64 `json:"llm_call_delta"`
	TokenDelta     int     `json:"token_delta"`
	LatencyDeltaMs float64 `json:"latency_delta_ms"`
	CostDelta      float64 `json:"cost_delta"`
	TTFTDeltaMs    float64 `json:"ttft_delta_ms"`
}

// ─── Read path ───────────────────────────────────────────────────────────────

// GetWABenchRunTrace resolves a WABench run's outputs to their enhanced agent
// traces and returns a per-output analysis list plus a run-level summary.
// It parses the heavy per-call arrays (needed for step breakdown / timeline),
// so callers use it when a run's trace is actually opened, not in list views.
func (r *EvaluationRepo) GetWABenchRunTrace(ctx context.Context, runID string) (*RunTraceSummary, []*EvaluationTraceAnalysis, error) {
	if r.db == nil {
		return nil, nil, fmt.Errorf("database not available")
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT wo.output_id,
		       COALESCE(wo.trace_ref, ''),
		       COALESCE(at.llm_calls, '[]'::jsonb),
		       COALESCE(at.tool_calls, '[]'::jsonb),
		       COALESCE(at.pipeline_metadata, '{}'::jsonb),
		       at.estimated_cost,
		       at.created_at,
		       COALESCE(at.model, ''),
		       COALESCE(at.agent_mode, ''),
		       at.first_token_ms,
		       at.output_tokens_per_sec,
		       at.cost_input, at.cost_output, at.cost_cache_read, at.cost_cache_write
		FROM wabench_runs wr
		JOIN wabench_outputs wo ON wo.run_pk = wr.id
		JOIN agent_traces at ON at.trace_id = wo.trace_ref
		WHERE wr.run_id = $1
		ORDER BY wo.created_at
	`, runID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query run trace: %w", err)
	}
	defer rows.Close()

	var analyses []*EvaluationTraceAnalysis
	for rows.Next() {
		var (
			outputID, traceRef string
			llmRaw, toolRaw    []byte
			pipelineRaw        []byte
			estimatedCost      *float64
			createdAt          time.Time
			model, agentMode   string
			firstTokenMs       *int64
			throughput         *float64
			costIn, costOut    *float64
			costCR, costCW     *float64
		)
		if err := rows.Scan(
			&outputID, &traceRef, &llmRaw, &toolRaw, &pipelineRaw,
			&estimatedCost, &createdAt,
			&model, &agentMode, &firstTokenMs, &throughput,
			&costIn, &costOut, &costCR, &costCW,
		); err != nil {
			return nil, nil, err
		}

		var llmCalls []engine.LLMCallRecord
		_ = json.Unmarshal(llmRaw, &llmCalls)
		var toolCalls []engine.ToolCallRecord
		_ = json.Unmarshal(toolRaw, &toolCalls)
		var pipeline *engine.PipelineMetadata
		if len(pipelineRaw) > 0 && string(pipelineRaw) != "{}" {
			pipeline = &engine.PipelineMetadata{}
			_ = json.Unmarshal(pipelineRaw, pipeline)
		}

		analyses = append(analyses, buildTraceAnalysis(
			runID, outputID, traceRef, llmCalls, toolCalls, pipeline,
			estimatedCost, model, agentMode, firstTokenMs, throughput,
			costIn, costOut, costCR, costCW, createdAt,
		))
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	if len(analyses) == 0 {
		return nil, nil, fmt.Errorf("no enhanced trace data available for run %s", runID)
	}

	return summarizeRun(analyses), analyses, nil
}

// GetWABenchRunSummary computes a run's headline trace summary directly from the
// flat summary columns (SQL aggregate). No JSONB arrays are parsed, so this is
// the cheap path used by A/B compare and dashboards.
func (r *EvaluationRepo) GetWABenchRunSummary(ctx context.Context, runID string) (*RunTraceSummary, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	s := &RunTraceSummary{}
	var (
		avgLLMCalls   *float64
		avgTokens     *float64
		avgLatency    *float64
		avgTTFT       *float64
		avgThroughput *float64
		avgCost       *float64
		totalCost     *float64
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			AVG(at.llm_call_count),
			AVG(at.total_tokens),
			AVG(at.duration_ms),
			AVG(at.first_token_ms),
			AVG(at.output_tokens_per_sec),
			AVG(at.estimated_cost),
			SUM(at.estimated_cost)
		FROM wabench_runs wr
		JOIN wabench_outputs wo ON wo.run_pk = wr.id
		JOIN agent_traces at ON at.trace_id = wo.trace_ref
		WHERE wr.run_id = $1
	`, runID).Scan(
		&s.SamplesAnalyzed, &avgLLMCalls, &avgTokens, &avgLatency,
		&avgTTFT, &avgThroughput, &avgCost, &totalCost,
	)
	if err != nil {
		return nil, err
	}
	if s.SamplesAnalyzed == 0 {
		return nil, fmt.Errorf("no enhanced trace data available for run %s", runID)
	}
	if avgLLMCalls != nil {
		s.AvgLLMCalls = *avgLLMCalls
	}
	if avgTokens != nil {
		s.AvgTokens = int(*avgTokens)
	}
	if avgLatency != nil {
		s.AvgTotalLatencyMs = *avgLatency
	}
	if avgCost != nil {
		s.AvgCostPerSample = *avgCost
	}
	if totalCost != nil {
		s.TotalCost = *totalCost
	}
	if avgTTFT != nil {
		v := *avgTTFT
		s.AvgTTFTMs = &v
	}
	if avgThroughput != nil {
		v := *avgThroughput
		s.AvgOutputTokensPerSec = &v
	}
	return s, nil
}

// CompareWABenchTraces compares two runs' trace summaries via the cheap SQL path.
func (r *EvaluationRepo) CompareWABenchTraces(ctx context.Context, baselineRunID, candidateRunID string) (*TraceComparison, error) {
	baseline, err := r.GetWABenchRunSummary(ctx, baselineRunID)
	if err != nil {
		return nil, fmt.Errorf("baseline: %w", err)
	}
	candidate, err := r.GetWABenchRunSummary(ctx, candidateRunID)
	if err != nil {
		return nil, fmt.Errorf("candidate: %w", err)
	}

	deltas := TraceDeltas{
		LLMCallDelta:   candidate.AvgLLMCalls - baseline.AvgLLMCalls,
		TokenDelta:     candidate.AvgTokens - baseline.AvgTokens,
		LatencyDeltaMs: candidate.AvgTotalLatencyMs - baseline.AvgTotalLatencyMs,
		CostDelta:      candidate.AvgCostPerSample - baseline.AvgCostPerSample,
	}
	if baseline.AvgTTFTMs != nil && candidate.AvgTTFTMs != nil {
		deltas.TTFTDeltaMs = *candidate.AvgTTFTMs - *baseline.AvgTTFTMs
	}

	return &TraceComparison{
		BaselineRunID:  baselineRunID,
		CandidateRunID: candidateRunID,
		Baseline:       *baseline,
		Candidate:      *candidate,
		Deltas:         deltas,
	}, nil
}

// ─── Build / aggregate helpers ───────────────────────────────────────────────

func buildTraceAnalysis(
	runID, outputID, traceRef string,
	llmCalls []engine.LLMCallRecord,
	toolCalls []engine.ToolCallRecord,
	pipeline *engine.PipelineMetadata,
	estimatedCost *float64,
	model, agentMode string,
	firstTokenMs *int64,
	throughput *float64,
	costIn, costOut, costCR, costCW *float64,
	createdAt time.Time,
) *EvaluationTraceAnalysis {
	a := &EvaluationTraceAnalysis{
		ID:            "trace:" + runID + ":" + outputID,
		SampleID:      outputID,
		RunID:         runID,
		TraceID:       traceRef,
		StepBreakdown: map[string]StepBreakdown{},
		Model:         model,
		TTFTMs:        firstTokenMs,
		OutputTokensPerSec: throughput,
		CreatedAt:     createdAt,
	}

	// Per-call breakdown from the heavy arrays (opened view).
	var ttftFromArrays *int64
	for _, call := range llmCalls {
		a.TotalLLMCalls++
		a.TotalPromptTokens += call.PromptTokens
		a.TotalCompletionTokens += call.CompletionTokens
		a.TotalReasoningTokens += call.ReasoningTokens
		a.TotalTokens += call.TotalTokens
		a.TotalLLMLatencyMs += call.LatencyMs
		if ttftFromArrays == nil && call.TTFT != nil {
			v := call.TTFT.Milliseconds()
			ttftFromArrays = &v
		}
		if a.Model == "" {
			a.Model = call.Model
		}
		step := a.StepBreakdown[string(call.Step)]
		step.LLMCalls++
		step.Tokens += call.TotalTokens
		step.LatencyMs += call.LatencyMs
		a.StepBreakdown[string(call.Step)] = step
	}
	if a.TotalLLMCalls > 0 {
		a.AvgLLMLatencyMs = a.TotalLLMLatencyMs / int64(a.TotalLLMCalls)
	}
	if a.TTFTMs == nil {
		a.TTFTMs = ttftFromArrays
	}

	for _, call := range toolCalls {
		a.TotalToolCalls++
		a.TotalToolLatencyMs += call.DurationMs
		step := a.StepBreakdown[string(call.Step)]
		step.ToolCalls++
		step.ToolLatencyMs += call.DurationMs
		a.StepBreakdown[string(call.Step)] = step
	}
	if a.TotalToolCalls > 0 {
		a.AvgToolLatencyMs = a.TotalToolLatencyMs / int64(a.TotalToolCalls)
	}

	// Cost: prefer the persisted breakdown (cheap summary); fall back to a
	// token-based estimate for rows written before migration 120.
	if costIn != nil {
		a.Cost.Input = *costIn
	}
	if costOut != nil {
		a.Cost.Output = *costOut
	}
	if costCR != nil {
		a.Cost.CacheRead = *costCR
	}
	if costCW != nil {
		a.Cost.CacheWrite = *costCW
	}
	a.Cost.Total = a.Cost.Input + a.Cost.Output + a.Cost.CacheRead + a.Cost.CacheWrite

	if estimatedCost != nil {
		a.EstimatedCost = *estimatedCost
	} else if a.Cost.Total > 0 {
		a.EstimatedCost = a.Cost.Total
	} else {
		a.EstimatedCost = estimateCostFromTokens(a.TotalPromptTokens, a.TotalCompletionTokens)
		a.Cost.Total = a.EstimatedCost
	}

	a.AgentMode = agentMode
	if pipeline != nil {
		if a.AgentMode == "" {
			a.AgentMode = pipeline.AgentMode
		}
		a.ExecutionPlan = stepsToStrings(pipeline.ExecutionPlan)
		a.SkippedSteps = stepsToStrings(pipeline.SkippedSteps)
	}

	return a
}

func summarizeRun(analyses []*EvaluationTraceAnalysis) *RunTraceSummary {
	s := &RunTraceSummary{SamplesAnalyzed: len(analyses)}
	if len(analyses) == 0 {
		return s
	}

	var ttftSum int64
	var ttftCount int
	var throughputSum float64
	var throughputCount int
	for _, a := range analyses {
		s.TotalCost += a.EstimatedCost
		s.AvgTotalLatencyMs += float64(a.TotalLLMLatencyMs + a.TotalToolLatencyMs)
		if a.TTFTMs != nil {
			ttftSum += *a.TTFTMs
			ttftCount++
		}
		if a.OutputTokensPerSec != nil {
			throughputSum += *a.OutputTokensPerSec
			throughputCount++
		}
	}
	n := float64(len(analyses))
	s.AvgLLMCalls = sumLLMCalls(analyses) / n
	s.AvgTokens = int(sumTokens(analyses) / n)
	s.AvgTotalLatencyMs = s.AvgTotalLatencyMs / n
	s.AvgCostPerSample = s.TotalCost / n
	if ttftCount > 0 {
		v := float64(ttftSum) / float64(ttftCount)
		s.AvgTTFTMs = &v
	}
	if throughputCount > 0 {
		v := throughputSum / float64(throughputCount)
		s.AvgOutputTokensPerSec = &v
	}
	return s
}

func sumLLMCalls(analyses []*EvaluationTraceAnalysis) float64 {
	var t int
	for _, a := range analyses {
		t += a.TotalLLMCalls
	}
	return float64(t)
}

func sumTokens(analyses []*EvaluationTraceAnalysis) float64 {
	var t int
	for _, a := range analyses {
		t += a.TotalTokens
	}
	return float64(t)
}

func stepsToStrings(steps []engine.StepName) []string {
	out := make([]string, 0, len(steps))
	for _, s := range steps {
		out = append(out, string(s))
	}
	return out
}

// estimateCostFromTokens mirrors engine.ExecutionContext.GetCostEstimate's
// simplified DeepSeek pricing, used only for pre-120 rows with no stored cost.
func estimateCostFromTokens(promptTokens, completionTokens int) float64 {
	const (
		inputCostPer1M  = 0.27
		outputCostPer1M = 1.10
	)
	return float64(promptTokens)/1_000_000*inputCostPer1M +
		float64(completionTokens)/1_000_000*outputCostPer1M
}
