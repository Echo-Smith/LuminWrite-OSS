// Package contextcompiler compiles a node's execution context
// deterministically: the same inputs with the same compiler version always
// produce a byte-identical envelope (docs/18 §18.5). The compiler is a pure
// function over caller-provided data — it never queries a store or service,
// so replaying evidence does not depend on live state.
//
// M5 Context Runtime (docs/18 §18.5.6): the line-count accounting of M3 is
// replaced by a segmentation-aware token estimator, envelopes expose their
// pressure against the budget (0.70 warn / 0.85 compress), and overflow
// retention walks blocks in priority order — each block keeps up to its
// share plus whatever budget higher-ranked blocks left unused, so lower
// priority content is the first to shrink and the first to vanish. The
// compiler stays a pure function — pressure is reported, never acted on
// here; the runtime owns compaction decisions and their cooldown state
// (writingruntime), because those depend on wall-clock and concurrency.
package contextcompiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// ErrResidentOverflow marks the fail-closed resident-layer overflow. The
// runtime inspects it to name the recovery path (canon bloat is an
// operations problem, not a trimming problem), and the message keeps the
// "fails closed" phrasing the M3 evidence tooling greps for.
var ErrResidentOverflow = errors.New("canon is bloated, compilation fails closed")

// CompilerVersion pins the block set, budget table, assembly order, retention
// semantics, and token estimator. Changing any of them changes which content
// survives trimming — i.e. what the model sees — so it must be treated like a
// policy change: new version, never an in-place edit. Version 2 replaces the
// M3 one-token-per-line approximation with the segmentation-aware estimator
// below and adds priority-order retention (docs/18 §18.11 record: real token
// accounting was deferred to M5).
const CompilerVersion = 2

// DefaultTokenBudget is the conservative total budget applied when the
// caller does not override it. Deliberately not NarraCat's 12k: LuminBuddy's
// per-node envelopes are smaller and the value must stay a configuration
// decision, not a constant baked into behavior.
const DefaultTokenBudget = 6000

// ResidentBudget is the dedicated budget of the resident layer
// (through_line_anchor). The resident layer itself is never trimmed and
// never participates in the retention walk; if its content alone exceeds
// this budget, compilation fails closed rather than silently truncating
// canon (docs/18 §18.5.3). Its unused room is not locked away either: it
// feeds the retention pool below, so a small resident layer does not starve
// the share blocks of budget they can actually use.
const ResidentBudget = 1200

// ResidentBlock is the one block that is never trimmed. Everything else is
// trimmed in reverse retention order when the budget binds.
const ResidentBlock = "through_line_anchor"

// Pressure thresholds (docs/18 §18.5.6, LucidWrite constants). At Warn the
// envelope is nearly full — the runtime raises an out-of-band alert. At
// Compress the runtime may pre-compress (recompile against a smaller budget)
// under its own cooldown and in-flight guards.
const (
	PressureWarnThreshold     = 0.70
	PressureCompressThreshold = 0.85
)

// Pressure is the envelope's context pressure: total accounted tokens over
// the budget the envelope was compiled against. The thresholds are exported
// so the runtime and evidence tooling classify envelopes identically to the
// compiler.
func (envelope Envelope) Pressure() float64 {
	if envelope.tokenBudget <= 0 {
		return 0
	}
	return float64(envelope.TotalTokens) / float64(envelope.tokenBudget)
}

// blockOrder is the assembly order. It doubles as the default retention
// order: earlier blocks are more load-bearing and survive longer when the
// budget binds (docs/18 §18.5.6 retention sequence, projected onto the
// LuminBuddy block set — contract_digest first, document_state and style
// directives last).
var blockOrder = []string{
	"contract_digest",
	ResidentBlock,
	"canon_facts",
	"terminology",
	"open_decisions",
	"entities_cards",
	"source_evidence",
	"document_state",
	"style_directives",
}

// Blocks returns the compiler's block names in assembly order.
func Blocks() []string {
	return append([]string(nil), blockOrder...)
}

// ValidBlock reports whether name is a compiler block.
func ValidBlock(name string) bool {
	for _, block := range blockOrder {
		if block == name {
			return true
		}
	}
	return false
}

