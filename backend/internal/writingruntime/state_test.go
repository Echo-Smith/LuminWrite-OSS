package writingruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

func TestRunStateInventoryIsStable(t *testing.T) {
	want := []RunState{
		StateDraft, StateContractConfirmed, StatePlanning, StatePlanned,
		StateAwaitingApproval, StateRunning, StatePausing, StatePaused,
		StateReplanning, StateFailed, StateCancelling, StateCancelled, StateCompleted,
	}
	if got := AllRunStates(); len(got) != len(want) {
		t.Fatalf("state inventory length = %d, want %d: %#v", len(got), len(want), got)
	} else {
		for index := range want {
			if got[index] != want[index] || !got[index].Valid() {
				t.Fatalf("state inventory[%d] = %q, want %q", index, got[index], want[index])
			}
		}
	}
	if RunState("unknown").Valid() {
		t.Fatal("unknown state must not validate")
	}
}

func TestStateMachineAllowsOnlyDeclaredTransitions(t *testing.T) {
	allowed := map[transitionPair]bool{
		{StateDraft, StateContractConfirmed}:      true,
		{StateDraft, StateCancelling}:             true,
		{StateContractConfirmed, StatePlanning}:   true,
		{StateContractConfirmed, StateCancelling}: true,
		{StatePlanning, StatePlanned}:             true,
		{StatePlanning, StateFailed}:              true,
		{StatePlanning, StateCancelling}:          true,
		{StatePlanned, StateAwaitingApproval}:     true,
		{StatePlanned, StateRunning}:              true,
		{StatePlanned, StateReplanning}:           true,
		{StatePlanned, StateCancelling}:           true,
		{StateAwaitingApproval, StateRunning}:     true,
		{StateAwaitingApproval, StateReplanning}:  true,
		{StateAwaitingApproval, StateCancelling}:  true,
		{StateRunning, StatePausing}:              true,
		{StateRunning, StateReplanning}:           true,
		{StateRunning, StateFailed}:               true,
		{StateRunning, StateCancelling}:           true,
		{StateRunning, StateCompleted}:            true,
		{StatePausing, StatePaused}:               true,
		{StatePausing, StateFailed}:               true,
		{StatePausing, StateCancelling}:           true,
		{StatePaused, StateRunning}:               true,
		{StatePaused, StateReplanning}:            true,
		{StatePaused, StateCancelling}:            true,
		{StateReplanning, StatePlanned}:           true,
		{StateReplanning, StateFailed}:            true,
		{StateReplanning, StateCancelling}:        true,
		{StateFailed, StateReplanning}:            true,
		{StateFailed, StateCancelling}:            true,
		{StateCancelling, StateCancelled}:         true,
		{StateCancelling, StateFailed}:            true,
	}

	for _, from := range AllRunStates() {
		for _, to := range AllRunStates() {
			want := allowed[transitionPair{from, to}]
			if got := CanTransition(from, to); got != want {
				t.Errorf("CanTransition(%q, %q) = %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestTransitionRecordsAcceptedDecisionBeforeReturningNewState(t *testing.T) {
	recorder := &recordingTransitionStore{}
	machine := NewStateMachine(recorder)
	machine.now = func() time.Time { return time.Date(2026, 8, 28, 5, 0, 0, 0, time.UTC) }

	next, err := machine.Transition(context.Background(), validTransitionRequest(StatePlanned, StateRunning))
	if err != nil {
		t.Fatal(err)
	}
	if next != StateRunning || len(recorder.records) != 1 {
		t.Fatalf("next=%q records=%#v", next, recorder.records)
	}
	record := recorder.records[0]
	if !record.Accepted || record.EventType != EventRunTransitioned || record.EffectiveState != StateRunning {
		t.Fatalf("invalid accepted transition record: %#v", record)
	}
	if record.OccurredAt != machine.now() || record.CommandID != "command_test" {
		t.Fatalf("transition identity/time not preserved: %#v", record)
	}
}

func TestIllegalTransitionIsRejectedAndStillRecorded(t *testing.T) {
	recorder := &recordingTransitionStore{}
	machine := NewStateMachine(recorder)

	next, err := machine.Transition(context.Background(), validTransitionRequest(StateRunning, StateDraft))
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("illegal transition error = %v", err)
	}
	if next != StateRunning || len(recorder.records) != 1 {
		t.Fatalf("illegal transition next=%q records=%#v", next, recorder.records)
	}
	record := recorder.records[0]
	if record.Accepted || record.EventType != EventRunTransitionRejected || record.EffectiveState != StateRunning {
		t.Fatalf("invalid rejection record: %#v", record)
	}
	if record.ReasonCode != ReasonInvalidTransition {
		t.Fatalf("rejection reason = %q", record.ReasonCode)
	}
}

func TestTerminalStatesRejectEveryTransitionAndWriteAuditRecord(t *testing.T) {
	for _, terminal := range []RunState{StateCancelled, StateCompleted} {
		t.Run(string(terminal), func(t *testing.T) {
			recorder := &recordingTransitionStore{}
			machine := NewStateMachine(recorder)
			next, err := machine.Transition(context.Background(), validTransitionRequest(terminal, StateRunning))
			if !errors.Is(err, ErrInvalidTransition) || next != terminal || len(recorder.records) != 1 {
				t.Fatalf("terminal transition next=%q err=%v records=%#v", next, err, recorder.records)
			}
		})
	}
}

func TestInvalidRequestDoesNotWriteUnattributableEvent(t *testing.T) {
	recorder := &recordingTransitionStore{}
	machine := NewStateMachine(recorder)
	request := validTransitionRequest(StateDraft, StateContractConfirmed)
	request.RunID = ""
	if _, err := machine.Transition(context.Background(), request); !errors.Is(err, ErrInvalidTransitionRequest) {
		t.Fatalf("invalid request error = %v", err)
	}
	if len(recorder.records) != 0 {
		t.Fatalf("invalid request wrote records: %#v", recorder.records)
	}
}

func TestRecorderFailurePreventsStateProjection(t *testing.T) {
	recordErr := errors.New("ledger unavailable")
	recorder := &recordingTransitionStore{err: recordErr}
	machine := NewStateMachine(recorder)
	next, err := machine.Transition(context.Background(), validTransitionRequest(StatePlanned, StateRunning))
	if !errors.Is(err, recordErr) || next != StatePlanned {
		t.Fatalf("recorder failure next=%q err=%v", next, err)
	}
}

type recordingTransitionStore struct {
	records []TransitionRecord
	err     error
}

func (s *recordingTransitionStore) RecordTransition(_ context.Context, record TransitionRecord) error {
	s.records = append(s.records, record)
	return s.err
}

func validTransitionRequest(from, to RunState) TransitionRequest {
	return TransitionRequest{
		CommandID: "command_test", RunID: "run_test", From: from, To: to,
		Cause: "test", Summary: "test transition",
		Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "writingruntime-test"},
	}
}
