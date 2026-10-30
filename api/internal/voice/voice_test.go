package voice

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWakeMatch(t *testing.T) {
	matches := []string{"Hi Fritz", "hi fritz", "Hi, Fritz!", "FRITZ",
		"hey frits, are you there", "Fritts", "please tell Fritz I said hi"}
	for _, s := range matches {
		if !WakeMatch(s) {
			t.Errorf("WakeMatch(%q) = false, want true", s)
		}
	}
	non := []string{"", "hello there", "fritzgerald is here", "hi fritzgerald"}
	for _, s := range non {
		if WakeMatch(s) {
			t.Errorf("WakeMatch(%q) = true, want false", s)
		}
	}
}

func TestStripWake(t *testing.T) {
	if got := StripWake("Hi Fritz, what time is it?"); got != "hi what time is it" {
		t.Errorf("StripWake = %q", got)
	}
	if got := StripWake("Fritz"); got != "" {
		t.Errorf("StripWake(only wake) = %q, want empty", got)
	}
}

func TestParseEvent(t *testing.T) {
	ack, err := ParseEvent([]byte(`{"sessionId":"s1"}`))
	if err != nil || !ack.IsSessionAck() {
		t.Errorf("ack = %+v, %v", ack, err)
	}
	ev, err := ParseEvent([]byte(`{"type":"transcript","turnId":"t1","transcript":"hi","final":true}`))
	if err != nil || ev.Type != "transcript" || !ev.Final || ev.Transcript != "hi" {
		t.Errorf("event = %+v, %v", ev, err)
	}
	unknown, err := ParseEvent([]byte(`{"type":"somethingNew","x":1}`))
	if err != nil || unknown.Type != "somethingNew" {
		t.Errorf("unknown events must parse without error: %+v, %v", unknown, err)
	}
	if _, err := ParseEvent([]byte(`not json`)); err == nil {
		t.Error("malformed JSON must error")
	}
}

func TestTurnCollector(t *testing.T) {
	c := &TurnCollector{}
	// Text only in cumulative transcript events; speechComplete empty.
	c.Observe(Event{Type: "transcript", TurnID: "t1", Transcript: "Hi"})
	c.Observe(Event{Type: "transcript", TurnID: "t1", Transcript: "Hi Fritz"})
	if got := c.Complete(Event{Type: "speechComplete", TurnID: "t1"}); got != "Hi Fritz" {
		t.Errorf("Complete from transcripts = %q, want latest cumulative", got)
	}
	// The event's own transcript wins when present.
	c.Observe(Event{Type: "transcript", TurnID: "t2", Transcript: "partial text"})
	if got := c.Complete(Event{Type: "speechComplete", TurnID: "t2", Transcript: "final text"}); got != "final text" {
		t.Errorf("Complete with own text = %q", got)
	}
	// Turns are forgotten after completion.
	if got := c.Complete(Event{Type: "speechComplete", TurnID: "t2"}); got != "" {
		t.Errorf("Complete twice = %q, want empty", got)
	}
}

func TestTurnCollectorLatest(t *testing.T) {
	c := &TurnCollector{}
	if got := c.Latest(); got != "" {
		t.Errorf("Latest with no events = %q, want empty", got)
	}
	c.Observe(Event{Type: "transcript", TurnID: "t1", Transcript: "Hi"})
	c.Observe(Event{Type: "transcript", TurnID: "t1", Transcript: "Hi Fritz"})
	// Latest peeks without forgetting: a later Complete still works.
	if got := c.Latest(); got != "Hi Fritz" {
		t.Errorf("Latest = %q, want newest cumulative text", got)
	}
	if got := c.Complete(Event{Type: "speechComplete", TurnID: "t1"}); got != "Hi Fritz" {
		t.Errorf("Complete after Latest = %q", got)
	}
	// TakeLatest returns the text and forgets it (local endpointing).
	c.Observe(Event{Type: "transcript", TurnID: "t2", Transcript: "stalled turn"})
	if got := c.TakeLatest(); got != "stalled turn" {
		t.Errorf("TakeLatest = %q", got)
	}
	if got := c.Latest(); got != "" {
		t.Errorf("Latest after TakeLatest = %q, want empty", got)
	}
	if got := c.TakeLatest(); got != "" {
		t.Errorf("TakeLatest twice = %q, want empty", got)
	}
}

// TestSplitUtteranceKeepsWake replays a real Pi log: one utterance
// ("Hi, Fritz. What is the value of peso to dollar right now?") whose
// partials arrive without any server completion. Whether the server
// splits it across turnIds or restates it within one turn, local
// endpointing must keep the wake phrase, or the tracker ignores the
// turn and Fritz stays silent.
func TestSplitUtteranceKeepsWake(t *testing.T) {
	partials := []string{"Hi,", "Hi, Fritz.", "What is", "What is the value",
		"What is the value of peso to dollar right now?"}
	feed := func(c *TurnCollector, split bool) {
		for i, p := range partials {
			turn := "t1"
			if split && i >= 2 {
				turn = "t2" // server started a new turn mid-utterance
			}
			c.Observe(Event{Type: "transcript", TurnID: turn, Transcript: p})
		}
	}
	for _, split := range []bool{false, true} {
		c := &TurnCollector{}
		feed(c, split)
		text := c.TakeLatest()
		if !WakeMatch(text) {
			t.Errorf("split=%v: endpointed %q lost the wake phrase", split, text)
			continue
		}
		tr := &Tracker{}
		now := time.Unix(1000, 0)
		req, ok := tr.Accept(text, now)
		if !ok || !strings.Contains(req, "peso") {
			t.Errorf("split=%v: request = %q, %v, want the peso question", split, req, ok)
		}
	}
}