// BudgetTable maps block names to their share of the total budget. The
// compiler derives per-block limits from the requested total so operators can
// scale envelopes without touching code.
type BudgetTable struct {
	Total int            `json:"total"`
	Share map[string]int `json:"share"`
}

func defaultBudgetTable(total int) BudgetTable {
	// Relative weights of the blocks; resident gets its own protected slice.
	weights := map[string]int{
		"contract_digest":  12,
		ResidentBlock:      0, // fixed ResidentBudget, excluded from the share math
		"canon_facts":      22,
		"terminology":      10,
		"open_decisions":   10,
		"entities_cards":   12,
		"source_evidence":  14,
		"document_state":   12,
		"style_directives": 8,
	}
	share := map[string]int{}
	sum := 0
	for _, block := range blockOrder {
		if block == ResidentBlock {
			continue
		}
		sum += weights[block]
	}
	if sum <= 0 {
		sum = 1
	}
	remaining := total - ResidentBudget
	if remaining < len(blockOrder) {
		remaining = len(blockOrder)
	}
	for _, block := range blockOrder {
		if block == ResidentBlock {
			continue
		}
		share[block] = remaining * weights[block] / sum
	}
	return BudgetTable{Total: total, Share: share}
}

// Block is one compiled context section. Body carries the rendered content;
// Tokens is the accounting value the compiler used against the budget.
type Block struct {
	Name   string `json:"name"`
	Body   string `json:"body"`
	Tokens int    `json:"tokens"`
}

// Missing records a block the node asked for (it contributed input data
// expectations) but the compiler could not supply, with the reason. Absence
// must be explicit — silence makes the model guess.
type Missing struct {
	Block  string `json:"block"`
	Reason string `json:"reason"`
}

// Trimmed records a block that overflowed the budget and how much survived.
// KeptTok 0 means the block lost the retention contest entirely and never
// reached the model.
type Trimmed struct {
	Block       string `json:"block"`
	OriginalTok int    `json:"original_tokens"`
	KeptTok     int    `json:"kept_tokens"`
}

// Diagnostic is an out-of-band compiler observation (engineering channel, per
// docs/18 §18.5.4): it never enters the envelope payload the model sees.
// Scopes: "budget" for trimming, "pressure" for threshold crossings.
type Diagnostic struct {
	Scope   string `json:"scope"`
	Message string `json:"message"`
}

// Envelope is the compiled context: the model-visible blocks plus the
// metadata that makes the compilation auditable and replayable.
type Envelope struct {
	CompilerVersion int          `json:"compiler_version"`
	Blocks          []Block      `json:"blocks"`
	Missing         []Missing    `json:"missing,omitempty"`
	Trimmed         []Trimmed    `json:"trimmed,omitempty"`
	TotalTokens     int          `json:"total_tokens"`
	Hash            string       `json:"hash"`
	tokenBudget     int          `json:"-"`
	diagnostics     []Diagnostic `json:"-"`
}

// TokenBudget exposes the total budget the envelope was compiled against.
func (envelope Envelope) TokenBudget() int { return envelope.tokenBudget }

// Diagnostics returns the out-of-band observations; they are deliberately not
// part of the serialized payload.
func (envelope Envelope) Diagnostics() []Diagnostic { return envelope.diagnostics }

// Render serializes the model-visible payload: blocks only, in compiled
// order, with no metadata. The envelope hash is taken over exactly these
// bytes — metadata changes must not change what the model sees.
func (envelope Envelope) Render() (string, error) {
	serialized, err := json.Marshal(envelope.Blocks)
	if err != nil {
		return "", err
	}
	return string(serialized), nil
}

