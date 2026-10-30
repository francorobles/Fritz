package voice

import "time"

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
	wakeOnlyPending bool
	awakeUntil      time.Time
}

// FollowUpWindow is how long after a reply Fritz keeps listening
// without needing the wake phrase again.
const FollowUpWindow = 8 * time.Second

// Accept processes one completed transcript at time now.
func (t *Tracker) Accept(transcript string, now time.Time) (request string, ok bool) {
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
	t.wakeOnlyPending = false
	t.awakeUntil = now.Add(FollowUpWindow)
}

// Awake reports whether the tracker would accept a non-wake turn at
// time now (for the face: focused while awake).
func (t *Tracker) Awake(now time.Time) bool {
	return t.wakeOnlyPending || now.Before(t.awakeUntil)
}
