package scholar

import (
	"context"
	"sync"
	"time"
)

// MinIntervalRateLimiter is a simple per-operation minimum-interval limiter
// wired to the RateLimiter hook (T04). Each operation key ("provider class")
// must be at least Interval apart; the first call is immediate. A more
// sophisticated token-bucket or budget-aware limiter can replace it behind
// the same interface.
type MinIntervalRateLimiter struct {
	mu        sync.Mutex
	intervals map[Operation]time.Duration
	last      map[Operation]time.Time
	now       func() time.Time // injectable clock for tests
}

// NewMinIntervalRateLimiter builds a limiter with the given per-operation
// minimum intervals. A missing operation key defaults to the DefaultInterval.
func NewMinIntervalRateLimiter(intervals map[Operation]time.Duration) *MinIntervalRateLimiter {
	copied := make(map[Operation]time.Duration, len(intervals))
	for op, d := range intervals {
		copied[op] = d
	}
	return &MinIntervalRateLimiter{
		intervals: copied,
		last:      map[Operation]time.Time{},
		now:       time.Now,
	}
}

// DefaultInterval is used for operations without an explicit entry.
const DefaultInterval = 500 * time.Millisecond

// Wait blocks until the operation may start, or returns an error when ctx is
// done first (before any bytes hit the wire).
func (l *MinIntervalRateLimiter) Wait(ctx context.Context, op Operation) error {
	interval := l.intervals[op]
	if interval <= 0 {
		interval = DefaultInterval
	}
	for {
		l.mu.Lock()
		next := l.last[op].Add(interval)
		now := l.now()
		if l.last[op].IsZero() || !now.Before(next) {
			l.last[op] = now
			l.mu.Unlock()
			return nil
		}
		delay := next.Sub(now)
		l.mu.Unlock()

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			// Loop and re-acquire; another waiter may have taken the slot.
		}
	}
}
