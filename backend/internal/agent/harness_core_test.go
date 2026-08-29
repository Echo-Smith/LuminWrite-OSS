package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	worldstate "github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/worldstate"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// HarnessCore defensive contract: RunCore is a provisional value producer.
// It must never touch session persistence, never emit terminal/UI events,
// and must fail stably when the caller cancels mid-stream.

type recordingSessionStore struct {
	mu    sync.Mutex
	loads int
	dores int
}

func (store *recordingSessionStore) IsEnabledForUser(string) bool { return true }

func (store *recordingSessionStore) LoadHistory(context.Context, string, int) ([]memory.ConversationMessage, error) {
	store.mu.Lock()
	store.loads++
	store.mu.Unlock()
	return nil, nil
}

func (store *recordingSessionStore) StoreMessage(context.Context, *memory.ConversationMessage) error {
	store.mu.Lock()
	store.dores++
	store.mu.Unlock()
	return nil
}

type recordingEmitter struct {
	mu      sync.Mutex
	streams int
	resets  int
	dones   int
	titles  int
	errors  int
	pauses  int
	dones2  int // Completed
	total   int
}

func (emitter *recordingEmitter) note() {
	emitter.total++
}
func (emitter *recordingEmitter) StepStart(engine.StepName, int)                  { emitter.note() }
func (emitter *recordingEmitter) StepComplete(engine.StepName, interface{}, int64) { emitter.note() }
func (emitter *recordingEmitter) StreamDelta(string)                              { emitter.mu.Lock(); emitter.streams++; emitter.mu.Unlock(); emitter.note() }
func (emitter *recordingEmitter) StreamReset()                                    { emitter.mu.Lock(); emitter.resets++; emitter.mu.Unlock(); emitter.note() }
func (emitter *recordingEmitter) ReasoningDelta(string)                           { emitter.note() }
func (emitter *recordingEmitter) ArticleTitle(string)                             { emitter.mu.Lock(); emitter.titles++; emitter.mu.Unlock(); emitter.note() }
func (emitter *recordingEmitter) StreamDone(string)                               { emitter.mu.Lock(); emitter.dones++; emitter.mu.Unlock(); emitter.note() }
func (emitter *recordingEmitter) AwaitInput(engine.StepName, interface{}, []string, int, int) { emitter.note() }
func (emitter *recordingEmitter) Paused(engine.StepName, interface{})             { emitter.mu.Lock(); emitter.pauses++; emitter.mu.Unlock(); emitter.note() }
func (emitter *recordingEmitter) PausedWithReason(engine.StepName, interface{}, string) { emitter.note() }
func (emitter *recordingEmitter) Resumed(engine.StepName)                         { emitter.note() }
func (emitter *recordingEmitter) Error(string, string, engine.StepName)           { emitter.mu.Lock(); emitter.errors++; emitter.mu.Unlock(); emitter.note() }
func (emitter *recordingEmitter) Completed(string, string, interface{}, interface{}) { emitter.mu.Lock(); emitter.dones2++; emitter.mu.Unlock(); emitter.note() }
func (emitter *recordingEmitter) Cancelled()                                      { emitter.note() }
func (emitter *recordingEmitter) Compaction(int, int, string, uint64, string)     { emitter.note() }

func (emitter *recordingEmitter) counts() (int, int, int, int, int) {
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	return emitter.total, emitter.dones, emitter.dones2, emitter.streams, emitter.resets
}

func newCoreTestHarness(client *tools.LLMClient, store SessionStore, emitter engine.EventEmitter) *Harness {
	return &Harness{llm: client, sessionStore: store, emitter: emitter, maxIterations: 12,
		worldState: worldstate.NewWorldState(), tokenBudget: &worldstate.TokenBudget{ContextWindowID: ""},
		autoCompact: worldstate.NewAutoCompactFallback()}
}

// newChatStreamStub serves the minimal chat-completions SSE contract the
// governed core path needs: content deltas, one usage frame, and [DONE].
func newChatStreamStub(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	if handler == nil {
		handler = func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" core\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":7}}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func newCoreClient(t *testing.T, baseURL string) *tools.LLMClient {
	t.Helper()
	return tools.NewLLMClient(baseURL, "test-key", "test-model", 256, 0.3, 5*time.Second)
}

