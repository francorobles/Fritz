package face

import "testing"

func TestDirectivesFor(t *testing.T) {
	tests := []struct {
		state State
		want  Directives
	}{
		{StateIdleAbsent, Directives{Standby: true}},
		{StatePresentIdle, Directives{Emotion: "normal"}},
		{StateListening, Directives{Emotion: "focused", Blink: true}},
		{StateThinking, Directives{Emotion: "thinking"}},
		{StateUrgent, Directives{Emotion: "surprised"}},
		{StateDone, Directives{Emotion: "happy"}},
		{StateError, Directives{Emotion: "skeptical"}},
		{StateDeepWork, Directives{Emotion: "focused"}},
		{StateLateNight, Directives{Emotion: "tired"}},
		{StateStartled, Directives{Emotion: "scared"}},
	}
	for _, tt := range tests {
		got := DirectivesFor(tt.state)
		if got.Emotion != tt.want.Emotion || got.Standby != tt.want.Standby ||
			got.Blink != tt.want.Blink || got.Speaking != tt.want.Speaking {
			t.Errorf("DirectivesFor(%d) = %+v, want %+v", tt.state, got, tt.want)
		}
		if got.Emotion == "angry" {
			t.Errorf("DirectivesFor(%d) uses angry, which is deliberately unused", tt.state)
		}
	}

	speaking := DirectivesFor(StateSpeaking)
	if speaking.Emotion != "" || speaking.Standby || speaking.Speaking == nil || !*speaking.Speaking {
		t.Errorf("DirectivesFor(StateSpeaking) = %+v, want only Speaking=true", speaking)
	}
}

func TestApplySendsDirectivesInOrder(t *testing.T) {
	face, addr := newFakeFace(t)
	client := newClient(t, addr)

	client.Apply(StateListening)
	if got := face.next(t); got != "blink" {
		t.Errorf("first datagram: got %q, want blink", got)
	}
	if got := face.next(t); got != "emotion focused" {
		t.Errorf("second datagram: got %q, want emotion focused", got)
	}

	client.Apply(StateSpeaking)
	if got := face.next(t); got != "speaking on" {
		t.Errorf("speaking datagram: got %q, want speaking on", got)
	}

	client.Apply(StateIdleAbsent)
	if got := face.next(t); got != "standby" {
		t.Errorf("idle datagram: got %q, want standby", got)
	}
}
