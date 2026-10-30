package voice

import "strings"

// TurnCollector assembles completed-turn text from realtime events.
// The API does not guarantee that speechComplete carries the
// transcript — the text may only ever appear in transcript events
// (partials are cumulative, so the latest one per turn wins). Feed
// every event to Observe; when a speechComplete arrives, take the
// text from Complete instead of trusting the event's own field.
type TurnCollector struct {
	latest map[string]string // turnId -> latest cumulative transcript
}

// Observe records transcript and final events.
func (c *TurnCollector) Observe(event Event) {
	if event.Type != "transcript" || event.Transcript == "" {
		return
	}
	if c.latest == nil {
		c.latest = make(map[string]string)
	}
	c.latest[event.TurnID] = event.Transcript
}

// Complete returns the text for a finished turn: the speechComplete
// event's own transcript when it has one, otherwise the latest
// transcript observed for that turn. The turn is forgotten either way.
func (c *TurnCollector) Complete(event Event) string {
	text := strings.TrimSpace(event.Transcript)
	if text == "" && c.latest != nil {
		text = strings.TrimSpace(c.latest[event.TurnID])
	}
	if c.latest != nil {
		delete(c.latest, event.TurnID)
	}
	return text
}
