// Package vision is Fritz's camera work: capturing frames from the
// C922, finding the user with SAM 3.1, and pointing the face at
// them. See the fritz-vision and fritz-c922 skills. Frames stay in
// memory; nothing is written to disk except under
// FRITZ_DEBUG_FRAMES for debugging.
package vision

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config tunes the tracking loop. Zero values get defaults in
// NewTracker / RunTracker; use ConfigFromEnv for the real thing.
type Config struct {
	// Prompt is the SAM text prompt. "person" gives reliable boxes;
	// "face" is tighter when it works — try both on real frames.
	Prompt string
	// HZ is segmentation calls per second (default 1, max 2 — the
	// API rate limit for vision).
	HZ float64
	// MirrorX / MirrorY flip the gaze axes for cameras mounted
	// upside-down or with --hflip (FRITZ_GAZE_MIRROR_X/Y).
	MirrorX bool
	MirrorY bool
	// PresentHits is consecutive hits needed before "present"
	// (default 2, debounces single-frame false positives).
	PresentHits int
	// AbsentAfter is how long without a hit before "absent"
	// (default 10 s).
	AbsentAfter time.Duration
	// DebugFrames, when set, is a directory where each frame sent
	// to SAM is also written as latest.jpg (debugging only).
	DebugFrames string
	// DebugLog prints the raw SAM output per call (for capturing a
	// parser fixture from a real frame).
	DebugLog bool
}

func (c *Config) withDefaults() {
	if c.Prompt == "" {
		c.Prompt = "person"
	}
	if c.HZ <= 0 {
		c.HZ = 1
	}
	if c.HZ > 2 {
		c.HZ = 2
	}
	if c.PresentHits <= 0 {
		c.PresentHits = 2
	}
	if c.AbsentAfter <= 0 {
		c.AbsentAfter = 10 * time.Second
	}
}

// ConfigFromEnv reads the FRITZ_* vision settings.
func ConfigFromEnv() Config {
	var cfg Config
	cfg.Prompt = os.Getenv("FRITZ_VISION_PROMPT")
	if hz, err := strconv.ParseFloat(os.Getenv("FRITZ_TRACK_HZ"), 64); err == nil {
		cfg.HZ = hz
	}
	cfg.MirrorX = truthy(os.Getenv("FRITZ_GAZE_MIRROR_X"))
	cfg.MirrorY = truthy(os.Getenv("FRITZ_GAZE_MIRROR_Y"))
	cfg.DebugFrames = os.Getenv("FRITZ_DEBUG_FRAMES")
	cfg.DebugLog = os.Getenv("FRITZ_VISION_DEBUG") == "1"
	return cfg
}

// CameraOff reports whether FRITZ_CAMERA=off disables the camera.
func CameraOff() bool {
	return strings.EqualFold(os.Getenv("FRITZ_CAMERA"), "off")
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
