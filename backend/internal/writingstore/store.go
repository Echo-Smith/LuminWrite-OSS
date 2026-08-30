// Package writingstore persists the governed writing domain. It deliberately
// exposes domain records instead of database rows and keeps all authoritative
// writes behind explicit transactions.
package writingstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

var (
	ErrNotFound            = errors.New("writingstore: not found")
	ErrImmutableConflict   = errors.New("writingstore: immutable record conflict")
	ErrIdempotencyConflict = errors.New("writingstore: idempotency conflict")
	ErrInvalidRecord       = errors.New("writingstore: invalid record")
	ErrConflict            = writingkernel.ErrConflicted
)

var sha256Pattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type ActorType string

const (
	ActorUser       ActorType = "user"
	ActorSystem     ActorType = "system"
	ActorModel      ActorType = "model"
	ActorWorker     ActorType = "worker"
	ActorValidator  ActorType = "validator"
	ActorPolicy     ActorType = "policy"
	ActorCapability ActorType = "capability"
)

type Actor struct {
	Type ActorType `json:"type"`
	ID   string    `json:"id,omitempty"`
}

func (a Actor) Validate() error {
	switch a.Type {
	case ActorUser, ActorSystem, ActorModel, ActorWorker, ActorValidator, ActorPolicy, ActorCapability:
		return nil
	default:
		return fmt.Errorf("%w: unsupported actor type %q", ErrInvalidRecord, a.Type)
	}
}

type TraceContext struct {
	Provenance map[string]any `json:"provenance"`
	SourceRefs []string       `json:"source_refs"`
	Actor      Actor          `json:"actor"`
}

func (t TraceContext) validate() error {
	if err := t.Actor.Validate(); err != nil {
		return err
	}
	if t.Provenance == nil {
		return fmt.Errorf("%w: provenance must be present", ErrInvalidRecord)
	}
	if t.SourceRefs == nil {
		return fmt.Errorf("%w: source refs must be present", ErrInvalidRecord)
	}
	return nil
}

func (t TraceContext) values() ([]byte, []byte, error) {
	if err := t.validate(); err != nil {
		return nil, nil, err
	}
	provenance, err := json.Marshal(t.Provenance)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal provenance: %w", err)
	}
	sources, err := json.Marshal(t.SourceRefs)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal source refs: %w", err)
	}
	return provenance, sources, nil
}

type Store struct {
	db *database.DB
}

func New(db *database.DB) (*Store, error) {
	if db == nil || db.DB == nil {
		return nil, errors.New("writingstore: database is required")
	}
	return &Store{db: db}, nil
}

type Tx struct {
	tx *sql.Tx
}

func (s *Store) InTransaction(ctx context.Context, fn func(*Tx) error) error {
	if fn == nil {
		return errors.New("writingstore: transaction callback is required")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin writing transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := fn(&Tx{tx: tx}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit writing transaction: %w", err)
	}
	committed = true
	return nil
}

func marshalJSON(value any, field string) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", field, err)
	}
	return encoded, nil
}

func validateID(value, prefix, field string) error {
	if !strings.HasPrefix(value, prefix) || len(value) <= len(prefix) {
		return fmt.Errorf("%w: %s must use %s prefix", ErrInvalidRecord, field, prefix)
	}
	return nil
}

func validateHash(value, field string) error {
	if !sha256Pattern.MatchString(value) {
		return fmt.Errorf("%w: %s must be a lowercase sha256 digest", ErrInvalidRecord, field)
	}
	return nil
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
