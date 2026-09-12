// M1.0 delivery-commit protocol wiring (docs/21 §21.8): the governed runtime
// drives the store's delivery primitives at two capability boundaries —
//
//	core.writing.draft        → commit the full_draft as a candidate
//	                            DocumentVersion (lcp-parsed AST)
//	core.validation.quality   → decode the quality report payload and resolve
//	                            the candidate version; the orchestrator then
//	                            saves the run checkpoint WITH the quality
//	                            report + document promotion, so the delivery
//	                            snapshot carries the quality-state transition
//	                            in the same transaction
//
// The orchestrator invokes the methods after it has persisted a successful
// node's artifacts. A nil DeliveryProtocol disables the protocol entirely
// (runtime mode off / harness keeps its current behavior). Failures surface
// as node errors: a gate violation is an evidence gap, so the node fails
// closed with the protocol's error in the attempt ledger.
package writingruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/lcp"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// Delivery protocol capability classes (writingplan manifest classes).
const (
	DeliveryClassDraft   = "writing.draft"
	DeliveryClassQuality = "validation.quality"
	// DeliveryClassResearchDraft routes the research draft node (class
	// research.draft, T06) through the same candidate-version commit: the
	// full_draft artifact carries [@ev_] markers, but the document lineage
	// the quality gate consumes is identical to the ordinary draft's.
	DeliveryClassResearchDraft = "research.draft"
)

// DeliveryProtocol implements the delivery commits for draft/quality nodes.
type DeliveryProtocol struct {
	Store   *writingstore.Store
	Content ContentGateway
	// Actor records the protocol's store writes; kernel delivery is a system
	// behavior (docs/21 §21.7 D1). Zero falls back to ActorSystem.
	Actor writingstore.Actor
	// Now overrides the wall clock in tests. Zero means time.Now.
	Now func() time.Time
}

// CommitDraftCandidate (M1.0a) persists the draft output as a candidate
// DocumentVersion: lcp-parsed AST, base version from the run. The artifact
// row is already durable (the orchestrator committed it); the version row is
// the delivery lineage the quality report will reference.
func (protocol *DeliveryProtocol) CommitDraftCandidate(ctx context.Context, run writingstore.RuntimeRun, node writingplan.PlanNode, artifacts []writingstore.ArtifactRecord) error {
	draft, ok := findArtifact(artifacts, "full_draft")
	if !ok {
		return fmt.Errorf("%w: draft node produced no full_draft artifact", ErrInvalidExecutionResult)
	}
	body, err := protocol.loadBody(ctx, draft)
	if err != nil {
		return err
	}
	document, err := buildDeliveryDocument(run, node.NodeID, body)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidExecutionResult, err)
	}
	_, err = protocol.Store.CommitDocumentVersion(ctx, writingstore.CommitDocumentVersionParams{
		Version: document, ExpectedBaseVersionID: strings.TrimSpace(run.BaseVersionID),
		ContractID: run.ContractID, ContractVersion: run.ContractVersion,
		Trace: writingstore.TraceContext{Actor: protocol.actor(), Provenance: map[string]any{}, SourceRefs: []string{}},
	})
	return err
}

// QualityDelivery is the quality node's delivery payload: the report record
// (candidate-version bound) and the promotion that advances quality_state.
type QualityDelivery struct {
	Report    writingstore.QualityReportRecord
	Promotion writingstore.DocumentPromotion
}

// BuildQualityDelivery (M1.0b input) decodes the quality node's report
// artifact payload, resolves the run document's candidate version, and
// returns the quality report + promotion for the orchestrator to commit with
// the delivery checkpoint.
func (protocol *DeliveryProtocol) BuildQualityDelivery(ctx context.Context, run writingstore.RuntimeRun, node writingplan.PlanNode, artifacts []writingstore.ArtifactRecord) (QualityDelivery, error) {
	report, ok := findArtifact(artifacts, "quality_report")
	if !ok {
		return QualityDelivery{}, fmt.Errorf("%w: quality node produced no quality_report artifact", ErrInvalidExecutionResult)
	}
	body, err := protocol.loadBody(ctx, report)
	if err != nil {
		return QualityDelivery{}, err
	}
	findings, err := decodeQualityFindings(body)
	if err != nil {
		return QualityDelivery{}, err
	}
	candidateVersionID, err := protocol.Store.CurrentDocumentVersionID(ctx, run.DocumentID)
	if err != nil {
		return QualityDelivery{}, fmt.Errorf("%w: delivery could not resolve the candidate version: %v", ErrInvalidExecutionResult, err)
	}
	now := protocol.clock()
	delivery := QualityDelivery{
		Report: writingstore.QualityReportRecord{
			ReportID: writingstore.StableID("qr_", run.RunID, node.NodeID), ReportVersion: 1,
			RunID: run.RunID, PlanID: run.ActivePlanID, PlanVersion: run.ActivePlanVersion,
			DocumentID: run.DocumentID, CandidateVersionID: candidateVersionID,
			ContentHash:        contentHash(body),
			RequestedAssurance: writingkernel.AssuranceLevelStandard,
			AchievedAssurance:  writingkernel.AssuranceLevelStandard,
			AssuranceSatisfied: true,
			QualityState:       writingstore.QualityAcceptedDraft,
			VersionConsistent:  true,
			BlockerCount:       findings.BlockerCount, ErrorCount: findings.ErrorCount,
			OpenErrorCount: findings.OpenErrorCount, WaivedErrorCount: findings.WaivedCount,
			WarningCount: findings.WarningCount,
			Payload:      findings.Payload,
			Trace:        writingstore.TraceContext{Actor: protocol.actor(), Provenance: map[string]any{}, SourceRefs: []string{}},
			CreatedAt:    now,
		},
		Promotion: writingstore.DocumentPromotion{DocumentID: run.DocumentID,
			VersionID: candidateVersionID, QualityState: writingstore.QualityAcceptedDraft, AcceptedAt: now},
	}
	return delivery, nil
}

