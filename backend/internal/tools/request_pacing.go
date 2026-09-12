package tools

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// SetMinRequestInterval configures pacing for all requests made by this client,
// including streaming, retries, and Responses API calls. Configure before use.
// The limit is per client, not an account-wide or multi-process rate limit.
func (c *LLMClient) SetMinRequestInterval(interval time.Duration) {
	transport := c.httpClient.Transport
	if paced, ok := transport.(*pacedTransport); ok {
		transport = paced.base
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	if interval > 0 {
		transport = &pacedTransport{base: transport, interval: interval}
	}
	clone := *c.httpClient
	clone.Transport = transport
	c.httpClient = &clone
}

type pacedTransport struct {
	base     http.RoundTripper
	interval time.Duration
	mu       sync.Mutex
	next     time.Time
}

func (p *pacedTransport) wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		p.mu.Lock()
		delay := time.Until(p.next)
		if delay <= 0 {
			p.next = time.Now().Add(p.interval)
			p.mu.Unlock()
			return nil
		}
		p.mu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (p *pacedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := p.wait(req.Context()); err != nil {
		return nil, err
	}
	return p.base.RoundTrip(req)
}
