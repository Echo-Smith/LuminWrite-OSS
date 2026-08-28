package writingstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

const QualityCandidateDraft = string(writingkernel.QualityStateCandidateDraft)

type DocumentRecord struct {
	DocumentID       string         `json:"document_id"`
	OwnerUserID      string         `json:"owner_user_id"`
	Title            string         `json:"title"`
	Status           string         `json:"status"`
	CurrentVersion   int            `json:"current_version"`
	CurrentVersionID string         `json:"current_version_id,omitempty"`
	Metadata         map[string]any `json:"metadata"`
	Actor            Actor          `json:"actor"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

func (s *Store) CreateDocument(ctx context.Context, record DocumentRecord) error {
	if err := validateID(record.DocumentID, "doc_", "document_id"); err != nil {
		return err
	}
	if record.OwnerUserID == "" {
		return fmt.Errorf("%w: owner_user_id is required", ErrInvalidRecord)
	}
	if err := record.Actor.Validate(); err != nil {
		return err
	}
	if record.Status == "" {
		record.Status = "active"
	}
	if record.Metadata == nil {
		record.Metadata = map[string]any{}
	}
	metadata, err := marshalJSON(record.Metadata, "document metadata")
	if err != nil {
		return err
	}
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO writing_documents (
			document_id, owner_user_id, title, status, metadata,
			created_by_type, created_by_id, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)
		ON CONFLICT (document_id) DO NOTHING
	`, record.DocumentID, record.OwnerUserID, record.Title, record.Status, metadata,
		string(record.Actor.Type), nullString(record.Actor.ID), createdAt)
	if err != nil {
		return fmt.Errorf("create writing document: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect document insert: %w", err)
	}
	if inserted == 1 {
		return nil
	}
	var owner, title string
	if err := s.db.QueryRowContext(ctx, `SELECT owner_user_id, title FROM writing_documents WHERE document_id=$1`, record.DocumentID).Scan(&owner, &title); err != nil {
		return fmt.Errorf("load conflicting document: %w", err)
	}
	if owner == record.OwnerUserID && title == record.Title {
		return nil
	}
	return fmt.Errorf("%w: document %s already exists", ErrImmutableConflict, record.DocumentID)
}

type CommitDocumentVersionParams struct {
	Version               writingkernel.DocumentVersion
	ExpectedBaseVersionID string
	ContractID            string
	ContractVersion       int
	Trace                 TraceContext
	CreatedAt             time.Time
}

type StoredDocumentVersion struct {
	Version         writingkernel.DocumentVersion `json:"document"`
	Sequence        int                           `json:"sequence"`
	ContractID      string                        `json:"contract_id"`
	ContractVersion int                           `json:"contract_version"`
	QualityState    string                        `json:"quality_state"`
	CreatedAt       time.Time                     `json:"created_at"`
	Trace           TraceContext                  `json:"trace"`
}

func (s *Store) CommitDocumentVersion(ctx context.Context, params CommitDocumentVersionParams) (StoredDocumentVersion, error) {
	var stored StoredDocumentVersion
	err := s.InTransaction(ctx, func(tx *Tx) error {
		var err error
		stored, err = tx.CommitDocumentVersion(ctx, params)
		return err
	})
	return stored, err
}

