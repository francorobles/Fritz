package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/francorobles/fritz/api/internal/integrations"
)

// maxBodyRunes caps an email body sent to the model (fritz-agent
// skill: ~4k chars). Spoken replies are short; the rest is cost.
const maxBodyRunes = 4000

// mailSummary is what the model sees per message: enough to say
// "two from Sarah about Thursday", plus the id to read one.
type mailSummary struct {
	Account string `json:"account"`
	ID      string `json:"id"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	Date    string `json:"date"`
	Unread  bool   `json:"unread"`
	Snippet string `json:"snippet"`
}

func summarize(s integrations.Summary) mailSummary {
	return mailSummary{
		Account: s.Account, ID: s.ID, From: s.From, Subject: s.Subject,
		Date: s.Date.Local().Format("Mon 2 Jan 3:04 PM"), Unread: s.Unread, Snippet: s.Snippet,
	}
}

// MailTools are the read-only email tools over every connected
// mailbox. None when no mailbox is connected, so Spark is never
// offered a tool that can't work.
func MailTools(boxes []integrations.Mailbox) []Tool {
	if len(boxes) == 0 {
		return nil
	}
	names := make([]string, len(boxes))
	for i, b := range boxes {
		names[i] = b.Name()
	}
	return []Tool{
		{
			Name: "email_search",
			Description: "Search the user's email (" + strings.Join(names, ", ") + "). " +
				"An empty query lists the newest messages. Returns sender, subject, date, " +
				"unread flag, a short snippet, and the account + id for email_read. " +
				"Gmail search syntax works for Gmail, e.g. is:unread, from:sarah, newer_than:1d.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "What to look for: a name, a topic, or Gmail syntax. Empty for the newest mail."},
					"max":   map[string]any{"type": "integer", "description": "How many messages, 1-10 (default 5)."},
				},
			},
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var args struct {
					Query string `json:"query"`
					Max   int    `json:"max"`
				}
				if err := decodeArgs(raw, &args); err != nil {
					return nil, err
				}
				return searchMail(ctx, boxes, args.Query, args.Max)
			},
		},
		{
			Name:        "email_read",
			Description: "Read one email in full (plain text, quoted replies removed). Use the account and id from email_search.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"account": map[string]any{"type": "string", "enum": names},
					"id":      map[string]any{"type": "string"},
				},
				"required": []string{"account", "id"},
			},
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var args struct {
					Account string `json:"account"`
					ID      string `json:"id"`
				}
				if err := decodeArgs(raw, &args); err != nil {
					return nil, err
				}
				return readMail(ctx, boxes, args.Account, args.ID)
			},
		},
	}
}

// searchMail asks every mailbox and merges the newest results. A
// broken account is reported alongside the others' results rather
// than hiding the mail that did arrive.
func searchMail(ctx context.Context, boxes []integrations.Mailbox, query string, max int) (any, error) {
	if max <= 0 || max > 10 {
		max = 5
	}
	var all []integrations.Summary
	var problems []string
	for _, box := range boxes {
		sums, err := box.Search(ctx, query, max)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", box.Name(), err))
			continue
		}
		all = append(all, sums...)
	}
	if len(problems) == len(boxes) {
		return nil, fmt.Errorf("could not search email: %s", strings.Join(problems, "; "))
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Date.After(all[j].Date) })
	if len(all) > max {
		all = all[:max]
	}
	out := struct {
		Messages []mailSummary `json:"messages"`
		Errors   []string      `json:"errors,omitempty"`
		Now      string        `json:"now"`
	}{Messages: []mailSummary{}, Errors: problems, Now: time.Now().Format("Mon 2 Jan 3:04 PM")}
	for _, s := range all {
		out.Messages = append(out.Messages, summarize(s))
	}
	return out, nil
}

func readMail(ctx context.Context, boxes []integrations.Mailbox, account, id string) (any, error) {
	for _, box := range boxes {
		if box.Name() != account {
			continue
		}
		msg, err := box.Read(ctx, id)
		if err != nil {
			return nil, err
		}
		body, truncated := msg.Body, false
		if r := []rune(body); len(r) > maxBodyRunes {
			body, truncated = string(r[:maxBodyRunes]), true
		}
		return struct {
			mailSummary
			Body      string `json:"body"`
			Truncated bool   `json:"truncated,omitempty"`
		}{summarize(msg.Summary), body, truncated}, nil
	}
	return nil, fmt.Errorf("no connected account %q", account)
}
