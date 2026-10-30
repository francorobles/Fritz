package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/francorobles/fritz/api/internal/actions"
)

// Executor runs one confirmed pending action and returns the
// text Fritz says (or the API returns) as the outcome.
type Executor func(ctx context.Context, payload json.RawMessage) (string, error)

// Confirmer enforces the confirmation rule for spoken/chat
// turns: when a pending action exists, a clear yes (checked
// here in Go, never by the model) executes it; any other turn
// declines it and proceeds normally. Wire one into every input
// channel (voice, WhatsApp) over the shared actions store.
type Confirmer struct {
	Store *actions.Store
	Exec  map[string]Executor
}

// affirmatives are the whole-utterance yeses that confirm a
// pending action. Deliberately strict — a missed yes just means
// the user repeats it; a false yes changes their calendar.
var affirmatives = map[string]bool{
	"yes": true, "yeah": true, "yep": true, "yup": true,
	"ok": true, "okay": true, "sure": true, "confirm": true,
	"approved": true, "approve": true,
	"yes please": true, "yeah go ahead": true, "go ahead": true,
	"go for it": true, "do it": true, "send it": true,
	"create it": true, "book it": true,
}

// IsAffirmative reports whether a whole user turn is a clear
// yes. The model never makes this call.
func IsAffirmative(text string) bool {
	return affirmatives[normalizeYes(text)]
}

func normalizeYes(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	// A wake word may survive into text channels; drop it.
	text = strings.TrimPrefix(text, "fritz ")
	text = strings.TrimPrefix(text, "hey fritz ")
	var b strings.Builder
	for _, r := range text {
		if (r >= 'a' && r <= 'z') || r == ' ' {
			b.WriteRune(r)
		}
	}
	joined := strings.Join(strings.Fields(b.String()), " ")
	// Stripping punctuation above can expose the wake word
	// ("Fritz, yes" → "fritz yes"); drop it here too.
	joined = strings.TrimPrefix(joined, "fritz ")
	joined = strings.TrimPrefix(joined, "hey fritz ")
	joined = strings.TrimPrefix(joined, "okay fritz ")
	return joined
}

// HandleTurn checks a fresh user turn against the pending
// action. handled=true means the turn is fully answered (reply
// is what Fritz says) and the model must not run. handled=false
// means proceed with the turn — either nothing was pending, or
// the turn wasn't a yes and quietly declined the action (the
// skill's rule: a spoken non-yes cancels).
func (c *Confirmer) HandleTurn(ctx context.Context, text string) (handled bool, reply string) {
	pending, ok, err := c.Store.LatestPending()
	if err != nil || !ok {
		return false, ""
	}
	if !IsAffirmative(text) {
		_ = c.Store.Decline(pending.ID)
		return false, ""
	}
	action, err := c.Store.Approve(pending.ID)
	if errors.Is(err, actions.ErrNotPending) || errors.Is(err, actions.ErrNotFound) {
		return true, "That one's already been taken care of."
	}
	if err != nil {
		return true, "Sorry — I couldn't confirm that just now."
	}
	exec := c.Exec[action.Kind]
	if exec == nil {
		_ = c.Store.Finish(action.ID, "no executor for "+action.Kind, true)
		return true, "Sorry — I can't run that anymore."
	}
	result, err := exec(ctx, json.RawMessage(action.Payload))
	if err != nil {
		_ = c.Store.Finish(action.ID, err.Error(), true)
		return true, "Sorry, that didn't work: " + err.Error()
	}
	_ = c.Store.Finish(action.ID, result, false)
	return true, result
}
