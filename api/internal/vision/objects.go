package vision

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Sighting is where an object was last seen. GoneAt is set when a
// later scan stopped finding it (it moved, or left with someone),
// and cleared when it is seen again.
type Sighting struct {
	Name   string
	SeenAt time.Time
	Box    Box
	Where  string // "on your left, near the front of the desk"
	GoneAt time.Time
}

// Finder answers "where are my keys?": it runs SAM on the latest
// frame for a named object, and between questions scans a short
// watch list in the background so it can still answer after the
// object has left the frame ("I last saw them at 3:40 on your
// left"). One object per scan tick keeps the SAM load small next to
// the 1 Hz face tracking (fritz-vision: ≤ 2 requests/s in total).
type Finder struct {
	seg    Segmenter
	frames Framer
	cfg    Config

	// Watch lists the objects scanned in the background.
	Watch []string
	// ScanEvery is the pause between background scans (each scan
	// checks the next watched object). Default 10 s.
	ScanEvery time.Duration
	// OnUpdate, when set, is called with every changed sighting
	// (persistence). May be nil.
	OnUpdate func(Sighting)
	// Logf reports scan errors and sightings. May be nil.
	Logf func(string)
	// Busy, when set, skips background scans while it returns true
	// (Fritz is answering): the reply's own calls come first.
	Busy func() bool

	mu   sync.Mutex
	last map[string]Sighting
}

// DefaultWatch is the watch list when FRITZ_WATCH_OBJECTS is unset:
// the things people lose on a desk.
var DefaultWatch = []string{"keys", "wallet", "glasses", "phone"}

// WatchFromEnv reads FRITZ_WATCH_OBJECTS ("keys,wallet"; "off"
// disables background scans).
func WatchFromEnv() []string {
	v := strings.TrimSpace(os.Getenv("FRITZ_WATCH_OBJECTS"))
	switch {
	case v == "":
		return DefaultWatch
	case strings.EqualFold(v, "off"):
		return nil
	}
	var out []string
	for _, name := range strings.Split(v, ",") {
		if name = normalizeName(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// NewFinder builds a Finder; cfg supplies the mirror flags used to
// describe and look at locations.
func NewFinder(seg Segmenter, frames Framer, cfg Config) *Finder {
	cfg.withDefaults()
	return &Finder{seg: seg, frames: frames, cfg: cfg, last: map[string]Sighting{}}
}

// Restore loads remembered sightings (from the store at startup).
func (f *Finder) Restore(sightings []Sighting) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range sightings {
		f.last[normalizeName(s.Name)] = s
	}
}

func (f *Finder) logf(format string, args ...any) {
	if f.Logf != nil {
		f.Logf(fmt.Sprintf(format, args...))
	}
}

func normalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, prefix := range []string{"my ", "the ", "a ", "an "} {
		name = strings.TrimPrefix(name, prefix)
	}
	return strings.TrimSpace(name)
}

// ErrNoFrame means the camera has not produced a frame.
var ErrNoFrame = fmt.Errorf("vision: no camera frame yet")

// Find looks for name in the current frame. found reports whether it
// is visible right now; either way the returned sighting is the
// latest known one (zero SeenAt: never seen).
func (f *Finder) Find(ctx context.Context, name string) (s Sighting, found bool, err error) {
	name = normalizeName(name)
	if name == "" {
		return Sighting{}, false, fmt.Errorf("vision: empty object name")
	}
	box, ok, err := f.look(Urgent(ctx), name)
	if err != nil {
		return f.LastSeen(name), false, err
	}
	return f.record(name, box, ok, time.Now()), ok, nil
}

// LastSeen returns the remembered sighting (zero SeenAt: never).
func (f *Finder) LastSeen(name string) Sighting {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.last[normalizeName(name)]
	if s.Name == "" {
		s.Name = normalizeName(name)
	}
	return s
}

// look runs SAM for name on the latest frame and returns the most
// confident (largest) match.
func (f *Finder) look(ctx context.Context, name string) (Box, bool, error) {
	frame, ok := f.frames.Latest()
	if !ok {
		return Box{}, false, ErrNoFrame
	}
	small, err := DownscaleJPEG(frame, 640)
	if err != nil {
		return Box{}, false, err
	}
	raw, err := f.seg.Segment(ctx, small, name)
	if err != nil {
		return Box{}, false, fmt.Errorf("vision: segment %q: %w", name, err)
	}
	if f.cfg.DebugLog {
		f.logf("vision: sam %q -> %s", name, raw)
	}
	box, found := Largest(ParseBoxes(raw))
	return box, found, nil
}

// record folds one scan result into memory and returns the current
// sighting. A miss only matters for something seen before: it marks
// the moment it went missing (once).
func (f *Finder) record(name string, box Box, found bool, now time.Time) Sighting {
	f.mu.Lock()
	prev := f.last[name]
	s := prev
	s.Name = name
	changed := false
	if found {
		s.SeenAt, s.Box, s.Where, s.GoneAt = now, box, Describe(box, f.cfg), time.Time{}
		changed = true
	} else if !prev.SeenAt.IsZero() && prev.GoneAt.IsZero() {
		s.GoneAt = now
		changed = true
	}
	if changed {
		f.last[name] = s
	}
	f.mu.Unlock()
	if changed && f.OnUpdate != nil {
		f.OnUpdate(s)
	}
	if found && (prev.SeenAt.IsZero() || !prev.GoneAt.IsZero()) {
		f.logf("vision: spotted %s %s", name, s.Where)
	}
	return s
}

// Run scans the watch list until ctx ends, one object per tick. Like
// the tracker it never fails: errors are logged and retried.
func (f *Finder) Run(ctx context.Context) {
	if len(f.Watch) == 0 {
		return
	}
	every := f.ScanEvery
	if every <= 0 {
		every = 10 * time.Second
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if f.Busy != nil && f.Busy() {
			i-- // don't skip this object, just this tick
			continue
		}
		name := normalizeName(f.Watch[i%len(f.Watch)])
		box, found, err := f.look(ctx, name)
		if err != nil {
			if !errors.Is(err, ErrNoFrame) && !errors.Is(err, ErrPaused) && ctx.Err() == nil {
				f.logf("%v", err)
			}
			continue
		}
		f.record(name, box, found, time.Now())
	}
}

// Describe says where a box is from the user's seat. The camera
// faces the user, so the image's left is the user's right (unless
// the image is mirrored); lower in the frame is nearer the front of
// the desk.
func Describe(b Box, cfg Config) string {
	cx, cy := b.Center()
	userRight := cx < 0.5
	if cfg.MirrorX {
		userRight = !userRight
	}
	side := "right in front of you"
	switch {
	case cx < 0.35 || cx > 0.65:
		if userRight {
			side = "on your right"
		} else {
			side = "on your left"
		}
	}
	switch {
	case cy > 0.66:
		return side + ", near the front of the desk"
	case cy < 0.33:
		return side + ", up high"
	}
	return side
}

// LookAt maps an object's box to face gaze: the box center, with
// the same mirroring as GazeFor (which aims at a person's eye level
// instead).
func LookAt(b Box, cfg Config) (x, y float64) {
	cx, cy := b.Center()
	x = (0.5 - cx) * 2
	y = (cy - 0.5) * 2
	if cfg.MirrorX {
		x = -x
	}
	if cfg.MirrorY {
		y = -y
	}
	return clampUnit(x), clampUnit(y)
}