// deliveryDriver is one capability class's post-commit delivery action. The
// returned *QualityDelivery is non-nil when the next checkpoint must carry
// the delivery bundle (quality nodes); draft nodes commit inside the driver
// and return nil.
type deliveryDriver func(ctx context.Context, run writingstore.RuntimeRun, node writingplan.PlanNode, artifacts []writingstore.ArtifactRecord) (*QualityDelivery, error)

// deliveryDrivers is the class→driver routing table (V3.0 M2, docs/26): the
// delivery protocol owns its routing so a new delivery-bearing capability
// class registers here — the orchestrator's loop stays capability-agnostic
// and no longer hard-codes draft/quality branches. Kernel note: the drivers
// run store-side delivery commits, which are kernel behaviors (ActorSystem
// discipline, docs/21 §21.7); the table only relocates the switch, it does
// not open store writes to capabilities.
func (protocol *DeliveryProtocol) deliveryDrivers() map[string]deliveryDriver {
	return map[string]deliveryDriver{
		DeliveryClassDraft: func(ctx context.Context, run writingstore.RuntimeRun, node writingplan.PlanNode, artifacts []writingstore.ArtifactRecord) (*QualityDelivery, error) {
			if err := protocol.CommitDraftCandidate(ctx, run, node, artifacts); err != nil {
				return nil, err
			}
			return nil, nil
		},
		DeliveryClassQuality: func(ctx context.Context, run writingstore.RuntimeRun, node writingplan.PlanNode, artifacts []writingstore.ArtifactRecord) (*QualityDelivery, error) {
			delivery, err := protocol.BuildQualityDelivery(ctx, run, node, artifacts)
			if err != nil {
				return nil, err
			}
			return &delivery, nil
		},
		DeliveryClassResearchDraft: func(ctx context.Context, run writingstore.RuntimeRun, node writingplan.PlanNode, artifacts []writingstore.ArtifactRecord) (*QualityDelivery, error) {
			if err := protocol.CommitDraftCandidate(ctx, run, node, artifacts); err != nil {
				return nil, err
			}
			return nil, nil
		},
	}
}

// Drive runs the class's post-commit delivery action and returns the
// delivery bundle for the next checkpoint, or nil when the class commits
// inside the driver (or has no delivery semantics). Unknown classes are a
// no-op: delivery semantics are opt-in per class via the routing table.
func (protocol *DeliveryProtocol) Drive(ctx context.Context, class string, run writingstore.RuntimeRun, node writingplan.PlanNode, artifacts []writingstore.ArtifactRecord) (*QualityDelivery, error) {
	driver, ok := protocol.deliveryDrivers()[class]
	if !ok {
		return nil, nil
	}
	return driver(ctx, run, node, artifacts)
}

func (protocol *DeliveryProtocol) loadBody(ctx context.Context, artifact writingstore.ArtifactRecord) ([]byte, error) {
	if protocol.Content == nil {
		return nil, fmt.Errorf("%w: delivery protocol requires a content gateway", ErrRuntimeNotReady)
	}
	body, err := protocol.Content.Load(ctx, InputArtifact{ArtifactID: artifact.ArtifactID,
		Version: artifact.Version, ArtifactType: writingplan.ArtifactType(artifact.ArtifactType),
		ContentHash: artifact.ContentHash, MediaType: artifact.MediaType, ContentRef: artifact.ContentRef})
	if err != nil {
		return nil, fmt.Errorf("%w: delivery protocol could not load %s bytes: %v", ErrInvalidExecutionResult, artifact.ArtifactType, err)
	}
	return body, nil
}

// qualityFindings is the minimal statistics the delivery gate needs, decoded
// from the quality node's report payload (PostReviewStep shape: scores +
// issues with severities).
type qualityFindings struct {
	BlockerCount   int
	ErrorCount     int
	OpenErrorCount int
	WaivedCount    int
	WarningCount   int
	Payload        map[string]any
}

