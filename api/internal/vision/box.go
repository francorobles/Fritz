package vision

import (
	"regexp"
	"strconv"
)

// Box is one detected object: normalized coordinates in the frame
// it came from (0..1), from SAM's pixel box. Width/Height are the
// source frame's pixels, kept for debugging.
type Box struct {
	ID   int // SAM object id (stable per response)
	X1   float64
	Y1   float64
	X2   float64
	Y2   float64
	W    int
	H    int
	Conf float64 // 0 unless the request asked for confidence
}

// Center returns the box center, normalized.
func (b Box) Center() (cx, cy float64) {
	return (b.X1 + b.X2) / 2, (b.Y1 + b.Y2) / 2
}

// Area returns the normalized box area.
func (b Box) Area() float64 {
	return (b.X2 - b.X1) * (b.Y2 - b.Y1)
}

// boxTokenRe extracts one `<id><|box;...|>` record's geometry. The
// mask token that follows each box is deliberately not captured —
// gaze only needs the box, and mask payloads are base85 blobs that
// can contain almost any byte. See the reading-segmentation docs.
var boxTokenRe = regexp.MustCompile(`(\d+)<\|box;x1=(\d+);y1=(\d+);x2=(\d+);y2=(\d+);w=(\d+);h=(\d+)(?:;c=([0-9.]+))?\|>`)

// ParseBoxes reads SAM's output_text lane (one line per frame,
// boxes inline as special tokens) into normalized boxes. Empty
// input — zero matches — returns nil, as does unparseable text:
// callers treat "no boxes" as "nobody there".
func ParseBoxes(text string) []Box {
	matches := boxTokenRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	boxes := make([]Box, 0, len(matches))
	for _, m := range matches {
		id, _ := strconv.Atoi(m[1])
		px := func(s string) int { n, _ := strconv.Atoi(s); return n }
		w, h := px(m[6]), px(m[7])
		if w <= 0 || h <= 0 {
			continue
		}
		conf, _ := strconv.ParseFloat(m[8], 64) // "" parses to 0
		boxes = append(boxes, Box{
			ID:   id,
			X1:   float64(px(m[2])) / float64(w),
			Y1:   float64(px(m[3])) / float64(h),
			X2:   float64(px(m[4])) / float64(w),
			Y2:   float64(px(m[5])) / float64(h),
			W:    w,
			H:    h,
			Conf: conf,
		})
	}
	return boxes
}

// Largest returns the biggest box by area — for face following,
// the nearest or only person in frame.
func Largest(boxes []Box) (Box, bool) {
	if len(boxes) == 0 {
		return Box{}, false
	}
	best := boxes[0]
	for _, b := range boxes[1:] {
		if b.Area() > best.Area() {
			best = b
		}
	}
	return best, true
}

// GazeFor maps a detected box to face gaze coordinates in [-1, 1]
// (+x = screen right, +y = down). The camera faces the user like
// the screen does, so a person on the left of the IMAGE stands on
// the device's right — screen-right from where the user sits:
// gaze_x = (0.5-cx)*2. Vertically we aim near eye level (cy-0.4)
// rather than the box center. The mirror flags flip an axis for
// odd camera mountings; verify with a live left/right test.
func GazeFor(b Box, cfg Config) (x, y float64) {
	cx, cy := b.Center()
	x = (0.5 - cx) * 2
	y = (cy - 0.4) * 2
	if cfg.MirrorX {
		x = -x
	}
	if cfg.MirrorY {
		y = -y
	}
	return clampUnit(x), clampUnit(y)
}

func clampUnit(v float64) float64 {
	if v < -1 {
		return -1
	}
	if v > 1 {
		return 1
	}
	return v
}
