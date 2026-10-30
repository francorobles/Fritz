package voice

import (
	"strings"
	"sync"
	"time"
)

// Tracker turns completed transcripts into agent requests using the
// wake phrase. It is a small state machine with three states:
//
//   - asleep: only transcripts containing the wake phrase count. If
//     the wake phrase is the whole turn ("Fritz"), the NEXT turn is
//     treated as the request (wake-only pending).
//   - wake-only pending: the next transcript is the request, whatever
//     it starts with.
//   - follow-up window: for FollowUpWindow after Fritz finishes
//     speaking (see MarkReplied), transcripts count without the wake
//     phrase.
//
// Accept is fed one completed transcript at a time (speechComplete in
// the realtime API) and returns the request text, or "" if the turn
// should be ignored.
type Tracker struct {
	mu              sync.Mutex
	wakeOnlyPending bool
	awakeUntil      time.Time
}

// FollowUpWindow is how long after a reply Fritz keeps listening
// without needing the wake phrase again.
const FollowUpWindow = 8 * time.Second

// Accept processes one completed transcript at time now.
func (t *Tracker) Accept(transcript string, now time.Time) (request string, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if WakeMatch(transcript) {
		t.wakeOnlyPending = false
		rest := StripWake(transcript)
		if rest == "" {
			// Turn was only "Fritz": the next turn is the request.
			t.wakeOnlyPending = true
			return "", false
		}
		return rest, true
	}
	if t.wakeOnlyPending {
		t.wakeOnlyPending = false
		return transcript, true
	}
	if now.Before(t.awakeUntil) {
		return transcript, true
	}
	return "", false
}

// MarkReplied opens the follow-up window when Fritz finishes speaking
// at time now.
func (t *Tracker) MarkReplied(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.wakeOnlyPending = false
	t.awakeUntil = now.Add(FollowUpWindow)
}

// Awake reports whether the tracker would accept a non-wake turn at
// time now (for the face: focused while awake).
func (t *Tracker) Awake(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.wakeOnlyPending || now.Before(t.awakeUntil)
}

// danglingWords never end a finished sentence: articles,
// prepositions, conjunctions, determiners, and auxiliaries. A
// transcript trailing one is a mid-thought pause ("…an email
// from me coming from", "…check if there's"), not the end of a
// turn — endpointing there answers a fragment, which feels like
// being interrupted.
var danglingWords = map[string]bool{
	"a": true, "an": true, "the": true,
	"from": true, "to": true, "of": true, "for": true, "with": true,
	"without": true, "about": true, "on": true, "in": true, "at": true,
	"by": true, "over": true, "under": true, "into": true, "than": true,
	"if": true, "and": true, "or": true, "but": true, "because": true,
	"so": true, "as": true, "when": true, "while": true,
	"my": true, "your": true, "his": true, "her": true, "their": true,
	"our": true, "this": true, "that": true, "these": true, "those": true,
	// Object pronouns: "…create an event for me" stopped for a
	// title/time is mid-thought, not a finished request.
	"me": true, "us": true, "him": true, "them": true, "you": true,
	// Bare request verbs with no object yet: "…can you create".
	"create": true, "make": true, "set": true, "schedule": true,
	"add": true, "book": true, "cancel": true, "delete": true,
	"remove": true, "remind": true, "tell": true, "show": true,
	"give": true, "find": true, "search": true, "check": true,
	"send": true, "call": true, "read": true, "write": true,
	"is": true, "are": true, "was": true, "were": true, "be": true,
	"been": true, "am": true, "do": true, "does": true, "did": true,
	"can": true, "could": true, "will": true, "would": true,
	"shall": true, "should": true, "may": true, "might": true,
	"must": true, "have": true, "has": true, "had": true,
	"there's": true, "theres": true, "it's": true, "its": true,
}

// LooksIncomplete reports whether a transcript reads like the
// speaker stopped mid-thought: it ends on a dangling word (above)
// rather than sentence-final punctuation or a content word.
func LooksIncomplete(text string) bool {
	text = strings.TrimSpace(strings.ToLower(text))
	if text == "" {
		return false
	}
	if last := text[len(text)-1]; last == '.' || last == '?' || last == '!' {
		return false
	}
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r == '\'')
	})
	if len(words) == 0 {
		return false
	}
	return danglingWords[words[len(words)-1]]
}

// StallWait is how long partials must sit unchanged before local
// endpointing may finish the turn. Ordinary endings wait base;
// mid-thought-looking text gets four times as long (at least
// 4 s), because no single global stall distinguishes a finished
// sentence from someone pausing to remember a name — the words
// do. The longer floor costs a genuinely finished dangling-word
// utterance a few extra seconds of silence; cutting a user off
// mid-sentence costs the whole turn, so it wins.
func StallWait(text string, base time.Duration) time.Duration {
	if !LooksIncomplete(text) {
		return base
	}
	wait := 4 * base
	if wait < 4*time.Second {
		wait = 4 * time.Second
	}
	return wait
}
