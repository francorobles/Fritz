package vision

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"
)

func makeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSplitFrames(t *testing.T) {
	f1 := append(append([]byte{0xFF, 0xD8}, []byte("frame-one")...), 0xFF, 0xD9)
	f2 := append(append([]byte{0xFF, 0xD8}, []byte("two")...), 0xFF, 0xD9)
	stream := append(append([]byte("noise"), f1...), f2...)
	stream = append(stream, 0xFF, 0xD8, 'p', 'a') // partial third frame: not yielded
	var got [][]byte
	splitFrames(strings.NewReader(string(stream)), func(f []byte) { got = append(got, f) })
	if len(got) != 2 {
		t.Fatalf("splitFrames yielded %d frames, want 2", len(got))
	}
	if !bytes.Contains(got[0], []byte("frame-one")) || !bytes.Contains(got[1], []byte("two")) {
		t.Errorf("frames = %q, %q", got[0], got[1])
	}
	if !bytes.HasPrefix(got[0], jpegSOI) || !bytes.HasSuffix(got[0], jpegEOI) {
		t.Errorf("frame markers wrong: % x ... % x", got[0][:2], got[0][len(got[0])-2:])
	}
}

func TestSplitFramesAcrossReads(t *testing.T) {
	frame := append(append([]byte{0xFF, 0xD8}, bytes.Repeat([]byte("x"), 70000)...), 0xFF, 0xD9)
	var got [][]byte
	splitFrames(bytes.NewReader(frame), func(f []byte) { got = append(got, f) })
	if len(got) != 1 || len(got[0]) != len(frame) {
		t.Errorf("got %d frames of %v, want one %d-byte frame", len(got), lens(got), len(frame))
	}
}

func lens(frames [][]byte) []int {
	out := make([]int, len(frames))
	for i, f := range frames {
		out[i] = len(f)
	}
	return out
}

func TestDownscaleJPEG(t *testing.T) {
	big := makeJPEG(t, 1280, 720)
	small, err := DownscaleJPEG(big, 640)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(small))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 640 || img.Bounds().Dy() != 360 {
		t.Errorf("downscaled = %v, want 640x360", img.Bounds())
	}
	// Already-small frames pass through untouched.
	tiny := makeJPEG(t, 320, 240)
	out, err := DownscaleJPEG(tiny, 640)
	if err != nil || !bytes.Equal(out, tiny) {
		t.Errorf("small frame: err=%v equal=%v, want passthrough", err, bytes.Equal(out, tiny))
	}
	if _, err := DownscaleJPEG([]byte("not a jpeg"), 640); err == nil {
		t.Error("garbage input must error")
	}
}