// TestVoiceConcurrentUse hammers the collector and tracker from
// several goroutines: both are now shared between the event callback
// and the endpointing watcher, so they must be race-free (run with
// -race to check) and never deadlock.
func TestVoiceConcurrentUse(t *testing.T) {
	c := &TurnCollector{}
	tr := &Tracker{}
	now := time.Unix(1000, 0)
	done := make(chan struct{})
	for g := 0; g < 4; g++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 50; i++ {
				c.Observe(Event{Type: "transcript", TurnID: "t", Transcript: "hi"})
				_ = c.Latest()
				_ = c.TakeLatest()
				_ = c.Complete(Event{Type: "speechComplete", TurnID: "t"})
				_, _ = tr.Accept("Hi Fritz", now)
				tr.MarkReplied(now)
				_ = tr.Awake(now)
			}
		}()
	}
	for g := 0; g < 4; g++ {
		<-done
	}
}

func TestPiperSampleRate(t *testing.T) {
	dir := t.TempDir()
	voice := filepath.Join(dir, "voice.onnx")
	if err := os.WriteFile(voice+".json", []byte(`{"audio":{"sample_rate":44100}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := PiperSampleRate(voice); got != 44100 {
		t.Errorf("PiperSampleRate with json = %d, want 44100", got)
	}
	if got := PiperSampleRate(filepath.Join(dir, "missing.onnx")); got != 22050 {
		t.Errorf("PiperSampleRate without json = %d, want 22050 fallback", got)
	}
}

func TestSplitSentences(t *testing.T) {
	got := splitSentences("Hi there. It is 9 AM! Anything else?")
	want := []string{"Hi there.", "It is 9 AM!", "Anything else?"}
	if len(got) != len(want) {
		t.Fatalf("splitSentences = %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sentence %d = %q, want %q", i, got[i], want[i])
		}
	}
	if got := splitSentences("no punctuation"); len(got) != 1 || got[0] != "no punctuation" {
		t.Errorf("splitSentences no-punct = %q", got)
	}
}

// TestSpeakUsesOnePipeline checks that a multi-sentence reply starts
// piper exactly once with the sentences as stdin lines: restarting
// piper + aplay per sentence left seconds of dead air after every
// period on the Pi.
func TestSpeakUsesOnePipeline(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "piper-calls")
	stdinLog := filepath.Join(dir, "piper-stdin")
	piperSh := "#!/bin/sh\necho x >> " + calls + "\ncat > " + stdinLog + "\n"
	if err := os.WriteFile(filepath.Join(dir, "piper"), []byte(piperSh), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "aplay"), []byte("#!/bin/sh\ncat > /dev/null\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Speak(ctx, filepath.Join(dir, "voice.onnx"), "Hi there. It is 9 AM! Anything else?"); err != nil {
		t.Fatalf("Speak: %v", err)
	}
	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("fake piper never ran: %v", err)
	}
	if n := strings.Count(string(data), "x"); n != 1 {
		t.Errorf("piper invocations = %d, want 1", n)
	}
	raw, err := os.ReadFile(stdinLog)
	if err != nil {
		t.Fatalf("fake piper got no stdin: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 {
		t.Errorf("piper stdin lines = %q, want the 3 sentences", lines)
	}
	if err := Speak(ctx, "", "hi"); err == nil {
		t.Error("Speak with no voice path must error")
	}
}

func TestTracker(t *testing.T) {
	now := time.Unix(1000, 0)
	tr := &Tracker{}

	if _, ok := tr.Accept("what time is it", now); ok {
		t.Error("asleep tracker accepted a non-wake turn")
	}
	if req, ok := tr.Accept("Hi Fritz, what time is it", now); !ok || req != "hi what time is it" {
		t.Errorf("wake+request = %q, %v", req, ok)
	}
	// Wake-only turn: next turn becomes the request.
	if _, ok := tr.Accept("Fritz", now); ok {
		t.Error("wake-only turn should not produce a request yet")
	}
	if !tr.Awake(now) {
		t.Error("tracker should be awake after wake-only turn")
	}
	if req, ok := tr.Accept("what time is it", now); !ok || req != "what time is it" {
		t.Errorf("turn after wake-only = %q, %v", req, ok)
	}
	// Follow-up window: 8 s after a reply, no wake phrase needed;
	// after it expires, the wake phrase is required again.
	tr.MarkReplied(now)
	if _, ok := tr.Accept("and tomorrow", now.Add(7*time.Second)); !ok {
		t.Error("follow-up window should accept without wake phrase")
	}
	if _, ok := tr.Accept("and next week", now.Add(9*time.Second)); ok {
		t.Error("expired follow-up window should require the wake phrase")
	}
}
