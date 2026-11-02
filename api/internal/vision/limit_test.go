package vision

import (
	"context"
	"errors"
	"testing"
	"time"
)

type rateLimited struct{ wait time.Duration }

func (e rateLimited) Error() string             { return "429" }
func (e rateLimited) RetryAfter() time.Duration { return e.wait }

func TestLimiterPausesEveryoneAfterRateLimit(t *testing.T) {
	seg := &fakeSegmenter{err: rateLimited{wait: 300 * time.Millisecond}}
	var logs []string
	l := &Limiter{Seg: seg, Logf: func(m string) { logs = append(logs, m) }}
	ctx := context.Background()

	if _, err := l.Segment(ctx, nil, "person"); !errors.Is(err, ErrPaused) {
		t.Fatalf("429 err = %v, want ErrPaused", err)
	}
	// Background callers fail fast without reaching SAM.
	if _, err := l.Segment(ctx, nil, "keys"); !errors.Is(err, ErrPaused) || seg.calls() != 1 {
		t.Fatalf("paused call reached SAM: err=%v calls=%d", err, seg.calls())
	}
	if !l.Paused() || len(logs) != 1 {
		t.Errorf("paused=%v logs=%v", l.Paused(), logs)
	}
	// An urgent call (the user asked) waits the pause out.
	seg.mu.Lock()
	seg.err, seg.text = nil, personLine
	seg.mu.Unlock()
	start := time.Now()
	out, err := l.Segment(Urgent(ctx), nil, "keys")
	if err != nil || out != personLine {
		t.Fatalf("urgent call = %q, %v", out, err)
	}
	if time.Since(start) < 200*time.Millisecond {
		t.Error("urgent call did not wait for the pause")
	}
	if l.Paused() {
		t.Error("still paused after a success")
	}
}

func TestLimiterBacksOffWithoutRetryAfter(t *testing.T) {
	seg := &fakeSegmenter{err: rateLimited{}}
	l := &Limiter{Seg: seg, MaxWait: time.Millisecond}
	_, _ = l.Segment(context.Background(), nil, "person")
	l.mu.Lock()
	first := time.Until(l.until)
	l.mu.Unlock()
	if first < time.Second || first > 2*time.Second {
		t.Errorf("first pause = %s, want ~2s", first)
	}
	// Too long for an urgent wait: fails instead of hanging.
	if _, err := l.Segment(Urgent(context.Background()), nil, "keys"); !errors.Is(err, ErrPaused) {
		t.Errorf("urgent call during a long pause = %v, want ErrPaused", err)
	}
}

func TestFinderSkipsScansWhileBusy(t *testing.T) {
	seg := &fakeSegmenter{text: personLine}
	frames := &fakeFramer{}
	frames.set(makeJPEG(t, 64, 48))
	f := NewFinder(seg, frames, Config{})
	f.Watch = []string{"keys"}
	f.ScanEvery = 10 * time.Millisecond
	f.Busy = func() bool { return true }
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	f.Run(ctx)
	if seg.calls() != 0 {
		t.Errorf("scanned %d times while busy", seg.calls())
	}
}
