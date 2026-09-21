package agent

// HarnessCore defensive contract (⑥D): RunCore is the ONLY entry point and
// a provisional value producer. The struct no longer has a SessionStore or
// an emitter field, so "never touches session persistence, never emits
// terminal/UI events" is now structurally guaranteed — these tests pin the
// observable behavior and the stable cancellation semantics.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	worldstate "github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/worldstate"
)

func newCoreTestHarness(client *tools.LLMClient) *Harness {
	return &Harness{llm: client, maxIterations: 12,
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

func TestRunCoreProducesProvisionalValue(t *testing.T) {
	server := newChatStreamStub(t, nil)
	harness := newCoreTestHarness(newCoreClient(t, server.URL))
	session, execCtx := newCoreSessionAndContext()

	output, err := harness.RunCore(context.Background(), execCtx, session)
	if err != nil {
		t.Fatal(err)
	}
	if output.Article != "Hello core" || output.TotalTokens != 7 {
		t.Fatalf("output=%#v", output)
	}
	// The value lives in the caller-owned execCtx only; the Harness has no
	// SessionStore field through which it could persist anything.
	if execCtx.Article != "Hello core" {
		t.Fatalf("execCtx article=%q", execCtx.Article)
	}
	if execCtx.Status != engine.StatusCompleted {
		t.Fatalf("execCtx status=%s", execCtx.Status)
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
	harness := newCoreTestHarness(newCoreClient(t, server.URL))
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
	if execCtx.Status != engine.StatusFailed {
		t.Fatalf("cancelled run must fail closed, status=%s", execCtx.Status)
	}
}

func TestRunCorePreCanceledContextFailsFast(t *testing.T) {
	server := newChatStreamStub(t, nil)
	harness := newCoreTestHarness(newCoreClient(t, server.URL))
	session, execCtx := newCoreSessionAndContext()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := harness.RunCore(ctx, execCtx, session); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
