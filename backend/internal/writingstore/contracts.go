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

type ContractRecord struct {
	DocumentID string
	Contract   writingkernel.WritingContract
	Trace      TraceContext
	CreatedAt  time.Time
}

func (s *Store) PutContract(ctx context.Context, record ContractRecord) error {
	return s.InTransaction(ctx, func(tx *Tx) error {
		return tx.PutContract(ctx, record)
	})
}

func (tx *Tx) PutContract(ctx context.Context, record ContractRecord) error {
	if err := validateID(record.DocumentID, "doc_", "document_id"); err != nil {
		return err
	}
	if err := record.Contract.Validate(); err != nil {
		return fmt.Errorf("%w: contract: %v", ErrInvalidRecord, err)
	}
	provenance, sources, err := record.Trace.values()
	if err != nil {
		return err
	}
	payload, err := marshalJSON(record.Contract, "contract")
	if err != nil {
		return err
	}
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	var confirmedByType any
	var confirmedByID any
	var confirmedAt any
	if record.Contract.Status == writingkernel.ContractStatusConfirmed {
		if record.Trace.Actor.Type != ActorUser && record.Trace.Actor.Type != ActorSystem {
			return fmt.Errorf("%w: confirmed contract actor must be user or system", ErrInvalidRecord)
		}
		confirmedByType = string(record.Trace.Actor.Type)
		confirmedByID = nullString(record.Trace.Actor.ID)
		confirmedAt = createdAt
	}

	result, err := tx.tx.ExecContext(ctx, `
		INSERT INTO writing_contracts (
			contract_id, version, document_id, schema_version, contract_hash,
			content_hash, contract_payload, confirmation_status,
			confirmed_by_type, confirmed_by_id, confirmed_at, provenance,
			source_refs, created_by_type, created_by_id, created_at
		) VALUES ($1,$2,$3,$4,$5,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (contract_id, version) DO NOTHING
	`, record.Contract.ContractID, record.Contract.Version, record.DocumentID,
		record.Contract.SchemaVersion, record.Contract.ContractHash, payload,
		string(record.Contract.Status), confirmedByType, confirmedByID, confirmedAt,
		provenance, sources, string(record.Trace.Actor.Type), nullString(record.Trace.Actor.ID), createdAt)
	if err != nil {
		return fmt.Errorf("insert writing contract: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect writing contract insert: %w", err)
	}
	if inserted == 1 {
		return nil
	}

	var existingDocumentID, existingHash string
	var traceMatches bool
	err = tx.tx.QueryRowContext(ctx, `
		SELECT document_id, contract_hash,
		       provenance=$3::jsonb AND source_refs=$4::jsonb
		         AND created_by_type=$5 AND COALESCE(created_by_id, '')=$6
		FROM writing_contracts WHERE contract_id=$1 AND version=$2
	`, record.Contract.ContractID, record.Contract.Version, string(provenance), string(sources),
		string(record.Trace.Actor.Type), record.Trace.Actor.ID).Scan(&existingDocumentID, &existingHash, &traceMatches)
	if err != nil {
		return fmt.Errorf("load conflicting writing contract: %w", err)
	}
	// ContractHash is computed from the full canonical contract payload. Comparing
	// the hash avoids false conflicts caused by PostgreSQL JSONB key reordering.
	if existingDocumentID == record.DocumentID && existingHash == record.Contract.ContractHash && traceMatches {
		return nil
	}
	return fmt.Errorf("%w: contract %s version %d already exists with different content",
		ErrImmutableConflict, record.Contract.ContractID, record.Contract.Version)
}

func (s *Store) GetContract(ctx context.Context, contractID string, version int) (ContractRecord, error) {
	var record ContractRecord
	var payload, provenance, sources []byte
	var actorType string
	var actorID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT document_id, contract_payload, provenance, source_refs,
		       created_by_type, created_by_id, created_at
		FROM writing_contracts WHERE contract_id=$1 AND version=$2
	`, contractID, version).Scan(&record.DocumentID, &payload, &provenance, &sources,
		&actorType, &actorID, &record.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ContractRecord{}, ErrNotFound
	}
	if err != nil {
		return ContractRecord{}, fmt.Errorf("get writing contract: %w", err)
	}
	if err := json.Unmarshal(payload, &record.Contract); err != nil {
		return ContractRecord{}, fmt.Errorf("decode writing contract: %w", err)
	}
	record.Trace.Actor = Actor{Type: ActorType(actorType), ID: actorID.String}
	if err := json.Unmarshal(provenance, &record.Trace.Provenance); err != nil {
		return ContractRecord{}, fmt.Errorf("decode contract provenance: %w", err)
	}
	if err := json.Unmarshal(sources, &record.Trace.SourceRefs); err != nil {
		return ContractRecord{}, fmt.Errorf("decode contract source refs: %w", err)
	}
	return record, nil
}