// Input carries everything one compilation needs, preloaded by the caller.
// Nil slices mean "not supplied": the block becomes an explicit missing entry
// rather than an empty section.
type Input struct {
	// ProjectID scopes canon lookups in evidence review; it never enters the
	// rendered payload.
	ProjectID string
	// ContractDigest is the writing contract summary for this node.
	ContractDigest string
	// Threads are the resident-layer lines (resident ones only should be
	// supplied, but the compiler re-filters defensively).
	ThreadLabels []string
	// FactLines are the canonical facts relevant to this node, pre-rendered
	// by the caller as lines.
	FactLines []string
	// TerminologyLines are glossary directives.
	TerminologyLines []string
	// DecisionLines are settled decisions; QuestionLines are open questions.
	DecisionLines []string
	QuestionLines []string
	// EntityCards are mechanically folded entity summaries.
	EntityCards []string
	// EvidenceLines are provenance-carrying source citations.
	EvidenceLines []string
	// DocumentState is the current document subtree summary.
	DocumentState string
	// StyleDirectives are user-memory style rules (read-only projection).
	StyleDirectives []string
	// TotalBudget overrides DefaultTokenBudget when positive.
	TotalBudget int
	// Wanted lists the blocks the node requires; a wanted block without data
	// is a hard missing entry. A non-empty Wanted is also an allowlist: only
	// declared blocks are assembled, even when data exists for others — this
	// is what makes a capability manifest's context contract binding. All
	// wanted names must be valid compiler blocks.
	Wanted []string
	// RetentionPriority overrides the default (assembly-order) retention
	// rank: earlier names claim leftover budget first when the total binds.
	// Names must be valid compiler blocks; blanks and duplicates are
	// ignored. Unlisted blocks keep their default rank, after the listed
	// ones in assembly order. The resident block is always ranked first —
	// it is never trimmed, so it never loses a contest it does not enter.
	RetentionPriority []string
}

// retentionRanks resolves the effective retention order: the caller's
// RetentionPriority first (in declaration order), then unlisted blocks in
// assembly order, with the resident block always first.
func (input Input) retentionRanks() []string {
	ranked := make([]string, 0, len(blockOrder))
	add := func(name string) {
		for _, existing := range ranked {
			if existing == name {
				return
			}
		}
		ranked = append(ranked, name)
	}
	add(ResidentBlock)
	if input.RetentionPriority != nil {
		for _, name := range input.RetentionPriority {
			if trimmed := strings.TrimSpace(name); trimmed != "" && ValidBlock(trimmed) {
				add(trimmed)
			}
		}
	}
	for _, block := range blockOrder {
		add(block)
	}
	return ranked
}

// tokenCount is the M5 token estimator (docs/18 §18.11 record: the M3
// one-token-per-line approximation is replaced). It is a deterministic,
// segmentation-aware estimator over unicode runes — no model-specific BPE,
// no vocab file, no network: the accounting must be reproducible
// byte-for-byte on any machine for any compiler version.
//
// Segment classes, chosen to track byte-level BPE behaviour on the mixed
// CJK/Latin bodies this compiler actually renders:
//   - CJK ideographs, kana, hangul, and the halfwidth/fullwidth forms block:
//     ~1 token per rune (byte-level BPE rarely merges two of these);
//   - other scripts (Latin, Cyrillic, digits): ~runes/4 per
//     whitespace-delimited word, approximating BPE's common merges;
//   - other runes (punctuation, symbols, standalone spaces): 1 token each.
//
// The estimator errs high on CJK against CJK-heavy provider tokenizers
// (Qwen/DeepSeek) and on Latin against merged BPE runs — deliberately:
// budgets are safety limits, and under-counting is the unrecoverable
// direction.
func tokenCount(body string) int {
	if body == "" {
		return 0
	}
	tokens := 0
	wordRunes := 0
	flushWord := func() {
		if wordRunes > 0 {
			tokens += (wordRunes + 3) / 4
			wordRunes = 0
		}
	}
	for _, r := range body {
		switch {
		case r == '\n':
			flushWord()
			tokens++
		case isCJK(r):
			flushWord()
			tokens++
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			wordRunes++
		case unicode.IsSpace(r):
			flushWord()
			tokens++
		default:
			flushWord()
			tokens++
		}
	}
	flushWord()
	return tokens
}