func (tx *Tx) CommitDocumentVersion(ctx context.Context, params CommitDocumentVersionParams) (StoredDocumentVersion, error) {
	if err := params.Version.Validate(); err != nil {
		return StoredDocumentVersion{}, fmt.Errorf("%w: document version: %v", ErrInvalidRecord, err)
	}
	if err := validateID(params.ContractID, "ctr_", "contract_id"); err != nil {
		return StoredDocumentVersion{}, err
	}
	if params.ContractVersion < 1 {
		return StoredDocumentVersion{}, fmt.Errorf("%w: contract_version must be at least 1", ErrInvalidRecord)
	}
	provenance, sources, err := params.Trace.values()
	if err != nil {
		return StoredDocumentVersion{}, err
	}

	var currentVersion int
	var currentVersionID sql.NullString
	err = tx.tx.QueryRowContext(ctx, `
		SELECT current_version, current_version_id
		FROM writing_documents WHERE document_id=$1 FOR UPDATE
	`, params.Version.DocumentID).Scan(&currentVersion, &currentVersionID)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredDocumentVersion{}, ErrNotFound
	}
	if err != nil {
		return StoredDocumentVersion{}, fmt.Errorf("lock writing document: %w", err)
	}
	if currentVersionID.String != params.ExpectedBaseVersionID {
		return StoredDocumentVersion{}, fmt.Errorf("%w: document %s current version is %q, expected %q",
			ErrConflict, params.Version.DocumentID, currentVersionID.String, params.ExpectedBaseVersionID)
	}
	if params.ExpectedBaseVersionID == "" {
		if params.Version.BaseVersionID != nil {
			return StoredDocumentVersion{}, fmt.Errorf("%w: first document version cannot declare a base", ErrInvalidRecord)
		}
	} else if params.Version.BaseVersionID == nil || *params.Version.BaseVersionID != params.ExpectedBaseVersionID {
		return StoredDocumentVersion{}, fmt.Errorf("%w: document base_version_id does not match expected base", ErrConflict)
	}

	documentAST, err := marshalJSON(params.Version, "document AST")
	if err != nil {
		return StoredDocumentVersion{}, err
	}
	createdAt := params.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	sequence := currentVersion + 1
	_, err = tx.tx.ExecContext(ctx, `
		INSERT INTO writing_document_versions (
			version_id, document_id, version, base_version_id, contract_id,
			contract_version, schema_version, content_hash, version_hash,
			document_ast, quality_state, provenance, source_refs,
			created_by_type, created_by_id, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
	`, params.Version.VersionID, params.Version.DocumentID, sequence,
		nullString(params.ExpectedBaseVersionID), params.ContractID, params.ContractVersion,
		params.Version.SchemaVersion, params.Version.ContentHash, params.Version.VersionHash,
		documentAST, QualityCandidateDraft, provenance, sources,
		string(params.Trace.Actor.Type), nullString(params.Trace.Actor.ID), createdAt)
	if err != nil {
		return StoredDocumentVersion{}, fmt.Errorf("insert document version: %w", err)
	}
	result, err := tx.tx.ExecContext(ctx, `
		UPDATE writing_documents
		SET current_version=$1, current_version_id=$2, updated_at=$3
		WHERE document_id=$4 AND current_version=$5
	`, sequence, params.Version.VersionID, createdAt, params.Version.DocumentID, currentVersion)
	if err != nil {
		return StoredDocumentVersion{}, fmt.Errorf("advance document version: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return StoredDocumentVersion{}, fmt.Errorf("inspect document advance: %w", err)
	}
	if updated != 1 {
		return StoredDocumentVersion{}, fmt.Errorf("%w: document advanced concurrently", ErrConflict)
	}
	return StoredDocumentVersion{Version: params.Version, Sequence: sequence,
		ContractID: params.ContractID, ContractVersion: params.ContractVersion,
		QualityState: QualityCandidateDraft, CreatedAt: createdAt, Trace: params.Trace}, nil
}

func (s *Store) GetDocumentVersion(ctx context.Context, documentID, versionID string) (StoredDocumentVersion, error) {
	var stored StoredDocumentVersion
	var documentAST, provenance, sources []byte
	var actorType string
	var actorID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT version, contract_id, contract_version, quality_state, document_ast,
		       provenance, source_refs, created_by_type, created_by_id, created_at
		FROM writing_document_versions WHERE document_id=$1 AND version_id=$2
	`, documentID, versionID).Scan(&stored.Sequence, &stored.ContractID, &stored.ContractVersion,
		&stored.QualityState, &documentAST, &provenance, &sources, &actorType, &actorID, &stored.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredDocumentVersion{}, ErrNotFound
	}
	if err != nil {
		return StoredDocumentVersion{}, fmt.Errorf("get document version: %w", err)
	}
	if err := json.Unmarshal(documentAST, &stored.Version); err != nil {
		return StoredDocumentVersion{}, fmt.Errorf("decode document AST: %w", err)
	}
	if err := json.Unmarshal(provenance, &stored.Trace.Provenance); err != nil {
		return StoredDocumentVersion{}, fmt.Errorf("decode document provenance: %w", err)
	}
	if err := json.Unmarshal(sources, &stored.Trace.SourceRefs); err != nil {
		return StoredDocumentVersion{}, fmt.Errorf("decode document source refs: %w", err)
	}
	stored.Trace.Actor = Actor{Type: ActorType(actorType), ID: actorID.String}
	return stored, nil
}
