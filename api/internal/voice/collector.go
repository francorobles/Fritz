package voice

import (
	"strings"
	"sync"
)

// TurnCollector assembles completed-turn text from realtime events.
// The API does not guarantee that speechComplete carries the
// transcript — the text may only ever appear in transcript events
// (partials are cumulative, so the latest one per turn wins). Feed
// every event to Observe; when a speechComplete arrives, take the
// text from Complete instead of trusting the event's own field.
//
// It also supports local endpointing: if the server never closes a
// turn, Latest/TakeLatest let the caller finish the turn itself once
// the transcript stops changing. Safe for concurrent use.
type TurnCollector struct {
	mu     sync.Mutex
	latest map[string]string // turnId -> latest cumulative transcript
}

// Observe records transcript and final events.
func (c *TurnCollector) Observe(event Event) {
	if event.Type != "transcript" || event.Transcript == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.latest == nil {
		c.latest = make(map[string]string)
	}
	c.latest[event.TurnID] = event.Transcript
}

// Latest returns the newest transcript text seen so far without
// forgetting it ("" when nothing has been heard).
func (c *TurnCollector) Latest() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	best := ""
	for _, text := range c.latest {
		if len(text) > len(best) {
			best = text
		}
	}
	return strings.TrimSpace(best)
}

// TakeLatest returns and forgets all collected text. Used by local
// endpointing to finish a turn the server left open.
func (c *TurnCollector) TakeLatest() string {
	text := c.Latest()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.latest = nil
	return text
}

// Complete returns the text for a finished turn: the speechComplete
// event's own transcript when it has one, otherwise the latest
// transcript observed for that turn. The turn is forgotten either way.
func (c *TurnCollector) Complete(event Event) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	text := strings.TrimSpace(event.Transcript)
	if text == "" && c.latest != nil {
		text = strings.TrimSpace(c.latest[event.TurnID])
	}
	if c.latest != nil {
		delete(c.latest, event.TurnID)
	}
	return text
}
