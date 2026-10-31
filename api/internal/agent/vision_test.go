package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/francorobles/fritz/api/internal/vision"
)

type fakeFinder struct {
	s     vision.Sighting
	found bool
	err   error
}

func (f fakeFinder) Find(context.Context, string) (vision.Sighting, bool, error) {
	return f.s, f.found, f.err
}

type fakeEyes struct{ looked [][2]float64 }

func (e *fakeEyes) Look(x, y float64, _ time.Duration) { e.looked = append(e.looked, [2]float64{x, y}) }

func runFind(t *testing.T, f ObjectFinder, eyes Looker) string {
	t.Helper()
	tool := FindObjectTool(f, eyes, vision.Config{})[0]
	out, err := tool.Run(context.Background(), json.RawMessage(`{"name":"keys"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, _ := json.Marshal(out)
	return string(data)
}

func TestFindObjectInViewTurnsEyes(t *testing.T) {
	eyes := &fakeEyes{}
	box := vision.Box{X1: 0.1, Y1: 0.7, X2: 0.2, Y2: 0.9}
	got := runFind(t, fakeFinder{s: vision.Sighting{Name: "keys", Where: "on your right", Box: box, SeenAt: time.Now()}, found: true}, eyes)
	if !strings.Contains(got, `"in_view":true`) || !strings.Contains(got, "on your right") {
		t.Errorf("result = %s", got)
	}
	if len(eyes.looked) != 1 || eyes.looked[0][0] <= 0 {
		t.Errorf("eyes = %v, want one look toward screen-right (image left)", eyes.looked)
	}
}

func TestFindObjectFromMemory(t *testing.T) {
	seen := time.Now().Add(-25 * time.Minute)
	got := runFind(t, fakeFinder{s: vision.Sighting{Name: "keys", Where: "on your right", SeenAt: seen, GoneAt: seen.Add(5 * time.Minute)}}, nil)
	for _, want := range []string{`"in_view":false`, "25 minutes ago", "on your right", "gone_since"} {
		if !strings.Contains(got, want) {
			t.Errorf("result %s missing %q", got, want)
		}
	}
	got = runFind(t, fakeFinder{s: vision.Sighting{Name: "keys"}, err: vision.ErrNoFrame}, nil)
	if !strings.Contains(got, "not available") || !strings.Contains(got, "haven't seen it") {
		t.Errorf("no camera, never seen: %s", got)
	}
}