func newCoreSessionAndContext() (*WritingSession, *engine.ExecutionContext) {
	session := NewWritingSession("conversation-core", "user_core", "")
	execCtx := engine.NewCompatibilityExecutionContext(engine.CompatibilityInput{TraceID: "trace_core", UserID: "user_core", UserInput: "你好"})
	return session, execCtx
}

func TestRunCorePersistsNothingAndEmitsNothing(t *testing.T) {
	server := newChatStreamStub(t, nil)
	store := &recordingSessionStore{}
	emitter := &recordingEmitter{}
	harness := newCoreTestHarness(newCoreClient(t, server.URL), store, emitter)
	session, execCtx := newCoreSessionAndContext()

	output, err := harness.RunCore(context.Background(), execCtx, session)
	if err != nil {
		t.Fatal(err)
	}
	if output.Article != "Hello core" || output.TotalTokens != 7 {
		t.Fatalf("output=%#v", output)
	}
	if store.loads != 0 || store.dores != 0 {
		t.Fatalf("RunCore touched session persistence: loads=%d stores=%d", store.loads, store.dores)
	}
	total, dones, completes, streams, resets := emitter.counts()
	if total != 0 || dones != 0 || completes != 0 || streams != 0 || resets != 0 {
		t.Fatalf("RunCore emitted UI events: total=%d streamDone=%d completed=%d deltas=%d resets=%d", total, dones, completes, streams, resets)
	}
}

func TestRunPersistentPathIsTheOneThatPersistsAndEmits(t *testing.T) {
	server := newChatStreamStub(t, nil)
	store := &recordingSessionStore{}
	emitter := &recordingEmitter{}
	harness := newCoreTestHarness(newCoreClient(t, server.URL), store, emitter)
	session, execCtx := newCoreSessionAndContext()

	if err := harness.Run(context.Background(), execCtx, session); err != nil {
		t.Fatal(err)
	}
	if store.loads != 1 || store.dores != 2 {
		t.Fatalf("Run must persist history load and both messages: loads=%d stores=%d", store.loads, store.dores)
	}
	total, dones, completes, streams, resets := emitter.counts()
	if dones != 1 || completes != 1 || streams == 0 || total == 0 || resets != 0 {
		t.Fatalf("Run must emit StreamDone+Completed: total=%d streamDone=%d completed=%d deltas=%d resets=%d", total, dones, completes, streams, resets)
	}
}

func TestRunCoreMidStreamCancelReturnsStableError(t *testing.T) {
	// The stub blocks until the test releases it: the client-side context
	// cancellation must be what terminates RunCore, not server behaviour.
	release := make(chan struct{})
	server := newChatStreamStub(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
		_ = r
	})
	store := &recordingSessionStore{}
	emitter := &recordingEmitter{}
	harness := newCoreTestHarness(newCoreClient(t, server.URL), store, emitter)
	session, execCtx := newCoreSessionAndContext()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	output, err := harness.RunCore(ctx, execCtx, session)
	close(release)
	if err == nil {
		t.Fatalf("cancelled RunCore returned provisional output %#v", output)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v is not a stable cancellation", err)
	}
	if store.loads != 0 || store.dores != 0 {
		t.Fatalf("cancelled RunCore touched persistence: loads=%d stores=%d", store.loads, store.dores)
	}
	if total, dones, completes, _, _ := emitter.counts(); total != 0 || dones != 0 || completes != 0 {
		t.Fatalf("cancelled RunCore emitted events: total=%d streamDone=%d completed=%d", total, dones, completes)
	}
}

func TestRunCorePreCanceledContextFailsFast(t *testing.T) {
	server := newChatStreamStub(t, nil)
	store := &recordingSessionStore{}
	emitter := &recordingEmitter{}
	harness := newCoreTestHarness(newCoreClient(t, server.URL), store, emitter)
	session, execCtx := newCoreSessionAndContext()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := harness.RunCore(ctx, execCtx, session); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if store.loads != 0 || store.dores != 0 {
		t.Fatalf("loads=%d stores=%d", store.loads, store.dores)
	}
	if total, _, _, _, _ := emitter.counts(); total != 0 {
		t.Fatalf("emitter events=%d", total)
	}
}
