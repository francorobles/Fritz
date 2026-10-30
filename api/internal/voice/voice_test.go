package voice

import (
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
