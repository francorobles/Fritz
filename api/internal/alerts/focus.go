package alerts

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Focus mode ("let me focus for 30 minutes"): important mail that
// can wait is held instead of spoken or sent, and comes back as one
// digest when focus ends (or at the arrival greeting, if the user
// left). Interruption rules ("hold project updates unless there's a
// deadline") hold matching mail even outside focus. Only the voice
// process reads and writes these, but they live in the shared
// database so they survive a restart mid-focus.
const (
	KeyFocusUntil = "focus_until"   // RFC 3339 end time; "" = off
	KeyHeld       = "held_messages" // JSON []Held
	KeyProgress   = "progress"      // JSON Progress
)

// RulePrefix marks a memory as an interruption rule. Rules are
// ordinary preference memories, so they show on the dashboard (and
// can be deleted there) and reach every prompt that sees memories.
const RulePrefix = "Interruption rule: "

// Held is one message kept back for the next digest.
type Held struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"` // `Email from Dana: "Q4 numbers" (project update)`
}

// Focus is the focus-mode switch plus the held-message queue.
type Focus struct {
	State FlagStore
	Now   func() time.Time

	mu sync.Mutex // guards the held queue's read-modify-write
}

func (f *Focus) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

// Start turns focus on for d (replacing any running focus).
func (f *Focus) Start(d time.Duration) (time.Time, error) {
	until := f.now().Add(d)
	return until, f.State.SetState(KeyFocusUntil, until.UTC().Format(time.RFC3339))
}

// Stop turns focus off.
func (f *Focus) Stop() error { return f.State.SetState(KeyFocusUntil, "") }

// Active reports whether focus is on, and until when.
func (f *Focus) Active() (time.Time, bool) {
	until, set := f.until()
	return until, set && f.now().Before(until)
}

// Expired reports a focus that ran out but hasn't been wrapped up
// (Stop clears it): the moment to deliver the digest.
func (f *Focus) Expired() bool {
	until, set := f.until()
	return set && !f.now().Before(until)
}

func (f *Focus) until() (time.Time, bool) {
	v, _, ok, err := f.State.State(KeyFocusUntil)
	if err != nil || !ok || v == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, v)
	return t, err == nil
}

// Hold queues a message for the next digest.
func (f *Focus) Hold(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	held := f.held()
	held = append(held, Held{At: f.now(), Text: text})
	if len(held) > 20 { // a digest, not an inbox
		held = held[len(held)-20:]
	}
	data, _ := json.Marshal(held)
	return f.State.SetState(KeyHeld, string(data))
}

// Held returns the queue without clearing it.
func (f *Focus) Held() []Held {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.held()
}

// TakeHeld returns the queue and clears it: once in a digest, a
// message isn't repeated.
func (f *Focus) TakeHeld() []Held {
	f.mu.Lock()
	defer f.mu.Unlock()
	held := f.held()
	if len(held) > 0 {
		_ = f.State.SetState(KeyHeld, "")
	}
	return held
}

// Forget removes the given messages once they were told (a digest
// that was actually spoken). Anything held since stays queued.
func (f *Focus) Forget(told []Held) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Keyed by instant + text: decoded times can differ in
	// location while naming the same moment.
	type key struct {
		at   int64
		text string
	}
	done := make(map[key]bool, len(told))
	for _, h := range told {
		done[key{h.At.UnixNano(), h.Text}] = true
	}
	var keep []Held
	for _, h := range f.held() {
		if !done[key{h.At.UnixNano(), h.Text}] {
			keep = append(keep, h)
		}
	}
	if len(keep) == len(f.held()) {
		return
	}
	value := ""
	if len(keep) > 0 {
		data, _ := json.Marshal(keep)
		value = string(data)
	}
	_ = f.State.SetState(KeyHeld, value)
}

func (f *Focus) held() []Held {
	v, _, ok, err := f.State.State(KeyHeld)
	if err != nil || !ok || v == "" {
		return nil
	}
	var held []Held
	_ = json.Unmarshal([]byte(v), &held)
	return held
}

// Digest is the held queue as one line for a prompt.
func Digest(held []Held) string {
	parts := make([]string, len(held))
	for i, h := range held {
		parts[i] = fmt.Sprintf("%s (%s)", h.Text, h.At.Local().Format("3:04 PM"))
	}
	return strings.Join(parts, "; ")
}

// HeldMail is how a held email reads in the digest.
func HeldMail(m ImportantMail) string {
	text := fmt.Sprintf("Email from %s: %q", m.Sender(), m.Mail.Subject)
	if m.Why != "" {
		text += " (" + m.Why + ")"
	}
	return text
}

// Progress is where the user stopped ("I'm stopping at the pricing
// section"), for "where was I?" and the arrival greeting.
type Progress struct {
	Task     string    `json:"task"`
	Next     string    `json:"next,omitempty"`
	At       time.Time `json:"at"`
	Recalled bool      `json:"recalled,omitempty"` // already mentioned on arrival
}

// SaveProgress records where the user stopped.
func SaveProgress(st FlagStore, p Progress) error {
	data, _ := json.Marshal(p)
	return st.SetState(KeyProgress, string(data))
}

// LoadProgress returns the saved progress, if any.
func LoadProgress(st FlagStore) (Progress, bool) {
	v, _, ok, err := st.State(KeyProgress)
	if err != nil || !ok || v == "" {
		return Progress{}, false
	}
	var p Progress
	if json.Unmarshal([]byte(v), &p) != nil || p.Task == "" {
		return Progress{}, false
	}
	return p, true
}

// Rules picks the interruption rules out of the memory texts.
func Rules(memories []string) []string {
	var rules []string
	for _, m := range memories {
		if rest, ok := strings.CutPrefix(m, RulePrefix); ok {
			rules = append(rules, strings.TrimSpace(rest))
		}
	}
	return rules
}

// ShouldHold decides whether an important email waits for the next
// digest, and why: the user's rule says so, or they're focusing and
// it can wait. Everything else is delivered now.
func (f *Focus) ShouldHold(m ImportantMail) (bool, string) {
	if m.Hold {
		return true, "your interruption rule"
	}
	if _, focused := f.Active(); focused && !m.Urgent {
		return true, "focus mode"
	}
	return false, ""
}
