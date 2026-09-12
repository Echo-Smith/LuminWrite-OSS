package writingstore

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecutionOwnershipCompetesAndReleases(t *testing.T) {
	store, f := newIntegrationFixture(t, true)
	ctx := context.Background()
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := store.WithRunExecutionLock(ctx, f.runID, func(context.Context) error { close(entered); <-release; return nil })
		done <- err
	}()
	<-entered
	var calls atomic.Int32
	acquired, err := store.WithRunExecutionLock(ctx, f.runID, func(context.Context) error { calls.Add(1); return nil })
	if err != nil || acquired || calls.Load() != 0 {
		t.Fatalf("concurrent worker acquired=%v err=%v", acquired, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	acquired, err = store.WithRunExecutionLock(ctx, f.runID, func(context.Context) error { calls.Add(1); return nil })
	if err != nil || !acquired || calls.Load() != 1 {
		t.Fatalf("restart could not acquire: %v %v", acquired, err)
	}
}
func TestExecutionConnectionLossCancelsWorker(t *testing.T) {
	store, f := newIntegrationFixture(t, true)
	ctx := context.Background()
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := store.WithRunExecutionLock(ctx, f.runID, func(owned context.Context) error { close(entered); <-owned.Done(); return owned.Err() })
		done <- err
	}()
	<-entered
	if _, err := integrationDB.Exec(`SELECT pg_terminate_backend(pid) FROM pg_locks WHERE locktype='advisory' AND classid=1280660039 AND pid<>pg_backend_pid()`); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("lost ownership did not cancel")
	}
	acquired, err := store.WithRunExecutionLock(ctx, f.runID, func(context.Context) error { return nil })
	if !acquired || err != nil {
		t.Fatalf("restart: %v %v", acquired, err)
	}
}
func TestRuntimePolicyRevisionRoundTripAndImmutable(t *testing.T) {
	store, _ := newIntegrationFixture(t, true)
	ctx := context.Background()
	id := "test." + fmt.Sprint(time.Now().UnixNano())
	record := RuntimePolicyRevision{CapabilityID: id, PolicyHash: testHash("policy"), Policy: []byte(`{"mode":"shadow"}`), OperatorID: "test-operator"}
	if err := store.AppendRuntimePolicy(ctx, record); err != nil {
		t.Fatal(err)
	}
	first, err := store.LatestRuntimePolicy(ctx, id)
	if err != nil || first.Active {
		t.Fatal(first, err)
	}
	record.Active = true
	if err = store.AppendRuntimePolicy(ctx, record); err != nil {
		t.Fatal(err)
	}
	restarted, _ := New(integrationDB)
	last, err := restarted.LatestRuntimePolicy(ctx, id)
	if err != nil || !last.Active || last.Revision <= first.Revision {
		t.Fatal(last, err)
	}
	if _, err = integrationDB.Exec(`UPDATE writing_runtime_policy_revisions SET active=false WHERE revision=$1`, last.Revision); err == nil {
		t.Fatal("audit revision was mutable")
	}
	if _, err = integrationDB.Exec(`DELETE FROM writing_runtime_policy_revisions WHERE revision=$1`, first.Revision); err == nil {
		t.Fatal("audit revision was deletable")
	}
}
