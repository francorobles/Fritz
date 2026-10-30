package vision

import (
	"math"
	"os"
	"testing"
)

// The fixture in testdata/sam_person.txt is synthetic, shaped
// exactly per the reading-segmentation docs (two records, one with
// confidence). Replace it with a real capture: FRITZ_VISION_DEBUG=1
// prints raw SAM lines from the live camera.
func TestParseBoxesFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/sam_person.txt")
	if err != nil {
		t.Fatal(err)
	}
	boxes := ParseBoxes(string(raw))
	if len(boxes) != 2 {
		t.Fatalf("ParseBoxes = %d boxes, want 2", len(boxes))
	}
	b := boxes[0]
	if b.ID != 0 || b.W != 1280 || b.H != 720 {
		t.Errorf("box0 = %+v", b)
	}
	if math.Abs(b.X1-120.0/1280) > 1e-9 || math.Abs(b.Y2-700.0/720) > 1e-9 {
		t.Errorf("box0 normalized = %+v", b)
	}
	if boxes[1].Conf != 0.91 {
		t.Errorf("box1 conf = %v, want 0.91", boxes[1].Conf)
	}
	big, ok := Largest(boxes)
	if !ok || big.ID != 0 {
		t.Errorf("Largest = %+v, %v, want the wide person box", big, ok)
	}
}

func TestParseBoxesEmptyAndGarbage(t *testing.T) {
	if got := ParseBoxes(""); got != nil {
		t.Errorf("empty = %v, want nil", got)
	}
	if got := ParseBoxes("no tokens here"); len(got) != 0 {
		t.Errorf("garbage = %v, want none", got)
	}
	if _, ok := Largest(nil); ok {
		t.Error("Largest(nil) must report not found")
	}
}

func TestGazeFor(t *testing.T) {
	cfg := Config{}
	// Person box centered at (0.25, ~0.53) — left of frame, eye
	// level: eyes look screen-right, slightly down.
	box := Box{X1: 120.0 / 1280, Y1: 60.0 / 720, X2: 520.0 / 1280, Y2: 700.0 / 720, W: 1280, H: 720}
	x, y := GazeFor(box, cfg)
	if math.Abs(x-0.5) > 1e-9 {
		t.Errorf("gaze x = %v, want 0.5 (person image-left -> screen right)", x)
	}
	if want := (380.0/720 - 0.4) * 2; math.Abs(y-want) > 1e-9 {
		t.Errorf("gaze y = %v, want %v", y, want)
	}
	// Mirrored mount flips the sign.
	mx, _ := GazeFor(box, Config{MirrorX: true})
	if mx != -x {
		t.Errorf("mirrored x = %v, want %v", mx, -x)
	}
	// Centered person -> eyes near center; clamped to [-1, 1].
	cx, cy := GazeFor(Box{X1: 0.4, Y1: 0.3, X2: 0.6, Y2: 0.5}, cfg)
	if math.Abs(cx) > 1e-9 || math.Abs(cy-0.0) > 1e-9 {
		t.Errorf("centered gaze = %v, %v, want ~0, 0", cx, cy)
	}
	ex, _ := GazeFor(Box{X1: 0, Y1: 0, X2: 0.01, Y2: 0.01}, cfg)
	if math.Abs(ex-0.99) > 1e-9 {
		t.Errorf("edge gaze x = %v, want 0.99", ex)
	}
	// Out-of-frame coordinates (bad model output) still clamp.
	cx2, _ := GazeFor(Box{X1: -0.5, Y1: 0, X2: 0, Y2: 0.01}, cfg)
	if cx2 != 1 {
		t.Errorf("out-of-frame gaze x = %v, want clamped 1", cx2)
	}
}
