package vision

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrPaused means SAM calls are paused after a rate limit; the
// caller should skip this tick rather than retry.
var ErrPaused = errors.New("vision: SAM paused after a rate limit")

// retryAfterer is an error carrying the server's back-off hint
// (meta.RateLimitError). Matched by interface so vision doesn't
// import meta.
type retryAfterer interface {
	error
	RetryAfter() time.Duration
}

type urgentKey struct{}

// Urgent marks a call the user is waiting on ("where are my
// keys?"): during a short pause it waits instead of failing.
func Urgent(ctx context.Context) context.Context {
	return context.WithValue(ctx, urgentKey{}, true)
}

func isUrgent(ctx context.Context) bool {
	v, _ := ctx.Value(urgentKey{}).(bool)
	return v
}

// Limiter is the one Segmenter the tracker and finder share. When
// SAM answers HTTP 429 every caller pauses together — 2 s, doubling
// to 30 s while the limit lasts, or the server's Retry-After — so
// the 1 Hz tracker and the background scans stop feeding the limit
// that is stalling them. During a pause background calls fail fast
// with ErrPaused (the tracker just skips a tick); a call marked
// Urgent waits up to MaxWait for it to end.
type Limiter struct {
	Seg     Segmenter
	MaxWait time.Duration // urgent calls wait at most this; default 8 s
	Logf    func(string)

	mu     sync.Mutex
	until  time.Time
	streak int
}

func (l *Limiter) logf(format string, args ...any) {
	if l.Logf != nil {
		l.Logf(fmt.Sprintf(format, args...))
	}
}

// Paused reports whether calls are currently held back.
func (l *Limiter) Paused() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return time.Now().Before(l.until)
}

func (l *Limiter) Segment(ctx context.Context, jpeg []byte, prompt string) (string, error) {
	l.mu.Lock()
	wait := time.Until(l.until)
	l.mu.Unlock()
	if wait > 0 {
		max := l.MaxWait
		if max <= 0 {
			max = 8 * time.Second
		}
		if !isUrgent(ctx) || wait > max {
			return "", ErrPaused
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(wait):
		}
	}
	text, err := l.Seg.Segment(ctx, jpeg, prompt)
	var rl retryAfterer
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case errors.As(err, &rl):
		pause := rl.RetryAfter()
		if pause <= 0 {
			pause = min(2*time.Second<<min(l.streak, 4), 30*time.Second)
		}
		l.streak++
		// Concurrent callers can all hit the same limit; only the
		// first extends the pause and logs it.
		if until := time.Now().Add(pause); until.After(l.until) {
			if time.Now().After(l.until) {
				l.logf("vision: SAM rate limited — pausing camera calls for %s", pause.Round(time.Second))
			}
			l.until = until
		}
		return "", ErrPaused
	case err == nil:
		if l.streak > 0 {
			l.logf("vision: SAM calls resumed")
		}
		l.streak = 0
	}
	return text, err
}
