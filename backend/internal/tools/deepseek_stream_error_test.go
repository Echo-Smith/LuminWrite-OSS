package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newStreamCutServer starts a test server that emits a single SSE content
// delta, then abruptly kills the TCP connection mid-stream (raw chunked
// framing without the terminal chunk + RST close). A client reading the body
// therefore gets a real transport error (io.ErrUnexpectedEOF family), not a
// clean EOF.
func newStreamCutServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			panic("response writer does not support hijacking")
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			panic(err)
		}
		defer conn.Close()
		body := "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"
		// Hand-written HTTP/1.1 chunked response that promises more chunks
		// but never sends the terminating zero-length chunk.
		fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n")
		fmt.Fprintf(conn, "%x\r\n%s\r\n", len(body), body)
		// Force an RST so the client cannot read a graceful EOF.
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newSSEServer starts a test server that replays the given raw SSE payload
// with normal chunked framing (orderly close afterwards).
func newSSEServer(t *testing.T, payload string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, payload)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestChatStreamPropagatesMidStreamReadError(t *testing.T) {
	srv := newStreamCutServer(t)
	client := NewLLMClient(srv.URL, "test", "test-model", 1024, 0.7, 10*time.Second)

	var streamed strings.Builder
	text, _, err := client.ChatStream(context.Background(), []LLMMessage{{Role: "user", Content: "hi"}}, func(delta string) { streamed.WriteString(delta) })
	if err == nil {
		t.Fatalf("mid-stream connection cut was swallowed: returned (text=%q, nil error)", text)
	}
	if !strings.Contains(err.Error(), "SSE stream read failed") {
		t.Fatalf("error does not identify the stream read failure: %v", err)
	}
	// The transport error must surface, not be converted into a normal EOF.
	if errors.Is(err, io.EOF) {
		t.Fatalf("transport failure was misclassified as a clean EOF: %v", err)
	}
}

func TestChatCompletionsStreamReturnsPartialTextAlongsideError(t *testing.T) {
	srv := newStreamCutServer(t)
	client := NewLLMClient(srv.URL, "test", "test-model", 1024, 0.7, 10*time.Second)
	req := client.buildRequest([]LLMMessage{{Role: "user", Content: "hi"}}, true)

	text, _, _, err := client.chatCompletionsStream(context.Background(), req, nil, nil)
	if err == nil {
		t.Fatalf("mid-stream cut returned success with text %q", text)
	}
	if text != "partial" {
		t.Fatalf("partial text = %q, want the already-received deltas", text)
	}
}

func TestChatStreamRoundPropagatesMidStreamReadError(t *testing.T) {
	srv := newStreamCutServer(t)
	client := NewLLMClient(srv.URL, "test", "test-model", 1024, 0.7, 10*time.Second)

	msg, _, _, _, err := client.chatStreamRound(
		context.Background(),
		[]LLMMessage{{Role: "user", Content: "hi"}},
		nil, nil, nil,
	)
	if err == nil {
		t.Fatalf("chatStreamRound swallowed a mid-stream cut and returned %+v", msg)
	}
	if !strings.Contains(err.Error(), "SSE stream read failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestChatWithToolsWrapsMidStreamReadErrorAsRoundFailure(t *testing.T) {
	srv := newStreamCutServer(t)
	client := NewLLMClient(srv.URL, "test", "test-model", 1024, 0.7, 10*time.Second)

	_, _, err := client.ChatWithTools(
		context.Background(),
		[]LLMMessage{{Role: "user", Content: "hi"}},
		nil, nil, nil,
		nil, nil,
	)
	if err == nil {
		t.Fatal("ChatWithTools returned success for an aborted stream")
	}
	if !strings.Contains(err.Error(), "agent loop iteration 0 failed") {
		t.Fatalf("stream failure was not reported as an agent loop round failure: %v", err)
	}
}

func TestChatStreamNormalEOFSemanticsUnchanged(t *testing.T) {
	payload := "data: {\"choices\":[{\"delta\":{\"content\":\"你好\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"，世界\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5,\"total_tokens\":8}}\n\n" +
		"data: [DONE]\n\n"
	srv := newSSEServer(t, payload)
	client := NewLLMClient(srv.URL, "test", "test-model", 1024, 0.7, 10*time.Second)

	text, tokens, err := client.ChatStream(context.Background(), []LLMMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("complete stream returned error: %v", err)
	}
	if text != "你好，世界" || tokens != 8 {
		t.Fatalf("text=%q tokens=%d, want 你好，世界 / 8", text, tokens)
	}
}

func TestChatStreamOrderlyCloseWithoutDONESucceeds(t *testing.T) {
	// Provider closed the connection cleanly after the final chunk without a
	// [DONE] sentinel: existing semantics treat the clean EOF as success and
	// must keep doing so.
	srv := newSSEServer(t, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"}}]}\n\n")
	client := NewLLMClient(srv.URL, "test", "test-model", 1024, 0.7, 10*time.Second)

	text, _, err := client.ChatStream(context.Background(), []LLMMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("orderly close without [DONE] must stay a success, got %v", err)
	}
	if text != "done" {
		t.Fatalf("text = %q, want done", text)
	}
}
