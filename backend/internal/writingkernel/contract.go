package writingkernel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	hashPrefix      = "sha256:"
	SchemaVersionV1 = "lcp/1.0"
)

var (
	contractIDPattern = regexp.MustCompile(`^ctr_[A-Za-z0-9][A-Za-z0-9_-]*$`)
	hashPattern       = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// ContractVersion identifies one immutable WritingContract version.
type ContractVersion struct {
	ContractID   string `json:"contract_id"`
	Version      int    `json:"version"`
	ContractHash string `json:"contract_hash,omitempty"`
}

type WritingContract struct {
	SchemaVersion string `json:"schema_version"`
	ContractVersion
	Status             ContractStatus      `json:"status"`
	Intent             IntentSpec          `json:"intent"`
	Audience           AudienceSpec        `json:"audience"`
	Content            ContentSpec         `json:"content"`
	Voice              VoiceSpec           `json:"voice"`
	MaterialPolicy     MaterialPolicy      `json:"material_policy"`
	EvidencePolicy     EvidencePolicy      `json:"evidence_policy"`
	Delivery           DeliverySpec        `json:"delivery"`
	Collaboration      ExecutionControl    `json:"collaboration"`
	SourceAttributions []SourceAttribution `json:"source_attributions"`
	Inferences         []Inference         `json:"inferences"`
}

type IntentSpec struct {
	Operation Operation `json:"operation"`
	Genre     string    `json:"genre"`
	Purpose   string    `json:"purpose"`
}

type AudienceSpec struct {
	Role           string `json:"role"`
	KnowledgeLevel string `json:"knowledge_level"`
}

type ContentSpec struct {
	Topic            string   `json:"topic"`
	CentralQuestion  string   `json:"central_question"`
	RequiredPoints   []string `json:"required_points"`
	ProhibitedPoints []string `json:"prohibited_points"`
}

type VoiceSpec struct {
	Tone              string `json:"tone"`
	PreserveUserVoice bool   `json:"preserve_user_voice"`
}

type MaterialPolicy struct {
	UserMaterialPriority  UserMaterialPriority `json:"user_material_priority"`
	AllowExternalResearch bool                 `json:"allow_external_research"`
	ConflictHandling      ConflictHandling     `json:"conflict_handling"`
}

type EvidencePolicy struct {
	Level             EvidenceLevel           `json:"level"`
	UnsupportedClaims UnsupportedClaimsPolicy `json:"unsupported_claims"`
}

type LengthRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type DeliverySpec struct {
	Format   DeliveryFormat `json:"format"`
	Language string         `json:"language"`
	Length   LengthRange    `json:"length"`
}

type ExecutionControl struct {
	TaskMode          TaskMode          `json:"task_mode"`
	OrchestrationMode OrchestrationMode `json:"orchestration_mode"`
	AssuranceLevel    AssuranceLevel    `json:"assurance_level"`
	ApprovalMode      ApprovalMode      `json:"approval_mode"`
}

type SourceAttribution struct {
	FieldPath  string            `json:"field_path"`
	Source     AttributionSource `json:"source"`
	ValueHash  string            `json:"value_hash"`
	RecordedAt string            `json:"recorded_at"`
}

type Inference struct {
	FieldPath     string          `json:"field_path"`
	ProposedValue string          `json:"proposed_value"`
	Confidence    float64         `json:"confidence"`
	Status        InferenceStatus `json:"status"`
	ReasonCode    string          `json:"reason_code"`
	Summary       string          `json:"summary"`
}

// ResolvedExecutionControl keeps requested and effective values separate.
// A system recommendation never mutates the immutable WritingContract.
type ResolvedExecutionControl struct {
	Requested ExecutionControl `json:"requested"`
	Effective ExecutionControl `json:"effective"`
	Applied   []string         `json:"applied"`
}

// ExecutionRecommendation is intentionally partial. Empty fields mean that
// the strategy compiler made no recommendation for that dimension.
type ExecutionRecommendation struct {
	TaskMode          TaskMode          `json:"task_mode,omitempty"`
	OrchestrationMode OrchestrationMode `json:"orchestration_mode,omitempty"`
}

func (c WritingContract) Validate() error {
	if c.SchemaVersion != SchemaVersionV1 {
		return fmt.Errorf("schema_version must be %q", SchemaVersionV1)
	}
	if !contractIDPattern.MatchString(c.ContractID) {
		return errors.New("contract_id must use the ctr_ prefix and contain only letters, digits, underscore, or hyphen")
	}
	if c.Version < 1 {
		return errors.New("version must be at least 1")
	}
	if !hashPattern.MatchString(c.ContractHash) {
		return errors.New("contract_hash must be a lowercase sha256 digest")
	}
	if !c.Status.Valid() {
		return fmt.Errorf("invalid contract status %q", c.Status)
	}
	if !c.Intent.Operation.Valid() {
		return fmt.Errorf("invalid operation %q", c.Intent.Operation)
	}
	if err := validateRequiredStrings(map[string]string{
		"intent.genre":             c.Intent.Genre,
		"intent.purpose":           c.Intent.Purpose,
		"audience.role":            c.Audience.Role,
		"audience.knowledge_level": c.Audience.KnowledgeLevel,
		"content.topic":            c.Content.Topic,
		"content.central_question": c.Content.CentralQuestion,
		"voice.tone":               c.Voice.Tone,
		"delivery.language":        c.Delivery.Language,
	}); err != nil {
		return err
	}
	if c.Content.RequiredPoints == nil || c.Content.ProhibitedPoints == nil {
		return errors.New("required_points and prohibited_points must be present")
	}
	if err := validatePointSets(c.Content.RequiredPoints, c.Content.ProhibitedPoints); err != nil {
		return err
	}
	if !c.MaterialPolicy.UserMaterialPriority.Valid() {
		return fmt.Errorf("invalid user_material_priority %q", c.MaterialPolicy.UserMaterialPriority)
	}
	if !c.MaterialPolicy.ConflictHandling.Valid() {
		return fmt.Errorf("invalid conflict_handling %q", c.MaterialPolicy.ConflictHandling)
	}
	if !c.EvidencePolicy.Level.Valid() {
		return fmt.Errorf("invalid evidence level %q", c.EvidencePolicy.Level)
	}
	if !c.EvidencePolicy.UnsupportedClaims.Valid() {
		return fmt.Errorf("invalid unsupported_claims policy %q", c.EvidencePolicy.UnsupportedClaims)
	}
	if !c.Delivery.Format.Valid() {
		return fmt.Errorf("invalid delivery format %q", c.Delivery.Format)
	}
	if c.Delivery.Length.Min < 1 || c.Delivery.Length.Max < 1 || c.Delivery.Length.Min > c.Delivery.Length.Max {
		return errors.New("delivery length must be positive and min must not exceed max")
	}
	if err := validateExecutionControl(c.Collaboration); err != nil {
		return err
	}
	if c.SourceAttributions == nil {
		return errors.New("source_attributions must be present")
	}
	if c.Inferences == nil {
		return errors.New("inferences must be present")
	}
	if err := c.validateSourceAttributions(); err != nil {
		return err
	}
	if err := c.validateInferences(); err != nil {
		return err
	}
	expectedHash, err := c.ComputeHash()
	if err != nil {
		return fmt.Errorf("compute contract hash: %w", err)
	}
	if c.ContractHash != expectedHash {
		return fmt.Errorf("contract_hash mismatch: expected %s", expectedHash)
	}
	return nil
}

func validateRequiredStrings(values map[string]string) error {
	for name, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s must not be blank", name)
		}
	}
	return nil
}

