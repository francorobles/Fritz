package vision

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Segmenter finds text-prompted objects in a JPEG frame and returns
// SAM's raw output text (meta.Client implements it).
type Segmenter interface {
	Segment(ctx context.Context, jpeg []byte, prompt string) (string, error)
}

// Gazer points the face (face.Client implements it).
type Gazer interface {
	Gaze(x, y float64)
	GazeOff()
}

// Framer supplies the latest camera frame (FrameSource implements it).
type Framer interface {
	Latest() ([]byte, bool)
}

// PresenceChanged is emitted when the user arrives or leaves:
// present after Config.PresentHits consecutive sightings, absent
// after Config.AbsentAfter without one.
type PresenceChanged struct {
	Present bool
}

// Tracker follows the user's face with the eyes: at Config.HZ it
// segments the latest frame, aims the face at the largest matching
// box, and debounces presence. The face itself eases between our
// 1–2 Hz updates (0.15 s time constant) and drops the override
// 1.5 s after the last command, so a stalled loop never leaves the
// eyes stuck. Construct with NewTracker; run Run in a goroutine.
type Tracker struct {
	seg    Segmenter
	face   Gazer
	frames Framer
	cfg    Config

	// Events carries presence transitions (buffered; closes when
	// Run returns).
	Events chan PresenceChanged
	// Logf reports segmenter errors and presence changes (one line
	// per change, not per frame). May be nil.
	Logf func(string)
	// OnGaze, when set, is called with every smoothed gaze point
	// sent to the face (visioncheck prints these). May be nil.
	OnGaze func(x, y float64)

	present atomic.Bool

	// A held gaze ("look at the keys") overrides following until
	// holdUntil; see Look.
	holdMu    sync.Mutex
	holdX     float64
	holdY     float64
	holdUntil time.Time
}

// Look turns the eyes to (x, y) for d, overriding face following —
// how find_object points at what it found. Presence tracking keeps
// running underneath.
func (t *Tracker) Look(x, y float64, d time.Duration) {
	t.holdMu.Lock()
	t.holdX, t.holdY, t.holdUntil = clampUnit(x), clampUnit(y), time.Now().Add(d)
	t.holdMu.Unlock()
	t.face.Gaze(x, y)
}

// held returns the active gaze override, if any.
func (t *Tracker) held() (x, y float64, ok bool) {
	t.holdMu.Lock()
	defer t.holdMu.Unlock()
	return t.holdX, t.holdY, time.Now().Before(t.holdUntil)
}

// NewTracker wires a tracker to its frame source, segmenter, and
// face. vision.ConfigFromEnv supplies the usual Config.
func NewTracker(seg Segmenter, face Gazer, frames Framer, cfg Config) *Tracker {
	cfg.withDefaults()
	return &Tracker{
		seg:    seg,
		face:   face,
		frames: frames,
		cfg:    cfg,
		Events: make(chan PresenceChanged, 4),
	}
}

// Present reports whether the user is currently tracked as present.
func (t *Tracker) Present() bool { return t.present.Load() }

func (t *Tracker) logf(format string, args ...any) {
	if t.Logf != nil {
		t.Logf(fmt.Sprintf(format, args...))
	}
}

// Run tracks until ctx ends. It never returns an error: camera and
// API failures are logged and retried next tick, because vision is
// an enhancement — voice keeps working without it.
func (t *Tracker) Run(ctx context.Context) {
	defer close(t.Events)
	period := time.Duration(float64(time.Second) / t.cfg.HZ)
	ticker := time.NewTicker(period)
	defer ticker.Stop()

	var (
		hits     int       // consecutive sightings
		lastHit  time.Time // last successful sighting
		sx, sy   float64   // smoothed gaze (guarded by gmu: the
		smoothed bool      // refresher below reads it)
		gmu      sync.Mutex
	)
	emit := func(present bool) {
		t.present.Store(present)
		select {
		case t.Events <- PresenceChanged{Present: present}:
		default: // a full buffer means nobody is listening; don't stall tracking
		}
	}

	tick := func() {
		frame, ok := t.frames.Latest()
		if !ok {
			return // camera not up yet; absence clock handles the rest
		}
		small, err := DownscaleJPEG(frame, 640)
		if err != nil {
			t.logf("vision: %v", err)
			return
		}
		if t.cfg.DebugFrames != "" {
			writeDebugFrame(t.cfg.DebugFrames, small)
		}
		raw, err := t.seg.Segment(ctx, small, t.cfg.Prompt)
		if err != nil {
			t.logf("vision: segment %q: %v", t.cfg.Prompt, err)
			return
		}
		if t.cfg.DebugLog {
			t.logf("vision: sam %q -> %s", t.cfg.Prompt, raw)
		}
		box, found := Largest(ParseBoxes(raw))
		now := time.Now()
		if found {
			hits++
			lastHit = now
			gx, gy := GazeFor(box, t.cfg)
			gmu.Lock()
			if !smoothed {
				sx, sy, smoothed = gx, gy, true
			} else {
				// Light EMA: the face eases too, but raw box
				// jitter at 1 Hz still reads as twitchiness.
				sx += 0.5 * (gx - sx)
				sy += 0.5 * (gy - sy)
			}
			curX, curY := sx, sy
			gmu.Unlock()
			if _, _, holding := t.held(); !holding {
				t.face.Gaze(curX, curY)
				if t.OnGaze != nil {
					t.OnGaze(curX, curY)
				}
			}
			if !t.present.Load() && hits >= t.cfg.PresentHits {
				t.logf("vision: user present")
				emit(true)
			}
			return
		}
		hits = 0
		if t.present.Load() && !lastHit.IsZero() && now.Sub(lastHit) > t.cfg.AbsentAfter {
			t.logf("vision: user absent")
			gmu.Lock()
			smoothed = false
			gmu.Unlock()
			emit(false)
			if _, _, holding := t.held(); !holding {
				t.face.GazeOff()
			}
		}
	}

	// The face drops the gaze override 1.5 s after the last
	// command, but a SAM round-trip can take longer than that —
	// without refreshes the eyes keep falling back to standby
	// wander between sightings and never look like they're
	// following you. Re-send the last known point meanwhile.
	go func() {
		refresh := time.NewTicker(700 * time.Millisecond)
		defer refresh.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-refresh.C:
				if hx, hy, holding := t.held(); holding {
					t.face.Gaze(hx, hy)
					continue
				}
				gmu.Lock()
				x, y, ok := sx, sy, smoothed
				gmu.Unlock()
				if ok && t.present.Load() {
					t.face.Gaze(x, y)
				}
			}
		}
	}()

	// Track immediately, then on the ticker.
	tick()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick()
		}
	}
}
