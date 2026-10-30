package vision

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakeFramer struct {
	mu    sync.Mutex
	frame []byte
	have  bool
}

func (f *fakeFramer) Latest() ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.frame, f.have
}

func (f *fakeFramer) set(frame []byte) {
	f.mu.Lock()
	f.frame, f.have = frame, frame != nil
	f.mu.Unlock()
}

type fakeSegmenter struct {
	mu   sync.Mutex
	text string
	err  error
	seen int
}

func (s *fakeSegmenter) Segment(_ context.Context, _ []byte, _ string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen++
	return s.text, s.err
}

func (s *fakeSegmenter) set(text string) {
	s.mu.Lock()
	s.text = text
	s.mu.Unlock()
}

func (s *fakeSegmenter) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen
}

type fakeGazer struct {
	mu    sync.Mutex
	gazes [][2]float64
	offs  int
}

func (g *fakeGazer) Gaze(x, y float64) {
	g.mu.Lock()
	g.gazes = append(g.gazes, [2]float64{x, y})
	g.mu.Unlock()
}

func (g *fakeGazer) GazeOff() {
	g.mu.Lock()
	g.offs++
	g.mu.Unlock()
}

func (g *fakeGazer) last() ([2]float64, int, int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var last [2]float64
	if len(g.gazes) > 0 {
		last = g.gazes[len(g.gazes)-1]
	}
	return last, len(g.gazes), g.offs
}

const personLine = `<0f>0<|box;x1=120;y1=60;x2=520;y2=700;w=1280;h=720|><|mask;x=0;y=0;data=360,640,~AA|>`

func waitEvent(t *testing.T, ch chan PresenceChanged, want bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Present == want {
				return
			}
		case <-deadline:
			t.Fatalf("no presence event (want %v) within 3s", want)
		}
	}
}

func TestTrackerFollowsThenReleases(t *testing.T) {
	frames := &fakeFramer{frame: makeJPEG(t, 64, 48), have: true}
	seg := &fakeSegmenter{text: personLine}
	face := &fakeGazer{}
	cfg := Config{
		Prompt:      "person",
		HZ:          100,
		PresentHits: 2,
		AbsentAfter: 120 * time.Millisecond,
	}
	tr := NewTracker(seg, face, frames, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tr.Run(ctx)

	// Two consecutive hits -> present, with gaze at the mapped point.
	waitEvent(t, tr.Events, true)
	if !tr.Present() {
		t.Error("Present() = false after present event")
	}
	last, n, _ := face.last()
	if n == 0 {
		t.Fatal("no gaze sent")
	}
	if last[0] != 0.5 {
		t.Errorf("gaze x = %v, want 0.5 (see TestGazeFor)", last[0])
	}

	// Sightings stop -> absent after the debounce, gaze released.
	seg.set("")
	waitEvent(t, tr.Events, false)
	if tr.Present() {
		t.Error("Present() = true after absent event")
	}
	if _, _, offs := face.last(); offs != 1 {
		t.Errorf("GazeOff calls = %d, want 1", offs)
	}
	cancel()
	select {
	case _, ok := <-tr.Events:
		if ok {
			// drain remaining; channel must close
			for range tr.Events {
			}
		}
	case <-time.After(2 * time.Second):
		t.Error("Events not closed after cancel")
	}
}

func TestTrackerWithoutFramesStaysQuiet(t *testing.T) {
	frames := &fakeFramer{} // camera not up: no frames
	seg := &fakeSegmenter{text: personLine}
	face := &fakeGazer{}
	tr := NewTracker(seg, face, frames, Config{HZ: 100, PresentHits: 1, AbsentAfter: 50 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tr.Run(ctx)
	time.Sleep(120 * time.Millisecond)
	cancel()
	if seg.calls() != 0 {
		t.Errorf("segment calls without frames = %d, want 0", seg.calls())
	}
	if _, n, _ := face.last(); n != 0 {
		t.Errorf("gazes without frames = %d, want 0", n)
	}
	if tr.Present() {
		t.Error("Present() must stay false without frames")
	}
}