// decodeQualityFindings maps severity buckets onto the store's finding
// counts: blocker stays blocker, "high" is an open error, everything else is
// a warning. The full payload rides along on the report record.
func decodeQualityFindings(content []byte) (qualityFindings, error) {
	var payload map[string]any
	if err := json.Unmarshal(content, &payload); err != nil {
		return qualityFindings{}, fmt.Errorf("%w: quality report payload is not an object: %v", ErrInvalidExecutionResult, err)
	}
	findings := qualityFindings{Payload: payload}
	issues, _ := payload["issues"].([]any)
	for _, raw := range issues {
		issue, _ := raw.(map[string]any)
		switch severity, _ := issue["severity"].(string); severity {
		case "blocker":
			findings.BlockerCount++
			findings.ErrorCount++
			findings.OpenErrorCount++
		case "high":
			findings.ErrorCount++
			findings.OpenErrorCount++
		default:
			findings.WarningCount++
		}
	}
	return findings, nil
}

func findArtifact(artifacts []writingstore.ArtifactRecord, artifactType string) (writingstore.ArtifactRecord, bool) {
	for _, artifact := range artifacts {
		if artifact.ArtifactType == artifactType {
			return artifact, true
		}
	}
	return writingstore.ArtifactRecord{}, false
}

// buildDeliveryDocument parses a full-draft markdown body into a sealed
// DocumentVersion. Top-level ATX headings split sections (title → section
// title, body → section body); a body without headings becomes one section.
// This mirrors the lcp model where section bodies cannot contain headings.
func buildDeliveryDocument(run writingstore.RuntimeRun, nodeID string, body []byte) (writingkernel.DocumentVersion, error) {
	versionID := deliveryVersionID(run.RunID, nodeID)
	origin := lcp.Origin{Kind: lcp.OriginModel, Ref: "writingruntime.delivery"}
	sections := splitMarkdownSections(body)
	children := make([]*writingkernel.DocumentNode, 0, len(sections))
	for index, section := range sections {
		parsed, err := lcp.ParseSectionBody(string(section.body), lcp.ParseOptions{
			DocumentID: run.DocumentID, VersionID: versionID,
			SectionID: fmt.Sprintf("sec_%d", index+1), SectionTitle: section.title,
			Origin: origin,
		})
		if err != nil {
			return writingkernel.DocumentVersion{}, fmt.Errorf("section %d (%s): %v", index+1, section.title, err)
		}
		// parsed.Root is a document node wrapping one section; lift the
		// section nodes under this version's own document root.
		children = append(children, parsed.Root.Children...)
	}
	if len(children) == 0 {
		return writingkernel.DocumentVersion{}, fmt.Errorf("draft body produced no document content")
	}
	root := &writingkernel.DocumentNode{Type: writingkernel.NodeTypeDocument,
		Attrs: map[string]any{}, Children: children, Origin: origin}
	document := writingkernel.DocumentVersion{
		SchemaVersion: writingkernel.SchemaVersionV1,
		DocumentID:    run.DocumentID, VersionID: versionID,
		BaseVersionID: baseVersionID(run), Root: root,
	}
	return document.WithComputedHashes()
}

// markdownSection is one top-level heading split.
type markdownSection struct {
	title string
	body  string
}

// splitMarkdownSections splits a draft into top-level sections on ATX
// headings. Content before the first heading becomes the intro section.
func splitMarkdownSections(body []byte) []markdownSection {
	lines := strings.Split(string(body), "\n")
	sections := []markdownSection{}
	current := markdownSection{}
	flush := func() {
		if strings.TrimSpace(current.body) != "" || current.title != "" {
			sections = append(sections, current)
		}
	}
	for _, line := range lines {
		if heading, ok := markdownHeading(line); ok {
			flush()
			current = markdownSection{title: heading}
			continue
		}
		current.body += line + "\n"
	}
	flush()
	return sections
}

// markdownHeading returns the heading text when the line is an ATX heading.
func markdownHeading(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 2 || trimmed[0] != '#' {
		return "", false
	}
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level > 6 || level >= len(trimmed) || trimmed[level] != ' ' {
		return "", false
	}
	return strings.TrimSpace(trimmed[level:]), true
}

// baseVersionID returns the run's base version as a pointer for the
// DocumentVersion (nil when the run started from an empty document).
func baseVersionID(run writingstore.RuntimeRun) *string {
	if strings.TrimSpace(run.BaseVersionID) == "" {
		return nil
	}
	base := run.BaseVersionID
	return &base
}

// deliveryVersionID pre-allocates the candidate version id for one draft
// node attempt: stable, so the quality report can reference it later.
func deliveryVersionID(runID, nodeID string) string {
	return writingstore.StableID("ver_", runID, nodeID)
}

func (protocol *DeliveryProtocol) actor() writingstore.Actor {
	if protocol.Actor.Type != "" {
		return protocol.Actor
	}
	return writingstore.Actor{Type: writingstore.ActorSystem, ID: "writingruntime.delivery"}
}

func (protocol *DeliveryProtocol) clock() time.Time {
	if protocol.Now != nil {
		return protocol.Now()
	}
	return time.Now().UTC()
}
