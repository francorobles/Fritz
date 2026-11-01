package alerts

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// FlagStore is where the sentry switch lives (memory.Store): set
// by voice or a WhatsApp reply, read here at every arrival.
type FlagStore interface {
	SetState(key, value string) error
	State(key string) (value string, at time.Time, ok bool, err error)
}

// SentryArmed reports whether sentry mode is on, and since when.
func SentryArmed(st FlagStore) (bool, time.Time) {
	v, at, ok, err := st.State(KeySentry)
	return err == nil && ok && v == "on", at
}

// SetSentry turns sentry mode on or off.
func SetSentry(st FlagStore, on bool) error {
	v := "off"
	if on {
		v = "on"
	}
	return st.SetState(KeySentry, v)
}

// PhotoSender delivers a sentry snapshot (Notifier).
type PhotoSender interface {
	Photo(ctx context.Context, jpeg []byte, caption string) error
}

// Framer gives the latest camera frame (vision.FrameSource).
type Framer interface {
	Latest() ([]byte, bool)
}

// Sentry watches for people while the owner is away. Fritz can't
// tell who someone is (no face recognition), so the owner arms it
// on the way out ("Hey Fritz, sentry mode") and turns it off when
// back. Every arrival while armed sends a WhatsApp photo, at most
// once per Cooldown.
type Sentry struct {
	Flags  FlagStore
	Notify PhotoSender
	Frames Framer
	// Grace ignores arrivals right after arming: the owner is
	// usually still in front of the camera. Default 20 s.
	Grace time.Duration
	// MinAway ignores tracking flicker: the room must have been
	// empty this long before an arrival counts. Default 5 s.
	MinAway time.Duration
	// Cooldown is the minimum gap between alerts. Default 2 min.
	Cooldown time.Duration
	// Settle waits before taking the photo, so the person is in
	// the frame rather than at its edge. Default 1 s.
	Settle time.Duration
	Logf   func(string)
	Now    func() time.Time

	mu        sync.Mutex
	leftAt    time.Time
	lastAlert time.Time
}

func (s *Sentry) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Sentry) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(fmt.Sprintf(format, args...))
	}
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// Armed reports whether sentry mode is on.
func (s *Sentry) Armed() bool {
	on, _ := SentryArmed(s.Flags)
	return on
}

// OnPresence takes every presence change from the tracker. It
// reports whether this arrival triggered an alert; the photo is
// taken and sent in the background so tracking never waits on
// WhatsApp.
func (s *Sentry) OnPresence(ctx context.Context, present bool) bool {
	now := s.now()
	s.mu.Lock()
	if !present {
		s.leftAt = now
		s.mu.Unlock()
		return false
	}
	armed, armedAt := SentryArmed(s.Flags)
	switch {
	case !armed:
	case now.Sub(armedAt) < orDefault(s.Grace, 20*time.Second):
		armed = false
	case !s.leftAt.IsZero() && now.Sub(s.leftAt) < orDefault(s.MinAway, 5*time.Second):
		armed = false
	case !s.lastAlert.IsZero() && now.Sub(s.lastAlert) < orDefault(s.Cooldown, 2*time.Minute):
		armed = false
	}
	if armed {
		s.lastAlert = now
	}
	s.mu.Unlock()
	if !armed {
		return false
	}
	go s.send(ctx, now)
	return true
}

func (s *Sentry) send(ctx context.Context, at time.Time) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(orDefault(s.Settle, time.Second)):
	}
	frame, ok := s.Frames.Latest()
	if !ok {
		s.logf("alerts: sentry: no camera frame to send")
		return
	}
	caption := fmt.Sprintf("Someone just came to your desk (%s). Sentry mode is on; reply \"sentry off\" if it's you.",
		at.Format("3:04 PM"))
	sendCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := s.Notify.Photo(sendCtx, frame, caption); err != nil {
		s.logf("alerts: sentry alert: %v", err)
		return
	}
	s.logf("alerts: sentry alert sent")
}
