// Command visioncheck runs Fritz's camera face-tracking on its
// own: it captures the C922, segments with SAM 3.1 (~1 Hz), and
// points the eyes at your face, printing every gaze point and
// presence change. Use it to tune FRITZ_VISION_PROMPT ("person"
// vs "face"), FRITZ_GAZE_MIRROR_X/Y, and FRITZ_TRACK_HZ before
// running the full voicecheck.
//
//	MODEL_API_KEY=... go run ./cmd/visioncheck [-seconds 30]
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