func validatePointSets(required, prohibited []string) error {
	requiredSet := make(map[string]struct{}, len(required))
	for i, point := range required {
		normalized := normalizePoint(point)
		if normalized == "" {
			return fmt.Errorf("required_points[%d] must not be blank", i)
		}
		if _, exists := requiredSet[normalized]; exists {
			return fmt.Errorf("required_points contains duplicate %q", point)
		}
		requiredSet[normalized] = struct{}{}
	}
	prohibitedSet := make(map[string]struct{}, len(prohibited))
	for i, point := range prohibited {
		normalized := normalizePoint(point)
		if normalized == "" {
			return fmt.Errorf("prohibited_points[%d] must not be blank", i)
		}
		if _, exists := prohibitedSet[normalized]; exists {
			return fmt.Errorf("prohibited_points contains duplicate %q", point)
		}
		if _, conflict := requiredSet[normalized]; conflict {
			return fmt.Errorf("point %q is both required and prohibited", point)
		}
		prohibitedSet[normalized] = struct{}{}
	}
	return nil
}

func normalizePoint(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func validateExecutionControl(control ExecutionControl) error {
	if !control.TaskMode.Valid() {
		return fmt.Errorf("invalid task_mode %q", control.TaskMode)
	}
	if !control.OrchestrationMode.Valid() {
		return fmt.Errorf("invalid orchestration_mode %q", control.OrchestrationMode)
	}
	if !control.AssuranceLevel.Valid() {
		return fmt.Errorf("invalid assurance_level %q", control.AssuranceLevel)
	}
	if !control.ApprovalMode.Valid() {
		return fmt.Errorf("invalid approval_mode %q", control.ApprovalMode)
	}
	return nil
}

func (c WritingContract) validateSourceAttributions() error {
	requiredPaths := map[string]bool{
		"/collaboration/task_mode":          false,
		"/collaboration/orchestration_mode": false,
		"/collaboration/assurance_level":    false,
		"/collaboration/approval_mode":      false,
	}
	seen := make(map[string]struct{}, len(c.SourceAttributions))
	for i, attribution := range c.SourceAttributions {
		if attribution.FieldPath == "" {
			return fmt.Errorf("source_attributions[%d].field_path must not be blank", i)
		}
		if attribution.FieldPath == "/contract_hash" || strings.HasPrefix(attribution.FieldPath, "/source_attributions") || strings.HasPrefix(attribution.FieldPath, "/inferences") {
			return fmt.Errorf("source_attributions[%d] targets a non-attributable field", i)
		}
		if _, exists := seen[attribution.FieldPath]; exists {
			return fmt.Errorf("duplicate source attribution for %s", attribution.FieldPath)
		}
		seen[attribution.FieldPath] = struct{}{}
		if !attribution.Source.Valid() {
			return fmt.Errorf("source_attributions[%d] has invalid source %q", i, attribution.Source)
		}
		if !hashPattern.MatchString(attribution.ValueHash) {
			return fmt.Errorf("source_attributions[%d].value_hash must be a lowercase sha256 digest", i)
		}
		if _, err := time.Parse(time.RFC3339, attribution.RecordedAt); err != nil {
			return fmt.Errorf("source_attributions[%d].recorded_at must be RFC3339: %w", i, err)
		}
		expected, err := c.FieldValueHash(attribution.FieldPath)
		if err != nil {
			return fmt.Errorf("source_attributions[%d]: %w", i, err)
		}
		if attribution.ValueHash != expected {
			return fmt.Errorf("source_attributions[%d].value_hash mismatch: expected %s", i, expected)
		}
		if _, required := requiredPaths[attribution.FieldPath]; required {
			requiredPaths[attribution.FieldPath] = true
		}
	}
	for fieldPath, present := range requiredPaths {
		if !present {
			return fmt.Errorf("missing source attribution for %s", fieldPath)
		}
	}
	return nil
}

func (c WritingContract) validateInferences() error {
	for i, inference := range c.Inferences {
		if strings.TrimSpace(inference.FieldPath) == "" {
			return fmt.Errorf("inferences[%d].field_path must not be blank", i)
		}
		if _, err := c.valueAtJSONPointer(inference.FieldPath); err != nil {
			return fmt.Errorf("inferences[%d]: %w", i, err)
		}
		if strings.TrimSpace(inference.ProposedValue) == "" {
			return fmt.Errorf("inferences[%d].proposed_value must not be blank", i)
		}
		if inference.Confidence < 0 || inference.Confidence > 1 {
			return fmt.Errorf("inferences[%d].confidence must be between 0 and 1", i)
		}
		if !inference.Status.Valid() {
			return fmt.Errorf("inferences[%d] has invalid status %q", i, inference.Status)
		}
		if strings.TrimSpace(inference.ReasonCode) == "" || strings.TrimSpace(inference.Summary) == "" {
			return fmt.Errorf("inferences[%d] reason_code and summary must not be blank", i)
		}
		if c.Status == ContractStatusConfirmed && inference.Status == InferenceStatusPendingClarification && inferenceAffectsDirection(inference.FieldPath) {
			return fmt.Errorf("inferences[%d] requires clarification before confirmation", i)
		}
	}
	return nil
}

func inferenceAffectsDirection(fieldPath string) bool {
	return strings.HasPrefix(fieldPath, "/intent/") ||
		strings.HasPrefix(fieldPath, "/audience/") ||
		fieldPath == "/content/topic" ||
		fieldPath == "/content/central_question"
}

// ComputeHash returns the canonical contract hash with contract_hash excluded.
func (c WritingContract) ComputeHash() (string, error) {
	copyContract := c
	copyContract.ContractHash = ""
	payload, err := json.Marshal(copyContract)
	if err != nil {
		return "", err
	}
	return digest(payload), nil
}

// WithComputedHash returns a copy with a canonical contract_hash.
func (c WritingContract) WithComputedHash() (WritingContract, error) {
	hash, err := c.ComputeHash()
	if err != nil {
		return WritingContract{}, err
	}
	c.ContractHash = hash
	return c, nil
}

// FieldValueHash hashes the canonical JSON value at a JSON Pointer.
func (c WritingContract) FieldValueHash(fieldPath string) (string, error) {
	value, err := c.valueAtJSONPointer(fieldPath)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return digest(payload), nil
}

func digest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hashPrefix + hex.EncodeToString(sum[:])
}

