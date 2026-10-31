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

// Local endpointing can finish a turn while the user is only
// pausing ("…check my email" [pause] "Gmail"). The helpers below
// recognize the rest of that same utterance when it arrives, so
// it extends the request instead of becoming a second one.

// turnWords splits text into words, keeping each original spelling
// next to a normalized form (lowercase, letters/digits/apostrophes
// only) for comparison. Punctuation-only fields are dropped.
func turnWords(text string) (orig, norm []string) {
	for _, f := range strings.Fields(text) {
		n := strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '\'':
				return r
			case r >= 'A' && r <= 'Z':
				return r + ('a' - 'A')
			}
			return -1
		}, f)
		if n != "" {
			orig = append(orig, f)
			norm = append(norm, n)
		}
	}
	return orig, norm
}

// Continuation reports whether next is prev with more words after
// it — the same utterance heard again, longer — and returns the
// added words. Case and punctuation are ignored.
func Continuation(prev, next string) (rest string, ok bool) {
	_, p := turnWords(prev)
	o, n := turnWords(next)
	if len(p) == 0 || len(n) <= len(p) {
		return "", false
	}
	for i := range p {
		if p[i] != n[i] {
			return "", false
		}
	}
	return strings.Join(o[len(p):], " "), true
}

// AddsWords reports whether a transcript says something beyond
// prev. A late restatement of prev or a piece of it ("Fritz can
// you") adds nothing; a longer version or unrelated words do.
func AddsWords(prev, next string) bool {
	_, p := turnWords(prev)
	_, n := turnWords(next)
	if len(n) == 0 {
		return false
	}
	return !strings.Contains(" "+strings.Join(p, " ")+" ", " "+strings.Join(n, " ")+" ")
}

// JoinTurns rebuilds an utterance that was cut at a pause: earlier
// is what was heard before the cut, later what completed after it.
// The server often resends the whole utterance (later already
// contains earlier); otherwise later is just the new words.
func JoinTurns(earlier, later string) string {
	if !AddsWords(later, earlier) {
		return later
	}
	return strings.TrimSpace(earlier + " " + later)
}

// WordCount counts words as Continuation does.
func WordCount(text string) int {
	_, n := turnWords(text)
	return len(n)
}
