// Command voicecheck is the realtime voice loop on the Pi: it streams
// the microphone to Muse Voice Transcribe, and when the wake-phrase
// tracker accepts a turn it asks Muse Spark for a reply and speaks it
// with Piper through the speaker (see the fritz-voice skill):
//
//	MODEL_API_KEY=... FRITZ_PIPER_VOICE=/opt/fritz/piper/<voice>.onnx \
//	  go run ./cmd/voicecheck
//
// Say "Hi Fritz, what time is it?", pause, and Fritz answers out loud.
// Follow-up questions within 8 s need no wake phrase. Ctrl-C stops.
// Without FRITZ_PIPER_VOICE the reply is only printed, not spoken.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/francorobles/fritz/api/internal/face"
	"github.com/francorobles/fritz/api/internal/memory"
	"github.com/francorobles/fritz/api/internal/meta"
	"github.com/francorobles/fritz/api/internal/vision"
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

	// Long-term memory (fritz.db by default; FRITZ_DB overrides).
	// Fritz remembers conversations and routines across restarts;
	// a store that can't open is a config error worth dying for —
	// silently forgetting everything is worse.
	dbPath := os.Getenv("FRITZ_DB")
	if dbPath == "" {
		dbPath = "fritz.db"
	}
	store, err := memory.Open(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "memory store:", err, "(set FRITZ_DB to a writable path)")
		os.Exit(1)
	}
	defer store.Close()
	if snap, snapErr := store.SnapshotText(30); snapErr == nil && snap != "" {
		fmt.Printf("memory: recalling memories and recent conversation (%s)\n", dbPath)
	} else {
		fmt.Printf("memory: fresh store (%s) — I will start learning\n", dbPath)
	}

	fmt.Printf("voicecheck: streaming mic (%s) — say \"Hi Fritz\", Ctrl-C to stop\n", mic)
	if os.Getenv("FRITZ_PIPER_VOICE") == "" {
		fmt.Println("note: FRITZ_PIPER_VOICE unset — replies will be printed, not spoken")
	}
	if os.Getenv("FRITZ_VOICE_DEBUG") == "1" {
		fmt.Println("debug: event logging ON — every API event will print as [event ...]")
	} else {
		fmt.Println("debug: off (set FRITZ_VOICE_DEBUG=1 to see raw API events)")
	}
	tracker := &voice.Tracker{}
	collector := &voice.TurnCollector{}
	debug := os.Getenv("FRITZ_VOICE_DEBUG") == "1"
	client := &voice.Client{APIKey: apiKey}
	chat := &meta.Client{APIKey: apiKey}
	piperVoice := os.Getenv("FRITZ_PIPER_VOICE")

	// Vision: the eyes follow your face (SAM 3.1 at ~1 Hz).
	// Optional — voice keeps working without a camera or face.
	// follow is also how respond() tells the model whether Fritz
	// can currently see the user.
	var follow *vision.Tracker
	var frames *vision.FrameSource
	if vision.CameraOff() {
		fmt.Println("vision: camera off (FRITZ_CAMERA=off) — eyes will not follow")
	} else if faceClient == nil {
		fmt.Println("vision: face unavailable — eyes will not follow")
	} else {
		frames = vision.NewFrameSource(os.Getenv("FRITZ_CAMERA_DEVICE"))
		frames.Logf = func(msg string) { fmt.Println(msg) }
		go frames.Run(ctx)
		follow = vision.NewTracker(chat, faceClient, frames, vision.ConfigFromEnv())
		follow.Logf = func(msg string) { fmt.Println(msg) }
		go follow.Run(ctx)
		go func() {
			for ev := range follow.Events {
				if ev.Present {
					fmt.Println("vision: eyes on you")
				} else {
					fmt.Println("vision: eyes released (standby wander)")
				}
			}
		}()
	}

	// respond asks Muse Spark for a reply and speaks it. The reply
	// streams: each finished sentence is spoken as it arrives, so
	// the first words start while the rest is still generating.
	// Replies are serialized, the mic is muted while Fritz speaks
	// (plus 700 ms for Bluetooth delay, so it doesn't hear itself
	// through the Echo), and the follow-up window opens when
	// speaking finishes.
	// Conversation context survives restarts: the last ~10 turns
	// come back from the store, older context via the memory
	// snapshot injected below.
	var history []meta.Message
	if turns, terr := store.RecentTurns(20); terr == nil {
		for _, t := range turns {
			history = append(history, meta.Message{Role: t.Role, Content: t.Content})
		}
		if len(turns) > 0 {
			fmt.Printf("memory: restored %d recent turns\n", len(turns))
		}
	}
	var replyMu sync.Mutex
	respond := func(request string) {
		replyMu.Lock()
		defer replyMu.Unlock()
		defer tracker.MarkReplied(time.Now())

		if faceClient != nil {
			faceClient.Emotion("thinking")
		}
		now := time.Now()
		system := fmt.Sprintf(
			"You are Fritz, a warm desk companion. Your replies are spoken aloud: "+
				"answer in 1-3 short sentences, no markdown, no lists, no URLs. "+
				"The current local time is %s.", now.Format("Mon 2 Jan 2006, 3:04 PM"))
		// What Fritz remembers about the user goes in every turn,
		// refreshed each time so newly learned facts apply at once.
		if snap, snapErr := store.SnapshotText(30); snapErr == nil && snap != "" {
			system += "\n\nWhat you remember about the user:\n" + snap
		}
		// Fritz does have eyes: say so, truthfully, from the
		// tracker's live presence. Without this the model denies
		// being able to see the user at all. Presence only — it
		// cannot make out details, clothes, or expressions.
		if follow != nil {
			if follow.Present() {
				system += "\nYou have a camera on your desk and your eyes are following the user's face right now: you can see that they are there. If they ask whether you can see them, say yes."
			} else {
				system += "\nYou have a camera on your desk, but you cannot see the user right now (they may have stepped away). If they ask whether you can see them, say not at the moment."
			}
			system += "\nIf the user's message has a photo attached, it is the live view from your camera right now — answer questions about it from what the photo shows."
		}
		messages := []meta.Message{
			{Role: "system", Content: system},
		}
		messages = append(messages, history...)
		userMsg := meta.Message{Role: "user", Content: request}
		// A visual question ("what shirt am I wearing?") gets the
		// live camera frame attached, so Spark answers from what
		// Fritz actually sees. Only while the tracker currently
		// sees the user — a stale frame from a stalled camera is
		// never passed off as the present view.
		if frames != nil && follow != nil && follow.Present() && looksVisual(request) {
			if frame, ok := frames.Latest(); ok {
				userMsg.Image = frame
				fmt.Println("[looking through the camera]")
			}
		}
		messages = append(messages, userMsg)

		fmt.Println("(thinking...)")
		chatStart := time.Now()
		// Start the speaker before the first token arrives so
		// Piper's model load overlaps the reply's first sentence.
		// Without FRITZ_PIPER_VOICE the reply streams to the
		// console only.
		speaker, speakerErr := voice.StartSpeaker(ctx, piperVoice)
		if speakerErr != nil {
			fmt.Println("speaker unavailable (reply will be printed, not spoken):", speakerErr)
			speaker = nil
		}
		var firstSpokenAt time.Time
		say := func(sentence string) {
			if speaker == nil {
				return
			}
			if firstSpokenAt.IsZero() {
				firstSpokenAt = time.Now()
				fmt.Printf("[first sentence after %s]\n", firstSpokenAt.Sub(chatStart).Round(time.Millisecond))
				if faceClient != nil {
					faceClient.Emotion("happy")
				}
			}
			if err := speaker.Say(sentence); err != nil {
				fmt.Println("speaker error (rest of reply printed, not spoken):", err)
				_ = speaker.Close()
				speaker = nil
			}
		}
		if speaker != nil {
			client.Muted.Store(true)
			if faceClient != nil {
				faceClient.Speaking(true)
			}
			defer func() {
				if speaker != nil {
					if err := speaker.Close(); err != nil {
						fmt.Println("speaker error (reply was printed above):", err)
					} else if !firstSpokenAt.IsZero() {
						fmt.Printf("[spoken in %s]\n", time.Since(firstSpokenAt).Round(time.Millisecond))
					}
				}
				// Bluetooth tail: keep muting briefly after
				// playback ends.
				select {
				case <-ctx.Done():
				case <-time.After(700 * time.Millisecond):
				}
				client.Muted.Store(false)
				if faceClient != nil {
					faceClient.Speaking(false)
				}
			}()
		}

		var splitter voice.Splitter
		var reply strings.Builder
		// The returned full text duplicates what the callback
		// accumulated; the stream error is what matters here.
		_, streamErr := chat.ChatStream(ctx, messages, func(delta string) {
			reply.WriteString(delta)
			for _, sentence := range splitter.Feed(delta) {
				say(sentence)
			}
		})
		if tail := splitter.Flush(); tail != "" {
			say(tail)
		}
		text := strings.TrimSpace(reply.String())
		if streamErr != nil && text == "" {
			fmt.Println("reply error:", streamErr)
			if faceClient != nil {
				faceClient.Emotion("skeptical")
			}
			return
		}
		if streamErr != nil {
			fmt.Println("reply error (speaking the partial reply):", streamErr)
		}
		fmt.Printf("Fritz: %s  [model took %s]\n", text, time.Since(chatStart).Round(time.Millisecond))
		history = append(history,
			meta.Message{Role: "user", Content: request},
			meta.Message{Role: "assistant", Content: text})
		if len(history) > 20 { // keep roughly the last 10 turns
			history = history[len(history)-20:]
		}
		// Persist the exchange so the next start remembers it and
		// the nightly consolidation can learn from it.
		if err := store.AddTurn("user", request); err != nil {
			fmt.Println("memory error:", err)
		}
		if err := store.AddTurn("assistant", text); err != nil {
			fmt.Println("memory error:", err)
		}
	}
	// handleTurn runs one completed turn through the wake tracker.
	// The same turn can complete twice in the event stream (a final
	// transcript, then speechComplete), so identical text within a
	// few seconds is only handled once. It runs on both the event
	// callback and the endpointing watcher below, so the dedupe
	// state is mutex-guarded.
	var turnMu sync.Mutex
	lastHandled := ""
	lastHandledAt := time.Time{}
	claimTurn := func(text string) bool {
		turnMu.Lock()
		defer turnMu.Unlock()
		if text == "" {
			return false
		}
		if text == lastHandled && time.Since(lastHandledAt) < 3*time.Second {
			return false
		}
		lastHandled, lastHandledAt = text, time.Now()
		return true
	}
	neverHandled := func() bool {
		turnMu.Lock()
		defer turnMu.Unlock()
		return lastHandled == ""
	}
	handleTurn := func(text string) {
		if !claimTurn(text) {
			return
		}
		fmt.Printf("[turn complete] %q\n", text)
		if request, ok := tracker.Accept(text, time.Now()); ok {
			if faceClient != nil {
				faceClient.Blink()
			}
			fmt.Printf(">> REQUEST: %s\n", request)
			respond(request) // speaks the reply; opens the follow-up window
		} else if voice.WakeMatch(text) {
			fmt.Println(">> (wake phrase heard — next turn is the request)")
			if faceClient != nil {
				faceClient.Blink()
				faceClient.Emotion("focused")
			}
		} else {
			// Nothing heard that Fritz should answer. Say so:
			// silent ignores are indistinguishable from a dead
			// pipeline in the log.
			fmt.Println(">> (ignored — no wake phrase)")
		}
	}

	// Some sessions never close a turn: no final transcript, no
	// speechComplete — partials just stop changing. Watch for that
	// stall and finish the turn locally, so a quiet server never
	// leaves a request unheard. A late server completion carrying
	// the same text is dropped by handleTurn's dedupe.
	go func() {
		const (
			poll     = 200 * time.Millisecond
			stallFor = 1500 * time.Millisecond
		)
		ticker := time.NewTicker(poll)
		defer ticker.Stop()
		var lastText string
		var stableSince time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if client.Muted.Load() {
					// Fritz is speaking; the mic hears
					// only silence, and any collected
					// text predates the reply.
					lastText, stableSince = "", time.Time{}
					continue
				}
				text := collector.Latest()
				if text == "" || text != lastText {
					lastText, stableSince = text, now
					continue
				}
				if now.Sub(stableSince) < stallFor {
					continue
				}
				lastText, stableSince = "", time.Time{}
				if taken := collector.TakeLatest(); taken != "" {
					fmt.Println("[turn endpointed locally after stall]")
					handleTurn(taken)
				}
			}
		}
	}()

	err = client.Run(ctx, audio, func(event voice.Event) {
		collector.Observe(event)
		if debug && event.Type != "audioProgress" {
			// audioProgress fires constantly while audio streams; it
			// only proves the server is receiving sound. Print the raw
			// frame: the API may use field names we don't parse yet.
			fmt.Printf("[event %s]\n", event.Raw)
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
			if event.Final {
				// Some sessions mark completion only here, with no
				// (or a much later) speechComplete.
				handleTurn(collector.Complete(event))
			}
		case "speechComplete":
			text := collector.Complete(event)
			if text == "" && neverHandled() {
				fmt.Println("[turn complete with no text — run with FRITZ_VOICE_DEBUG=1 and report the event lines]")
				return
			}
			handleTurn(text)
		case "speechEnd":
			// Turn boundary; completion comes via final/speechComplete.
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

// lookTriggers mark a request as being about what Fritz sees.
// Matching is substring on the lowercased request — deliberately
// generous: attaching an unneeded photo costs a little latency,
// while missing a visual question makes Fritz blind to it.
var lookTriggers = []string{
	"see me", "can you see", "do you see", "see what",
	"wearing", "what color", "what colour",
	"look at", "how do i look", "do i look",
	"holding", "read this", "read that", "in my hand",
}

// looksVisual reports whether a request asks about the visible
// scene (and so should carry the live camera frame).
func looksVisual(request string) bool {
	lower := strings.ToLower(request)
	for _, trigger := range lookTriggers {
		if strings.Contains(lower, trigger) {
			return true
		}
	}
	return false
}
