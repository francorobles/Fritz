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
// Two server quirks shape this collector. First, one utterance can
// arrive as several turns with no completion between them; keeping
// only one turn's text has lost the wake phrase that way, so
// Latest/TakeLatest return every open turn's text in first-seen
// order. Second, partials are not always monotonic: a later partial
// can restate rather than extend the earlier text, so each turn
// accumulates instead of replacing — only the new suffix is kept
// when the text grows, the whole chunk when it restates.
//
// It also supports local endpointing: if the server never closes a
// turn, Latest/TakeLatest let the caller finish the turn itself once
// the transcript stops changing. Safe for concurrent use.
type TurnCollector struct {
	mu    sync.Mutex
	order []string          // turnIds in first-seen order
	last  map[string]string // turnId -> last raw partial (growth detection)
	acc   map[string]string // turnId -> accumulated text
}

// Observe records transcript and final events.
func (c *TurnCollector) Observe(event Event) {
	if event.Type != "transcript" || event.Transcript == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil {
		c.last = make(map[string]string)
		c.acc = make(map[string]string)
	}
	if _, seen := c.last[event.TurnID]; !seen {
		c.order = append(c.order, event.TurnID)
	}
	text := event.Transcript
	prev := c.last[event.TurnID]
	c.last[event.TurnID] = text
	switch acc := c.acc[event.TurnID]; {
	case acc == "":
		c.acc[event.TurnID] = text
	case strings.Contains(acc, text):
		// Repeat of something already kept.
	case strings.HasPrefix(text, prev):
		// Cumulative growth: keep only the new suffix.
		c.acc[event.TurnID] = acc + text[len(prev):]
	default:
		// Restatement, not growth: preserve both.
		c.acc[event.TurnID] = acc + " " + text
	}
}

// Latest returns the newest transcript text seen so far without
// forgetting it ("" when nothing has been heard).
func (c *TurnCollector) Latest() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimSpace(strings.Join(c.ordered(), " "))
}

// TakeLatest returns and forgets all collected text. Used by local
// endpointing to finish a turn the server left open.
func (c *TurnCollector) TakeLatest() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	text := strings.TrimSpace(strings.Join(c.ordered(), " "))
	c.order = nil
	c.last = nil
	c.acc = nil
	return text
}

// ordered returns the accumulated text of every open turn in
// first-seen order. The caller must hold c.mu.
func (c *TurnCollector) ordered() []string {
	out := make([]string, 0, len(c.order))
	for _, id := range c.order {
		if text := strings.TrimSpace(c.acc[id]); text != "" {
			out = append(out, text)
		}
	}
	return out
}

// Complete returns the text for a finished turn: the speechComplete
// event's own transcript when it has one, otherwise the accumulated
// transcript observed for that turn. The turn is forgotten either way.
func (c *TurnCollector) Complete(event Event) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	text := strings.TrimSpace(event.Transcript)
	if text == "" {
		text = strings.TrimSpace(c.acc[event.TurnID])
	}
	delete(c.last, event.TurnID)
	delete(c.acc, event.TurnID)
	for i, id := range c.order {
		if id == event.TurnID {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	return text
}
