package tools

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type pacingRecorder struct {
	mu     sync.Mutex
	starts []time.Time
}

func (r *pacingRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.starts = append(r.starts, time.Now())
	r.mu.Unlock()
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}"))}, nil
}
func TestPacingSharedByStreamingAndNonStreaming(t *testing.T) {
	recorder := &pacingRecorder{}
	c := NewLLMClient("https://example.invalid/v1", "test", "test", 100, .2, time.Second)
	c.httpClient.Transport = recorder
	c.SetMinRequestInterval(25 * time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(stream bool) {
			defer wg.Done()
			req := c.buildRequest(nil, stream)
			if stream {
				body, err := c.doStreamRequest(context.Background(), req)
				if err != nil {
					t.Error(err)
					return
				}
				body.Close()
			} else {
				if _, err := c.doRequest(context.Background(), req); err != nil {
					t.Error(err)
				}
			}
		}(i%2 == 0)
	}
	wg.Wait()
	if len(recorder.starts) != 4 {
		t.Fatalf("requests=%d", len(recorder.starts))
	}
	for i := 1; i < len(recorder.starts); i++ {
		if gap := recorder.starts[i].Sub(recorder.starts[i-1]); gap < 20*time.Millisecond {
			t.Errorf("request gap too short: %s", gap)
		}
	}
}
func TestPacingCancellationDoesNotConsumeSlotOrSendRequest(t *testing.T) {
	recorder := &pacingRecorder{}
	p := &pacedTransport{base: recorder, interval: time.Second, next: time.Now().Add(time.Hour)}
	next := p.next
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://example.invalid", nil)
	if _, err := p.RoundTrip(req); err != context.DeadlineExceeded {
		t.Fatalf("error=%v", err)
	}
	if len(recorder.starts) != 0 || !p.next.Equal(next) {
		t.Fatal("cancelled request sent or reserved a slot")
	}
	p.next = time.Time{}
	req, _ = http.NewRequest("POST", "https://example.invalid", nil)
	response, err := p.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(recorder.starts) != 1 {
		t.Fatal("later request did not proceed")
	}
}
func TestPacingCanBeDisabledBeforeUse(t *testing.T) {
	recorder := &pacingRecorder{}
	c := NewLLMClient("https://example.invalid", "test", "test", 100, .2, time.Second)
	c.httpClient.Transport = recorder
	c.SetMinRequestInterval(time.Second)
	c.SetMinRequestInterval(0)
	if c.httpClient.Transport != recorder {
		t.Fatal("original transport not restored")
	}
}