// isCJK reports whether the rune belongs to a script that byte-level BPE
// tokenizers emit at roughly one token per rune. The halfwidth/fullwidth
// forms block (U+FF00–FFEF) is folded in: fullwidth CJK punctuation counts
// the same either way, and fullwidth letters/digits cost close to one token
// each, so classifying the whole block as CJK errs high.
func isCJK(r rune) bool {
	if r >= 0xFF00 && r <= 0xFFEF {
		return true
	}
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

// compileBlock renders one block from its lines. Empty input yields an empty
// body; the caller decides whether that means missing.
func compileBlock(lines []string) string {
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			kept = append(kept, strings.TrimRight(line, "\n"))
		}
	}
	return strings.Join(kept, "\n")
}

// truncateToTokens hard-truncates a block body to at most tokens accounting
// units, cutting on line boundaries: a partially kept line would make the
// rendered payload disagree with the reported token count. It returns the
// trimmed body and its exact tokenCount.
func truncateToTokens(body string, tokens int) (string, int) {
	if tokens <= 0 {
		return "", 0
	}
	lines := strings.Split(body, "\n")
	used := 0
	kept := 0
	for i, line := range lines {
		cost := tokenCount(line)
		if i > 0 {
			cost++ // the newline separator between kept lines
		}
		if used+cost > tokens {
			break
		}
		used += cost
		kept = i + 1
	}
	if kept == 0 {
		return "", 0
	}
	if kept == len(lines) {
		return body, tokenCount(body)
	}
	trimmed := strings.Join(lines[:kept], "\n")
	return trimmed, tokenCount(trimmed)
}

