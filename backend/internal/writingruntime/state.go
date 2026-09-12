// Package writingruntime coordinates governed writing runs without allowing
// legacy executors to become an authoritative source of run state.
package writingruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

type RunState string

const (
	StateDraft             RunState = "draft"
	StateContractConfirmed RunState = "contract_confirmed"
	StatePlanning          RunState = "planning"
	StatePlanned           RunState = "planned"
	StateAwaitingApproval  RunState = "awaiting_approval"
	StateRunning           RunState = "running"
	StatePausing           RunState = "pausing"
	StatePaused            RunState = "paused"
	StateReplanning        RunState = "replanning"
	StateFailed            RunState = "failed"
	StateCancelling        RunState = "cancelling"
	StateCancelled         RunState = "cancelled"
	StateCompleted         RunState = "completed"
)

var runStates = []RunState{
	StateDraft, StateContractConfirmed, StatePlanning, StatePlanned,
	StateAwaitingApproval, StateRunning, StatePausing, StatePaused,
	StateReplanning, StateFailed, StateCancelling, StateCancelled, StateCompleted,
}

func AllRunStates() []RunState {
	return append([]RunState(nil), runStates...)
}

func (state RunState) Valid() bool {
	for _, candidate := range runStates {
		if state == candidate {
			return true
		}
	}
	return false
}

type transitionPair struct {
	from RunState
	to   RunState
}

var allowedTransitions = map[transitionPair]struct{}{
	{StateDraft, StateContractConfirmed}: {},
	{StateDraft, StateCancelling}:        {},

	{StateContractConfirmed, StatePlanning}:   {},
	{StateContractConfirmed, StateCancelling}: {},

	{StatePlanning, StatePlanned}:    {},
	{StatePlanning, StateFailed}:     {},
	{StatePlanning, StateCancelling}: {},

	{StatePlanned, StateAwaitingApproval}: {},
	{StatePlanned, StateRunning}:          {},
	{StatePlanned, StateReplanning}:       {},
	{StatePlanned, StateCancelling}:       {},

	{StateAwaitingApproval, StateRunning}:    {},
	{StateAwaitingApproval, StateReplanning}: {},
	{StateAwaitingApproval, StateCancelling}: {},

	{StateRunning, StatePausing}:    {},
	{StateRunning, StateReplanning}: {},
	{StateRunning, StateFailed}:     {},
	{StateRunning, StateCancelling}: {},
	{StateRunning, StateCompleted}:  {},

	{StatePausing, StatePaused}:     {},
	{StatePausing, StateFailed}:     {},
	{StatePausing, StateCancelling}: {},

	{StatePaused, StateRunning}:    {},
	{StatePaused, StateReplanning}: {},
	{StatePaused, StateCancelling}: {},

	{StateReplanning, StatePlanned}:    {},
	{StateReplanning, StateFailed}:     {},
	{StateReplanning, StateCancelling}: {},

	{StateFailed, StateReplanning}: {},
	{StateFailed, StateCancelling}: {},

	{StateCancelling, StateCancelled}: {},
	{StateCancelling, StateFailed}:    {},
}

func CanTransition(from, to RunState) bool {
	if !from.Valid() || !to.Valid() || from == to {
		return false
	}
	_, allowed := allowedTransitions[transitionPair{from: from, to: to}]
	return allowed
}

const (
	EventRunTransitioned       = "run.transitioned"
	EventRunTransitionRejected = "run.transition_rejected"
	ReasonTransitionApplied    = "transition_applied"
	ReasonInvalidTransition    = "invalid_transition"
)

var (
	ErrInvalidTransitionRequest = errors.New("writingruntime: invalid transition request")
	ErrInvalidTransition        = errors.New("writingruntime: invalid run transition")
	ErrTransitionStoreRequired  = errors.New("writingruntime: transition store is required")
)

type TransitionRequest struct {
	CommandID  string
	RunID      string
	From       RunState
	To         RunState
	Cause      string
	ReasonCode string
	Summary    string
	Actor      writingstore.Actor
	OccurredAt time.Time
}

func (request TransitionRequest) validate() error {
	if strings.TrimSpace(request.CommandID) == "" || strings.Contains(request.CommandID, ":") {
		return fmt.Errorf("%w: command_id must be nonblank and cannot contain ':'", ErrInvalidTransitionRequest)
	}
	if !strings.HasPrefix(request.RunID, "run_") || len(request.RunID) <= len("run_") {
		return fmt.Errorf("%w: run_id must use run_ prefix", ErrInvalidTransitionRequest)
	}
	if !request.From.Valid() || !request.To.Valid() {
		return fmt.Errorf("%w: from and to must be governed run states", ErrInvalidTransitionRequest)
	}
	if strings.TrimSpace(request.Cause) == "" || strings.TrimSpace(request.Summary) == "" {
		return fmt.Errorf("%w: cause and summary are required", ErrInvalidTransitionRequest)
	}
	if err := request.Actor.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTransitionRequest, err)
	}
	return nil
}

type TransitionRecord struct {
	CommandID      string
	IdempotencyKey string
	RunID          string
	From           RunState
	To             RunState
	EffectiveState RunState
	Cause          string
	ReasonCode     string
	Summary        string
	Accepted       bool
	EventType      string
	Actor          writingstore.Actor
	OccurredAt     time.Time
}

// TransitionStore must commit the transition record and, when Accepted is
// true, the run projection in one transaction. Rejected records only advance
// the audit ledger. CommandID is the stable replay identity.
type TransitionStore interface {
	RecordTransition(context.Context, TransitionRecord) error
}

type StateMachine struct {
	store TransitionStore
	now   func() time.Time
}

func NewStateMachine(store TransitionStore) *StateMachine {
	return &StateMachine{store: store, now: func() time.Time { return time.Now().UTC() }}
}

func (machine *StateMachine) Transition(ctx context.Context, request TransitionRequest) (RunState, error) {
	if err := request.validate(); err != nil {
		return request.From, err
	}
	if machine == nil || machine.store == nil {
		return request.From, ErrTransitionStoreRequired
	}
	accepted := CanTransition(request.From, request.To)
	record := TransitionRecord{
		CommandID: request.CommandID, IdempotencyKey: request.RunID + ":transition:" + request.CommandID,
		RunID: request.RunID, From: request.From, To: request.To, Cause: request.Cause,
		Summary: request.Summary, Accepted: accepted, Actor: request.Actor, OccurredAt: request.OccurredAt,
	}
	if record.OccurredAt.IsZero() {
		record.OccurredAt = machine.now()
	}
	if accepted {
		record.EffectiveState = request.To
		record.EventType = EventRunTransitioned
		record.ReasonCode = request.ReasonCode
		if record.ReasonCode == "" {
			record.ReasonCode = ReasonTransitionApplied
		}
	} else {
		record.EffectiveState = request.From
		record.EventType = EventRunTransitionRejected
		record.ReasonCode = ReasonInvalidTransition
	}
	if err := machine.store.RecordTransition(ctx, record); err != nil {
		return request.From, fmt.Errorf("record run transition: %w", err)
	}
	if !accepted {
		return request.From, &TransitionError{RunID: request.RunID, From: request.From, To: request.To}
	}
	return request.To, nil
}

type TransitionError struct {
	RunID string
	From  RunState
	To    RunState
}

func (err *TransitionError) Error() string {
	return fmt.Sprintf("%s: run %s cannot transition from %s to %s", ErrInvalidTransition, err.RunID, err.From, err.To)
}

func (err *TransitionError) Unwrap() error {
	return ErrInvalidTransition
}
