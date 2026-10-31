package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/francorobles/fritz/api/internal/actions"
	"github.com/francorobles/fritz/api/internal/activity"
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

	// Activity (optional) records each confirmed, declined, or
	// failed action; Source names the channel (voice, whatsapp).
	Activity *activity.Log
	Source   string
}

func (c *Confirmer) logAction(a actions.Action, detail string, ok bool) {
	_ = c.Activity.Add(activity.Entry{Source: c.Source, Kind: activity.KindAction, Name: a.Kind, Detail: detail, OK: ok})
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

// yesWords are single tokens that each mean yes on their own.
// A turn made ONLY of these (plus the wake word) also counts:
// people confirming out loud say "yes, hey Fritz, yes", and a
// missed yes here doesn't just repeat — it declined the action
// and the model filed a fresh one, looping forever.
var yesWords = map[string]bool{
	"yes": true, "yeah": true, "yep": true, "yup": true,
	"ok": true, "okay": true, "sure": true,
	"confirm": true, "confirmed": true, "approved": true,
}

// IsAffirmative reports whether a whole user turn is a clear
// yes. The model never makes this call.
func IsAffirmative(text string) bool {
	s := normalizeYes(text)
	if affirmatives[s] {
		return true
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return false
	}
	for _, f := range fields {
		if !yesWords[f] {
			return false
		}
	}
	return true
}

func normalizeYes(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	var b strings.Builder
	for _, r := range text {
		if (r >= 'a' && r <= 'z') || r == ' ' {
			b.WriteRune(r)
		}
	}
	// Drop the wake word wherever it landed — leading ("Hey
	// Fritz, yes"), trailing ("yes, Fritz"), or sandwiched by
	// the transcript ("yes hey yes", after the wake phrase was
	// partly stripped upstream). "hey" alone is never content.
	kept := make([]string, 0, 4)
	for _, tok := range strings.Fields(b.String()) {
		if tok == "fritz" || tok == "hey" {
			continue
		}
		kept = append(kept, tok)
	}
	return strings.Join(kept, " ")
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
		if c.Store.Decline(pending.ID) == nil {
			c.logAction(pending, "declined: "+pending.Summary, true)
		}
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
		c.logAction(action, "no executor: "+action.Summary, false)
		return true, "Sorry — I can't run that anymore."
	}
	result, err := exec(ctx, json.RawMessage(action.Payload))
	if err != nil {
		_ = c.Store.Finish(action.ID, err.Error(), true)
		c.logAction(action, "failed: "+action.Summary+" — "+err.Error(), false)
		return true, "Sorry, that didn't work: " + err.Error()
	}
	_ = c.Store.Finish(action.ID, result, false)
	c.logAction(action, "confirmed: "+action.Summary+" — "+result, true)
	return true, result
}
