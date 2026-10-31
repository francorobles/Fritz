// Command visioncheck runs Fritz's camera face-tracking on its
// own: it captures the C922, segments with SAM 3.1 (~1 Hz), and
// points the eyes at your face, printing every gaze point and
// presence change. Use it to tune FRITZ_VISION_PROMPT ("person"
// vs "face"), FRITZ_GAZE_MIRROR_X/Y, and FRITZ_TRACK_HZ before
// running the full voicecheck.
//
//	MODEL_API_KEY=... go run ./cmd/visioncheck [-seconds 30]
//	MODEL_API_KEY=... go run ./cmd/visioncheck -find keys
//
// -find runs one SAM search for an object (what find_object does),
// prints where it is from your seat, and points the eyes at it.
//
// FRITZ_VISION_DEBUG=1 prints the raw SAM output per call (the
// easiest way to capture a parser fixture). One process owns the
// camera — stop voicecheck first.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/francorobles/fritz/api/internal/face"
	"github.com/francorobles/fritz/api/internal/meta"
	"github.com/francorobles/fritz/api/internal/vision"
)

type silentGaze struct{}

func (silentGaze) Gaze(x, y float64) {}
func (silentGaze) GazeOff()          {}

func main() {
	seconds := flag.Int("seconds", 0, "stop after N seconds (0 = until Ctrl-C)")
	find := flag.String("find", "", "look for one object (e.g. keys) instead of tracking, then exit")
	flag.Parse()

	apiKey := os.Getenv("MODEL_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "MODEL_API_KEY is unset — export it first")
		os.Exit(1)
	}
	if vision.CameraOff() {
		fmt.Fprintln(os.Stderr, "FRITZ_CAMERA=off — unset it to check the camera")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *seconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(*seconds)*time.Second)
		defer cancel()
	}

	var gazer vision.Gazer = silentGaze{}
	if fc, err := face.New(face.DefaultAddr); err == nil {
		gazer = fc
		defer fc.Close()
	} else {
		fmt.Printf("note: face client unavailable (%v); printing gazes only\n", err)
	}

	cfg := vision.ConfigFromEnv()
	frames := vision.NewFrameSource(os.Getenv("FRITZ_CAMERA_DEVICE"))
	frames.Logf = func(msg string) { fmt.Println(msg) }
	go frames.Run(ctx)

	seg := &meta.Client{APIKey: apiKey}
	if *find != "" {
		findOnce(ctx, seg, gazer, frames, cfg, *find)
		return
	}
	follow := vision.NewTracker(seg, gazer, frames, cfg)
	follow.Logf = func(msg string) { fmt.Println(msg) }
	follow.OnGaze = func(x, y float64) {
		fmt.Printf("gaze %+.2f %+.2f\n", x, y)
	}
	go func() {
		for ev := range follow.Events {
			if ev.Present {
				fmt.Println("== user present — eyes following")
			} else {
				fmt.Println("== user absent — eyes released")
			}
		}
	}()

	fmt.Printf("visioncheck: tracking prompt %q at %.1f Hz — move around, Ctrl-C to stop\n", cfg.Prompt, cfg.HZ)
	follow.Run(ctx) // blocks until Ctrl-C or -seconds
	fmt.Println("visioncheck: stopped")
}

// findOnce waits for a camera frame, runs one object search, and
// holds the eyes on the result for a few seconds.
func findOnce(ctx context.Context, seg vision.Segmenter, gazer vision.Gazer, frames *vision.FrameSource, cfg vision.Config, name string) {
	for {
		if _, ok := frames.Latest(); ok {
			break
		}
		select {
		case <-ctx.Done():
			fmt.Println("visioncheck: no camera frame")
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	finder := vision.NewFinder(seg, frames, cfg)
	start := time.Now()
	s, found, err := finder.Find(ctx, name)
	took := time.Since(start).Round(time.Millisecond)
	switch {
	case err != nil:
		fmt.Printf("find %q: %v\n", name, err)
	case !found:
		fmt.Printf("find %q: not in view (%s) — try a simpler noun, better light, or FRITZ_VISION_DEBUG=1\n", name, took)
	default:
		x, y := vision.LookAt(s.Box, cfg)
		fmt.Printf("find %q: %s (box %.2f,%.2f–%.2f,%.2f, eyes %+.2f %+.2f, %s)\n",
			name, s.Where, s.Box.X1, s.Box.Y1, s.Box.X2, s.Box.Y2, x, y, took)
		// The face drops a gaze after 1.5 s without a command.
		for i := 0; i < 8; i++ {
			gazer.Gaze(x, y)
			time.Sleep(500 * time.Millisecond)
		}
		gazer.GazeOff()
	}
}