func (c WritingContract) valueAtJSONPointer(fieldPath string) (any, error) {
	if !strings.HasPrefix(fieldPath, "/") {
		return nil, errors.New("field_path must be an absolute JSON Pointer")
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	var current any
	if err := json.Unmarshal(payload, &current); err != nil {
		return nil, err
	}
	for _, rawToken := range strings.Split(strings.TrimPrefix(fieldPath, "/"), "/") {
		token, err := decodeJSONPointerToken(rawToken)
		if err != nil {
			return nil, err
		}
		switch typed := current.(type) {
		case map[string]any:
			value, ok := typed[token]
			if !ok {
				return nil, fmt.Errorf("field_path %s does not exist", fieldPath)
			}
			current = value
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, fmt.Errorf("field_path %s has invalid array index", fieldPath)
			}
			current = typed[index]
		default:
			return nil, fmt.Errorf("field_path %s traverses a scalar", fieldPath)
		}
	}
	return current, nil
}

func decodeJSONPointerToken(token string) (string, error) {
	for i := 0; i < len(token); i++ {
		if token[i] == '~' && (i+1 >= len(token) || (token[i+1] != '0' && token[i+1] != '1')) {
			return "", errors.New("field_path contains an invalid JSON Pointer escape")
		}
	}
	return strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~"), nil
}

