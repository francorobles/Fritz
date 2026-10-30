package vision

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/image/draw"
)

// FrameSource owns the camera: one long-running ffmpeg copies the
// C922's native MJPEG stream to stdout without re-encoding, and
// the latest complete frame is kept in an atomic slot for every
// consumer (tracking today, look/find_object later). Only one
// process can open the camera — this is it.
type FrameSource struct {
	device string // explicit FRITZ_CAMERA_DEVICE; "" = discover

	// Logf reports camera restarts and failures. May be nil.
	Logf func(string)

	mu     sync.RWMutex
	latest []byte
	have   bool
}

// NewFrameSource creates a source for the given device ("" selects
// the first C922 by-id path, per the fritz-c922 skill).
func NewFrameSource(device string) *FrameSource {
	return &FrameSource{device: device}
}

// Latest returns the newest complete JPEG frame, if any. The slice
// is shared — callers must not mutate it.
func (f *FrameSource) Latest() ([]byte, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.latest, f.have
}

func (f *FrameSource) set(frame []byte) {
	f.mu.Lock()
	f.latest = frame
	f.have = true
	f.mu.Unlock()
}

func (f *FrameSource) logf(format string, args ...any) {
	if f.Logf != nil {
		f.Logf(fmt.Sprintf(format, args...))
	}
}

// Run captures until ctx ends, restarting ffmpeg with backoff
// (1 s → 10 s) when it exits (USB unplug, device busy at start).
// Vision is optional for Fritz — errors are logged, never fatal.
func (f *FrameSource) Run(ctx context.Context) {
	backoff := time.Second
	for {
		err := f.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		f.logf("camera: %v — retrying in %s", err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 10*time.Second {
			backoff = 10 * time.Second
		}
	}
}

func (f *FrameSource) runOnce(ctx context.Context) error {
	device := f.device
	if device == "" {
		device = discoverDevice()
	}
	if device == "" {
		return fmt.Errorf("no camera found (set FRITZ_CAMERA_DEVICE; ls /dev/v4l/by-id/ | grep -i c922)")
	}
	ffmpeg := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "v4l2", "-input_format", "mjpeg", "-video_size", "1280x720",
		"-framerate", "10", "-i", device,
		"-c:v", "copy", "-f", "mjpeg", "-")
	stdout, err := ffmpeg.StdoutPipe()
	if err != nil {
		return err
	}
	if err := ffmpeg.Start(); err != nil {
		return fmt.Errorf("ffmpeg: %w (is ffmpeg installed?)", err)
	}
	splitFrames(stdout, f.set)
	return ffmpeg.Wait()
}

// discoverDevice finds the C922's stable by-id path.
func discoverDevice() string {
	for _, pattern := range []string{
		"/dev/v4l/by-id/*C922*-video-index0",
		"/dev/v4l/by-id/*-video-index0",
	} {
		if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
			return matches[0]
		}
	}
	return ""
}

var (
	jpegSOI = []byte{0xFF, 0xD8}
	jpegEOI = []byte{0xFF, 0xD9}
)

// splitFrames reads an MJPEG stream and yields each complete JPEG
// frame (SOI … EOI). Incomplete tails are kept across reads; a
// buffer that grows past 4 MB without an end marker is resynced to
// the next start marker (a corrupt frame must not stall the loop).
func splitFrames(r io.Reader, yield func([]byte)) {
	var buf []byte
	chunk := make([]byte, 64*1024)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			for {
				start := bytes.Index(buf, jpegSOI)
				if start < 0 {
					// Keep a trailing 0xFF: it may be half a marker.
					if len(buf) > 1 {
						buf = buf[len(buf)-1:]
					}
					break
				}
				end := bytes.Index(buf[start+2:], jpegEOI)
				if end < 0 {
					buf = buf[start:]
					if len(buf) > 4<<20 {
						buf = buf[:0]
					}
					break
				}
				frameEnd := start + 2 + end + 2
				frame := make([]byte, frameEnd-start)
				copy(frame, buf[start:frameEnd])
				yield(frame)
				buf = buf[frameEnd:]
			}
		}
		if err != nil {
			return
		}
	}
}

// DownscaleJPEG shrinks a JPEG frame to targetWidth pixels wide
// (height proportional), re-encoded at quality 80 — the size SAM
// tracking runs on, cheap at 1–2 Hz on the Pi. Frames already at or
// below the width pass through untouched.
func DownscaleJPEG(src []byte, targetWidth int) ([]byte, error) {
	img, err := jpeg.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("vision: decode frame: %w", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() <= targetWidth {
		return src, nil
	}
	scale := float64(targetWidth) / float64(bounds.Dx())
	dst := image.NewRGBA(image.Rect(0, 0, targetWidth, int(float64(bounds.Dy())*scale)))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, bounds, draw.Src, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 80}); err != nil {
		return nil, fmt.Errorf("vision: encode frame: %w", err)
	}
	return buf.Bytes(), nil
}

// writeDebugFrame saves a frame for debugging (opt-in only).
func writeDebugFrame(dir string, jpegBytes []byte) {
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "latest.jpg"), jpegBytes, 0o644)
}
