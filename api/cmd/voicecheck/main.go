// Command voicecheck streams the microphone to Muse Voice Transcribe
// in realtime and prints what Fritz hears. It is the first manual test
// for the voice pipeline on the Pi (see the fritz-voice skill):
//
//	MODEL_API_KEY=... go run ./cmd/voicecheck
//
// Speak normally; lines show partials as they arrive, completed turns,
// and ">> REQUEST" when the wake-phrase tracker would hand a turn to
// the agent (say "Hi Fritz", then ask something within 8 s without the
// wake phrase). Ctrl-C stops. No audio leaves the process except to
// the transcription API, and nothing is recorded to disk.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"time"

	"github.com/francorobles/fritz/api/internal/face"
	"github.com/francorobles/fritz/api/internal/voice"
)

func main() {
	apiKey := os.Getenv("MODEL_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "MODEL_API_KEY is unset — export it first")
		os.Exit(1)
	}
	mic := os.Getenv("FRITZ_MIC")
	if mic == "" {
		mic = "pipewire"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	arecord := exec.CommandContext(ctx, "arecord",
		"-D", mic, "-f", "S16_LE", "-r", "16000", "-c", "1", "-t", "raw", "-q", "-")
	audio, err := arecord.StdoutPipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, "arecord:", err)
		os.Exit(1)
	}
	if err := arecord.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "arecord (is alsa-utils installed?): %v\n", err)
		os.Exit(1)
	}
	defer arecord.Wait()

	faceClient, _ := face.New(face.DefaultAddr)
	if faceClient != nil {
		defer faceClient.Close()
	}

	fmt.Printf("voicecheck: streaming mic (%s) — say \"Hi Fritz\", Ctrl-C to stop\n", mic)
	tracker := &voice.Tracker{}
	collector := &voice.TurnCollector{}
	debug := os.Getenv("FRITZ_VOICE_DEBUG") == "1"
	client := &voice.Client{APIKey: apiKey}
	err = client.Run(ctx, audio, func(event voice.Event) {
		collector.Observe(event)
		if debug {
			fmt.Printf("[event %s turn=%s final=%v transcript=%q session=%q]\n",
				event.Type, event.TurnID, event.Final, event.Transcript, event.SessionID)
		}
		switch event.Type {
		case "speechStart":
			if tracker.Awake(time.Now()) && faceClient != nil {
				faceClient.Emotion("focused")
			}
			fmt.Println("[speech started]")
		case "transcript":
			kind := "partial"
			if event.Final {
				kind = "final"
			}
			fmt.Printf("[%s] %s\n", kind, event.Transcript)
		case "speechComplete":
			text := collector.Complete(event)
			if text == "" {
				fmt.Println("[turn complete with no text — run with FRITZ_VOICE_DEBUG=1 and report the event lines]")
				return
			}
			fmt.Printf("[turn complete] %q\n", text)
			if request, ok := tracker.Accept(text, time.Now()); ok {
				if faceClient != nil {
					faceClient.Blink()
					faceClient.Emotion("happy")
				}
				fmt.Printf(">> REQUEST: %s\n", request)
				// A real agent would reply now; the follow-up window
				// opens as if it had.
				tracker.MarkReplied(time.Now())
			} else if voice.WakeMatch(text) {
				fmt.Println(">> (wake phrase heard — next turn is the request)")
				if faceClient != nil {
					faceClient.Blink()
					faceClient.Emotion("focused")
				}
			}
		case "speechEnd":
			// Turn boundary; speechComplete carries the final text.
		default:
			// Ignore unknown event types, per the API contract.
		}
	})
	if err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "voicecheck:", err)
		os.Exit(1)
	}
	fmt.Println("voicecheck: stopped")
}
