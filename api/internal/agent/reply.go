package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/francorobles/fritz/api/internal/actions"
	"github.com/francorobles/fritz/api/internal/integrations"
)

// maxReplyRunes caps a reply body: Fritz writes short spoken-style
// replies, and a runaway body is a sign the model went off script.
const maxReplyRunes = 2000

type replyArgs struct {
	Account string `json:"account"`
	ID      string `json:"id"`
	Body    string `json:"body"`
}

// repliers picks the mailboxes that can send.
func repliers(boxes []integrations.Mailbox) map[string]integrations.MailReplier {
	out := map[string]integrations.MailReplier{}
	for _, b := range boxes {
		if r, ok := b.(integrations.MailReplier); ok && !isNil(r) {
			out[b.Name()] = r
		}
	}
	return out
}

// EmailReplyTool is email_reply, gated like calendar_create:
// calling it only files a pending action whose summary names the
// recipient and quotes the body. Nothing is sent until the user
// says yes (checked in Go by the Confirmer) or taps Approve.
func EmailReplyTool(boxes []integrations.Mailbox, store *actions.Store) []Tool {
	can := repliers(boxes)
	if len(can) == 0 || store == nil {
		return nil
	}
	var names []string
	for _, b := range boxes {
		if _, ok := can[b.Name()]; ok {
			names = append(names, b.Name())
		}
	}
	return []Tool{{
		Name: "email_reply",
		Description: "Propose a reply to an email (use account + id from email_search). This does NOT send: " +
			"it files a pending action and returns a summary you must read aloud, then ask the user to confirm " +
			"(they say yes). Write the body as the user, in their words: short, plain " +
			"text, no signature unless they asked for one.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"account": map[string]any{"type": "string", "enum": names},
				"id":      map[string]any{"type": "string", "description": "Message id from email_search."},
				"body":    map[string]any{"type": "string", "description": "The reply text, plain."},
			},
			"required": []string{"account", "id", "body"},
		},
		Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args replyArgs
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			args.Body = strings.TrimSpace(args.Body)
			switch {
			case args.Body == "":
				return nil, fmt.Errorf("body is empty")
			case utf8.RuneCountInString(args.Body) > maxReplyRunes:
				return nil, fmt.Errorf("body is too long (max %d characters)", maxReplyRunes)
			}
			box, ok := can[args.Account]
			if !ok {
				return nil, fmt.Errorf("account %q cannot send replies", args.Account)
			}
			// Resolve the recipient now, so the user hears exactly
			// who it goes to before agreeing.
			r, err := box.PrepareReply(ctx, args.ID)
			if err != nil {
				return nil, err
			}
			summary := fmt.Sprintf("reply to %s: \"%s\"?", r.Name, args.Body)
			payload, err := json.Marshal(args)
			if err != nil {
				return nil, err
			}
			action, err := store.Create("email_reply", summary, string(payload))
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"status":  "needs_confirmation",
				"summary": summary,
				"spoken":  fmt.Sprintf("Should I reply to %s: %s? Just say yes and I'll send it.", r.Name, strings.TrimRight(strings.TrimSpace(args.Body), ".!?")),
				"action":  action.ID,
				"note":    "Not sent. Read the summary aloud and ask the user to say yes (or approve on their phone). It expires in 15 minutes.",
			}, nil
		},
		// The full body is read back before the yes: the user must
		// hear exactly what will be sent.
		Confirm: spokenConfirm,
	}}
}

// EmailReplyExecutor sends a confirmed email_reply. Like
// CalendarCreateExecutor it is the only path that sends, reached
// after the pending action was claimed exactly once.
func EmailReplyExecutor(boxes []integrations.Mailbox) Executor {
	can := repliers(boxes)
	return func(ctx context.Context, payload json.RawMessage) (string, error) {
		var args replyArgs
		if err := json.Unmarshal(payload, &args); err != nil {
			return "", fmt.Errorf("bad pending payload: %w", err)
		}
		box, ok := can[args.Account]
		if !ok {
			return "", fmt.Errorf("account %q is not connected", args.Account)
		}
		r, err := box.PrepareReply(ctx, args.ID)
		if err != nil {
			return "", err
		}
		if err := box.SendReply(ctx, r, args.Body); err != nil {
			return "", err
		}
		return fmt.Sprintf("Sent. I replied to %s.", r.Name), nil
	}
}

// Executors maps every confirmation-gated action kind to the code
// that runs it once confirmed, for whatever is connected. Voice,
// WhatsApp, and the /actions routes share this one map, so a new
// gated tool only needs adding here.
func Executors(reg *integrations.Registry) map[string]Executor {
	exec := map[string]Executor{}
	if reg.Calendar != nil {
		exec["calendar_create"] = CalendarCreateExecutor(reg.Calendar)
	}
	if len(repliers(reg.Mailboxes)) > 0 {
		exec["email_reply"] = EmailReplyExecutor(reg.Mailboxes)
	}
	return exec
}