// ResolveExecutionControl applies recommendations only to explicit auto fields.
// ApprovalModeAuto is an actual approval policy, not a recommendation sentinel.
func ResolveExecutionControl(contract WritingContract, recommendation ExecutionRecommendation) (ResolvedExecutionControl, error) {
	if err := contract.Validate(); err != nil {
		return ResolvedExecutionControl{}, fmt.Errorf("invalid contract: %w", err)
	}
	if recommendation.TaskMode != "" && !recommendation.TaskMode.Valid() {
		return ResolvedExecutionControl{}, fmt.Errorf("invalid recommended task_mode %q", recommendation.TaskMode)
	}
	if recommendation.OrchestrationMode != "" && !recommendation.OrchestrationMode.Valid() {
		return ResolvedExecutionControl{}, fmt.Errorf("invalid recommended orchestration_mode %q", recommendation.OrchestrationMode)
	}
	resolved := ResolvedExecutionControl{
		Requested: contract.Collaboration,
		Effective: contract.Collaboration,
		Applied:   []string{},
	}
	if contract.Collaboration.TaskMode == TaskModeAuto && recommendation.TaskMode != "" && recommendation.TaskMode != TaskModeAuto {
		resolved.Effective.TaskMode = recommendation.TaskMode
		resolved.Applied = append(resolved.Applied, "/collaboration/task_mode")
	}
	if contract.Collaboration.OrchestrationMode == OrchestrationModeAuto && recommendation.OrchestrationMode != "" && recommendation.OrchestrationMode != OrchestrationModeAuto {
		resolved.Effective.OrchestrationMode = recommendation.OrchestrationMode
		resolved.Applied = append(resolved.Applied, "/collaboration/orchestration_mode")
	}
	return resolved, nil
}

func ValidateTransition(previous, next WritingContract) error {
	if err := previous.Validate(); err != nil {
		return fmt.Errorf("previous contract: %w", err)
	}
	if err := next.Validate(); err != nil {
		return fmt.Errorf("next contract: %w", err)
	}
	if previous.ContractID != next.ContractID {
		return errors.New("a contract transition cannot change contract_id")
	}
	if next.Version <= previous.Version {
		return errors.New("a contract transition must increase version")
	}
	return nil
}

// DecodeWritingContractStrict rejects duplicate and unknown JSON fields.
func DecodeWritingContractStrict(data []byte) (WritingContract, error) {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return WritingContract{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var contract WritingContract
	if err := decoder.Decode(&contract); err != nil {
		return WritingContract{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return WritingContract{}, err
	}
	return contract, nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := consumeJSONValue(decoder); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("invalid JSON object")
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("invalid JSON array")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}