// Compile assembles the envelope. Determinism contract: no wall-clock, no
// map iteration order, no external state — only Input and CompilerVersion.
// Resident overflow fails closed (an error, not a trimmed block).
func Compile(input Input) (Envelope, error) {
	total := input.TotalBudget
	if total <= 0 {
		total = DefaultTokenBudget
	}
	if total <= ResidentBudget {
		return Envelope{}, fmt.Errorf("total budget %d leaves no room beside the resident budget %d", total, ResidentBudget)
	}
	table := defaultBudgetTable(total)
	wanted := map[string]bool{}
	allowlist := len(input.Wanted) > 0
	for _, block := range input.Wanted {
		name := strings.TrimSpace(block)
		if !ValidBlock(name) {
			return Envelope{}, fmt.Errorf("block %q is not a compiler block", block)
		}
		wanted[name] = true
	}
	for _, name := range input.RetentionPriority {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue // blanks are ignored by the rank resolution, not rejected
		}
		if !ValidBlock(trimmed) {
			return Envelope{}, fmt.Errorf("retention priority %q is not a compiler block", name)
		}
	}

	threads := make([]string, 0, len(input.ThreadLabels))
	for _, label := range input.ThreadLabels {
		if strings.TrimSpace(label) != "" {
			threads = append(threads, label)
		}
	}
	sort.Strings(threads)

	bodies := map[string]string{
		"contract_digest":  strings.TrimSpace(input.ContractDigest),
		ResidentBlock:      compileBlock(threads),
		"canon_facts":      compileBlock(input.FactLines),
		"terminology":      compileBlock(input.TerminologyLines),
		"open_decisions":   compileBlock(append(append([]string{}, input.DecisionLines...), input.QuestionLines...)),
		"entities_cards":   compileBlock(input.EntityCards),
		"source_evidence":  compileBlock(input.EvidenceLines),
		"document_state":   strings.TrimSpace(input.DocumentState),
		"style_directives": compileBlock(input.StyleDirectives),
	}

	envelope := Envelope{CompilerVersion: CompilerVersion, tokenBudget: total}
	diagnostics := []Diagnostic{}

	// Resident layer first, fail-closed on overflow. Assembly only covers
	// allowlisted blocks when a manifest contract narrows the envelope.
	residentTokens := 0
	if residentBody := bodies[ResidentBlock]; residentBody != "" {
		if !allowlist || wanted[ResidentBlock] {
			residentTokens = tokenCount(residentBody)
			if residentTokens > ResidentBudget {
				return Envelope{}, fmt.Errorf("resident layer needs %d tokens over the %d budget: %w", residentTokens, ResidentBudget, ErrResidentOverflow)
			}
		}
	} else if wanted[ResidentBlock] {
		envelope.Missing = append(envelope.Missing, Missing{Block: ResidentBlock, Reason: "no resident threads supplied"})
	}

	// Collect the non-resident candidates with their original bodies; the
	// retention walk below decides how much of each survives.
	type candidate struct {
		name   string
		body   string
		tokens int
	}
	candidates := map[string]candidate{}
	shareSum := 0
	for _, block := range blockOrder {
		if block == ResidentBlock || allowlist && !wanted[block] {
			continue
		}
		shareSum += table.Share[block]
		body := bodies[block]
		if body == "" {
			if wanted[block] {
				envelope.Missing = append(envelope.Missing, Missing{Block: block, Reason: "no data supplied"})
			}
			continue
		}
		candidates[block] = candidate{name: block, body: body, tokens: tokenCount(body)}
	}

	// Retention walk (docs/18 §18.5.2 + §18.5.6): blocks are visited in
	// effective retention order. Each keeps up to its share plus whatever
	// budget is still unclaimed — the resident layer's unused protection,
	// the share table's floor slack, and the unused shares of
	// higher-ranked blocks — and the remainder is trimmed on line
	// boundaries, down to nothing for blocks the budget never reaches.
	// Higher priority claims room first, the tail shrinks first, and the
	// claimed total never exceeds the budget:
	//
	//	pool   = total - residentTokens
	//	room_1 = pool - shareSum                (>= 0 by construction)
	//	room_{b+1} = room_b + share_b - used_b  (>= 0: used_b <= share_b + room_b)
	//	Σ used = shareSum + room_1 - room_final <= pool
	//
	// The resident layer itself is excluded from the walk (never trimmed)
	// and its fail-closed overflow above has already guarded its size.
	pool := total - residentTokens
	room := pool - shareSum
	if room < 0 {
		room = 0
	}
	assembled := map[string]Block{}
	for _, name := range input.retentionRanks() {
		entry, ok := candidates[name]
		if !ok {
			continue
		}
		ceiling := table.Share[name] + room
		if entry.tokens <= ceiling {
			assembled[name] = Block{Name: name, Body: entry.body, Tokens: entry.tokens}
			room += table.Share[name] - entry.tokens
			continue
		}
		trimmedBody, kept := truncateToTokens(entry.body, ceiling)
		if kept > 0 {
			envelope.Trimmed = append(envelope.Trimmed, Trimmed{Block: name, OriginalTok: entry.tokens, KeptTok: kept})
			diagnostics = append(diagnostics, Diagnostic{Scope: "budget", Message: fmt.Sprintf("%s trimmed from %d to %d tokens", name, entry.tokens, kept)})
			assembled[name] = Block{Name: name, Body: trimmedBody, Tokens: kept}
		} else if wanted[name] {
			envelope.Missing = append(envelope.Missing, Missing{Block: name, Reason: "dropped over budget"})
		}
		room += table.Share[name] - kept
	}
	// Emit in assembly order for a stable render; the resident block closes
	// the payload as in every earlier compiler version.
	for _, block := range blockOrder {
		if assembledBlock, ok := assembled[block]; ok {
			envelope.Blocks = append(envelope.Blocks, assembledBlock)
		}
	}
	if residentTokens > 0 {
		envelope.Blocks = append(envelope.Blocks, Block{Name: ResidentBlock, Body: bodies[ResidentBlock], Tokens: residentTokens})
	}

	for _, block := range envelope.Blocks {
		envelope.TotalTokens += block.Tokens
	}

	// Pressure diagnostics (docs/18 §18.5.6): threshold crossings are
	// engineering observations and stay out of the payload. The runtime
	// acts on them; the compiler only reports.
	if pressure := envelope.Pressure(); pressure >= PressureWarnThreshold {
		level := "warn"
		if pressure >= PressureCompressThreshold {
			level = "compress"
		}
		diagnostics = append(diagnostics, Diagnostic{Scope: "pressure", Message: fmt.Sprintf("%s: %.2f (%d/%d tokens)", level, pressure, envelope.TotalTokens, total)})
	}

	rendered, err := envelope.Render()
	if err != nil {
		return Envelope{}, err
	}
	sum := sha256.Sum256([]byte(rendered))
	envelope.Hash = "sha256:" + hex.EncodeToString(sum[:])
	envelope.diagnostics = diagnostics
	return envelope, nil
}
