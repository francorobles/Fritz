package face

// State is what the assistant is doing, as the rest of the brain sees
// it. The mapping to face expressions lives only here (DirectivesFor)
// so the C++ face, the agent, and the voice loop cannot drift apart.
type State int

const (
	StateIdleAbsent  State = iota // nobody present
	StatePresentIdle              // person present, nothing happening
	StateListening                // wake phrase heard / taking a turn
	StateThinking                 // waiting on the model
	StateSpeaking                 // TTS playing (keeps previous expression)
	StateUrgent                   // urgent email / important event
	StateDone                     // task done, good news
	StateError                    // something failed
	StateDeepWork                 // holding notifications while user focuses
	StateLateNight                // suggesting the user stop for the night
	StateStartled                 // something unexpected (use sparingly)
)

// Directives is the set of face commands one state change sends.
// Empty fields send nothing: StateSpeaking, for example, only toggles
// the speaking pulse and keeps whatever expression is already shown.
type Directives struct {
	Emotion  string // pin this expression (ignored when Standby is set)
	Standby  bool   // return to the idle cycle instead of pinning
	Blink    bool   // blink first (wake-phrase acknowledgement)
	Speaking *bool  // non-nil: set the speaking pulse to this
}

func boolPtr(b bool) *bool { return &b }

// DirectivesFor maps an assistant state to face commands:
//
//	idle, nobody present        -> standby
//	person present, idle        -> normal (+ gaze following, sent separately)
//	heard wake phrase/listening -> blink, then focused
//	waiting on the model        -> thinking
//	speaking a reply            -> previous expression + speaking on
//	urgent email/important      -> surprised
//	task done, good news        -> happy
//	error, couldn't do it       -> skeptical
//	deep work (holding alerts)  -> focused
//	late night, suggest stop    -> tired
//	unexpected/startled         -> scared (sparingly)
//
// "angry" is deliberately unused: an angry assistant demos badly.
func DirectivesFor(state State) Directives {
	switch state {
	case StateIdleAbsent:
		return Directives{Standby: true}
	case StatePresentIdle:
		return Directives{Emotion: "normal"}
	case StateListening:
		return Directives{Emotion: "focused", Blink: true}
	case StateThinking:
		return Directives{Emotion: "thinking"}
	case StateSpeaking:
		return Directives{Speaking: boolPtr(true)}
	case StateUrgent:
		return Directives{Emotion: "surprised"}
	case StateDone:
		return Directives{Emotion: "happy"}
	case StateError:
		return Directives{Emotion: "skeptical"}
	case StateDeepWork:
		return Directives{Emotion: "focused"}
	case StateLateNight:
		return Directives{Emotion: "tired"}
	case StateStartled:
		return Directives{Emotion: "scared"}
	default:
		return Directives{Standby: true}
	}
}

// Apply sends the directives for a state change, in order.
func (c *Client) Apply(state State) {
	d := DirectivesFor(state)
	if d.Blink {
		c.Blink()
	}
	if d.Standby {
		c.Standby()
	} else if d.Emotion != "" {
		c.Emotion(d.Emotion)
	}
	if d.Speaking != nil {
		c.Speaking(*d.Speaking)
	}
}
