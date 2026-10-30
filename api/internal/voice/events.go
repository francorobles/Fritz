package voice

import "encoding/json"

// Event is one message from the Muse Voice Transcribe realtime API.
// The session acknowledgement is the only message with a SessionID
// and no Type. Unknown event types must be ignored by callers (the
// API adds new ones), so ParseEvent never fails on them — it only
// fails on malformed JSON.
type Event struct {
	Type       string `json:"type"` // speechStart | transcript | speechEnd | speechComplete | error | ...
	SessionID  string `json:"sessionId"`
	TurnID     string `json:"turnId"`
	Transcript string `json:"transcript"`
	Final      bool   `json:"final"`
	Message    string `json:"message"` // on error events, when present
}

// IsSessionAck reports whether this event is the handshake
// acknowledgement that must arrive before audio is sent.
func (e Event) IsSessionAck() bool { return e.Type == "" && e.SessionID != "" }

// ParseEvent decodes one WebSocket text frame.
func ParseEvent(data []byte) (Event, error) {
	var event Event
	if err := json.Unmarshal(data, &event); err != nil {
		return Event{}, err
	}
	return event, nil
}
