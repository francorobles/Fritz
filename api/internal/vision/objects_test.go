package vision

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// keysLeft is a box on the image's left third, low in the frame:
// from the user's seat that is their right, near the front.
const keysLeft = `<0f>0<|box;x1=100;y1=560;x2=260;y2=680;w=1280;h=720|><|mask;x=0;y=0;data=1,1,~AA|>`

// nameSegmenter answers per prompt, so one fake serves the watch
// list.
type nameSegmenter struct {
	mu      sync.Mutex
	answers map[string]string
	prompts []string
}

func (s *nameSegmenter) Segment(_ context.Context, _ []byte, prompt string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompts = append(s.prompts, prompt)
	return s.answers[prompt], nil
}

func (s *nameSegmenter) set(prompt, text string) {
	s.mu.Lock()
	s.answers[prompt] = text
	s.mu.Unlock()
}

func TestFinderRemembersLastSighting(t *testing.T) {
	frames := &fakeFramer{frame: makeJPEG(t, 64, 48), have: true}
	seg := &nameSegmenter{answers: map[string]string{"keys": keysLeft}}
	var updates []Sighting
	f := NewFinder(seg, frames, Config{})
	f.OnUpdate = func(s Sighting) { updates = append(updates, s) }

	s, found, err := f.Find(context.Background(), "my Keys")
	if err != nil || !found {
		t.Fatalf("Find = %+v found=%v err=%v", s, found, err)
	}
	if s.Name != "keys" || s.Where != "on your right, near the front of the desk" || s.SeenAt.IsZero() {
		t.Errorf("sighting = %+v", s)
	}

	// The keys leave the frame: still remembered, now marked gone
	// (once — a second miss changes nothing).
	seg.set("keys", "")
	s, found, _ = f.Find(context.Background(), "keys")
	if found || s.SeenAt.IsZero() || s.GoneAt.IsZero() || s.Where == "" {
		t.Errorf("after leaving: found=%v sighting=%+v, want last place + gone time", found, s)
	}
	_, _, _ = f.Find(context.Background(), "keys")
	if len(updates) != 2 {
		t.Errorf("OnUpdate called %d times, want 2 (seen, gone)", len(updates))
	}

	// Seen again: gone time clears.
	seg.set("keys", keysLeft)
	if s, found, _ := f.Find(context.Background(), "keys"); !found || !s.GoneAt.IsZero() {
		t.Errorf("seen again: found=%v sighting=%+v", found, s)
	}

	if s := f.LastSeen("umbrella"); !s.SeenAt.IsZero() || s.Name != "umbrella" {
		t.Errorf("never seen = %+v", s)
	}
}

func TestFinderNoCamera(t *testing.T) {
	f := NewFinder(&nameSegmenter{answers: map[string]string{}}, &fakeFramer{}, Config{})
	f.Restore([]Sighting{{Name: "Wallet", SeenAt: time.Now(), Where: "on your left"}})
	s, found, err := f.Find(context.Background(), "wallet")
	if err != ErrNoFrame || found || s.Where != "on your left" {
		t.Errorf("Find without camera = %+v %v %v, want the remembered sighting and ErrNoFrame", s, found, err)
	}
}

func TestFinderScansWatchList(t *testing.T) {
	frames := &fakeFramer{frame: makeJPEG(t, 64, 48), have: true}
	seg := &nameSegmenter{answers: map[string]string{"wallet": keysLeft}}
	f := NewFinder(seg, frames, Config{})
	f.Watch, f.ScanEvery = []string{"keys", "wallet"}, 5*time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go f.Run(ctx)
	for f.LastSeen("wallet").SeenAt.IsZero() {
		if ctx.Err() != nil {
			t.Fatal("watch scan never recorded the wallet")
		}
		time.Sleep(5 * time.Millisecond)
	}
	seg.mu.Lock()
	joined := strings.Join(seg.prompts, ",")
	seg.mu.Unlock()
	if !strings.HasPrefix(joined, "keys,wallet") {
		t.Errorf("scan order = %s, want the watch list in turn", joined)
	}
}

func TestDescribeAndLookAt(t *testing.T) {
	center := Box{X1: 0.45, Y1: 0.45, X2: 0.55, Y2: 0.55}
	if got := Describe(center, Config{}); got != "right in front of you" {
		t.Errorf("Describe(center) = %q", got)
	}
	imageRight := Box{X1: 0.8, Y1: 0.1, X2: 0.9, Y2: 0.2}
	if got := Describe(imageRight, Config{}); got != "on your left, up high" {
		t.Errorf("Describe(image right, high) = %q", got)
	}
	if got := Describe(imageRight, Config{MirrorX: true}); got != "on your right, up high" {
		t.Errorf("mirrored Describe = %q", got)
	}
	// Image-right is screen-left from the user's seat: negative x,
	// matching GazeFor's convention.
	if x, y := LookAt(imageRight, Config{}); x >= 0 || y >= 0 {
		t.Errorf("LookAt(image right, high) = %v,%v, want left and up", x, y)
	}
}

func TestTrackerLookHoldsGaze(t *testing.T) {
	frames := &fakeFramer{frame: makeJPEG(t, 64, 48), have: true}
	face := &fakeGazer{}
	tr := NewTracker(&fakeSegmenter{text: personLine}, face, frames, Config{HZ: 2})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr.Look(-0.7, 0.3, time.Hour)
	go tr.Run(ctx)
	waitEvent(t, tr.Events, true)
	time.Sleep(50 * time.Millisecond)
	if last, _, _ := face.last(); last != [2]float64{-0.7, 0.3} {
		t.Errorf("gaze while holding = %v, want the held point, not the person", last)
	}
}
