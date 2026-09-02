// Package contextcompiler compiles a node's execution context
// deterministically: the same inputs with the same compiler version always
// produce a byte-identical envelope (docs/18 §18.5). The compiler is a pure
// function over caller-provided data — it never queries a store or service,
// so replaying evidence does not depend on live state.
package contextcompiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// CompilerVersion pins the block set, budget table, and assembly order.
// Changing any of them changes every envelope hash, so it must be treated
// like a policy change: new version, never an in-place edit.
const CompilerVersion = 1

// DefaultTokenBudget is the conservative total budget applied when the
// caller does not override it. Deliberately not NarraCat's 12k: LuminBuddy's
// per-node envelopes are smaller and the value must stay a configuration
// decision, not a constant baked into behavior.
const DefaultTokenBudget = 6000

// ResidentBudget is the dedicated budget of the resident layer
// (through_line_anchor). It is protected from overflow trimming; if the
// resident content alone exceeds it, compilation fails closed rather than
// silently truncating canon.
const ResidentBudget = 1200

// ResidentBlock is the one block that is never trimmed. Everything else is
// dropped tail-first in the declared assembly order.
const ResidentBlock = "through_line_anchor"

// blockOrder is the assembly and overflow order: earlier blocks are more
// load-bearing and survive longer when the total budget binds.
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

// Trimmed records a block that overflowed its budget and how much survived.
type Trimmed struct {
	Block       string `json:"block"`
	OriginalTok int    `json:"original_tokens"`
	KeptTok     int    `json:"kept_tokens"`
}

// Diagnostic is an out-of-band compiler observation (engineering channel, per
// docs/18 §18.5.4): it never enters the envelope payload the model sees.
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
}

// lineCount is the token accounting approximation for MVP: one token per
// line. Real tokenization arrives with the runtime budget work (M5); the
// accounting is deliberately coarse but deterministic.
func lineCount(body string) int {
	if body == "" {
		return 0
	}
	return strings.Count(body, "\n") + 1
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

// truncateToTokens hard-truncates a block body to at most tokens lines.
func truncateToTokens(body string, tokens int) (string, int) {
	if tokens <= 0 {
		return "", 0
	}
	lines := strings.Split(body, "\n")
	if len(lines) <= tokens {
		return body, len(lines)
	}
	return strings.Join(lines[:tokens], "\n"), tokens
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
	if residentBody := bodies[ResidentBlock]; residentBody != "" {
		if !allowlist || wanted[ResidentBlock] {
			if tokens := lineCount(residentBody); tokens > ResidentBudget {
				return Envelope{}, fmt.Errorf("resident layer needs %d tokens over the %d budget: canon is bloated, compilation fails closed", tokens, ResidentBudget)
			}
		}
	} else if wanted[ResidentBlock] {
		envelope.Missing = append(envelope.Missing, Missing{Block: ResidentBlock, Reason: "no resident threads supplied"})
	}

	for _, block := range blockOrder {
		if block == ResidentBlock || allowlist && !wanted[block] {
			continue
		}
		body := bodies[block]
		if body == "" {
			if wanted[block] {
				envelope.Missing = append(envelope.Missing, Missing{Block: block, Reason: "no data supplied"})
			}
			continue
		}
		tokens := lineCount(body)
		limit := table.Share[block]
		if tokens > limit {
			trimmedBody, kept := truncateToTokens(body, limit)
			envelope.Trimmed = append(envelope.Trimmed, Trimmed{Block: block, OriginalTok: tokens, KeptTok: kept})
			diagnostics = append(diagnostics, Diagnostic{Scope: "budget", Message: fmt.Sprintf("%s trimmed from %d to %d tokens", block, tokens, kept)})
			body, tokens = trimmedBody, kept
		}
		if body == "" {
			if wanted[block] {
				envelope.Missing = append(envelope.Missing, Missing{Block: block, Reason: "dropped over budget"})
			}
			continue
		}
		envelope.Blocks = append(envelope.Blocks, Block{Name: block, Body: body, Tokens: tokens})
	}
	if residentBody := bodies[ResidentBlock]; residentBody != "" && (!allowlist || wanted[ResidentBlock]) {
		envelope.Blocks = append(envelope.Blocks, Block{Name: ResidentBlock, Body: residentBody, Tokens: lineCount(residentBody)})
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
