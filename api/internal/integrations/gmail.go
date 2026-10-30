package integrations

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/mail"
	"os"

	"golang.org/x/oauth2/google"
	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// GoogleScopes are the minimum the skill allows: read + compose
// Gmail, manage calendar events (calendar tools arrive with the
// agent; asking once now avoids a second consent flow).
var GoogleScopes = []string{
	gmailapi.GmailReadonlyScope,
	gmailapi.GmailComposeScope,
	"https://www.googleapis.com/auth/calendar.events",
}

// Gmail reads the user's Gmail via the official API and the OAuth
// token that cmd/googleauth saved.
type Gmail struct {
	svc *gmailapi.Service
}

// NewGmail builds the client from google-client.json +
// google-token.json in the config dir.
func NewGmail(ctx context.Context) (*Gmail, error) {
	clientJSON, err := readConfigFile(GoogleClientFile(), "google-client.json (create an OAuth Desktop client; see cmd/googleauth)")
	if err != nil {
		return nil, err
	}
	config, err := google.ConfigFromJSON(clientJSON, GoogleScopes...)
	if err != nil {
		return nil, fmt.Errorf("integrations: gmail client config: %w", err)
	}
	tok, err := loadTokenFile(GoogleTokenFile())
	if err != nil {
		return nil, fmt.Errorf("integrations: gmail token: %w (run: go run ./cmd/googleauth)", err)
	}
	src := newPersistingTokenSource(config.TokenSource(ctx, tok), GoogleTokenFile())
	svc, err := gmailapi.NewService(ctx, option.WithTokenSource(src))
	if err != nil {
		return nil, fmt.Errorf("integrations: gmail service: %w", err)
	}
	return &Gmail{svc: svc}, nil
}

func readConfigFile(path, hint string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("integrations: %w (missing %s)", err, hint)
	}
	return data, nil
}

// Name implements Mailbox.
func (g *Gmail) Name() string { return "gmail" }

// Search implements Mailbox: query is Gmail search syntax
// (free text works); empty query lists the newest mail.
func (g *Gmail) Search(ctx context.Context, query string, max int) ([]Summary, error) {
	if max <= 0 || max > 10 {
		max = 10 // skill: batch to at most 10 metadata fetches
	}
	list, err := g.svc.Users.Messages.List("me").Q(query).MaxResults(int64(max)).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("gmail: search: %w", err)
	}
	out := make([]Summary, 0, len(list.Messages))
	for _, ref := range list.Messages {
		msg, err := g.svc.Users.Messages.Get("me", ref.Id).Format("metadata").
			MetadataHeaders("From", "Subject", "Date").Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("gmail: get %s: %w", ref.Id, err)
		}
		out = append(out, gmailSummary(msg))
	}
	return out, nil
}

// Read implements Mailbox: full message, plain-text body with
// the quoted reply tail stripped.
func (g *Gmail) Read(ctx context.Context, id string) (Message, error) {
	msg, err := g.svc.Users.Messages.Get("me", id).Format("full").Context(ctx).Do()
	if err != nil {
		return Message{}, fmt.Errorf("gmail: read %s: %w", id, err)
	}
	sum := gmailSummary(msg)
	body := gmailBodyText(msg.Payload)
	if body == "" && msg.Snippet != "" {
		body = msg.Snippet
	}
	return Message{Summary: sum, Body: stripQuotedReply(body)}, nil
}

func gmailSummary(msg *gmailapi.Message) Summary {
	sum := Summary{Account: "gmail", ID: msg.Id, Snippet: msg.Snippet}
	for _, label := range msg.LabelIds {
		if label == "UNREAD" {
			sum.Unread = true
		}
	}
	if msg.Payload != nil {
		for _, h := range msg.Payload.Headers {
			switch h.Name {
			case "From":
				sum.From = h.Value
			case "Subject":
				sum.Subject = h.Value
			case "Date":
				if t, err := mail.ParseDate(h.Value); err == nil {
					sum.Date = t
				}
			}
		}
	}
	return sum
}

// gmailBodyText prefers text/plain parts, falling back to HTML
// flattened to text.
func gmailBodyText(part *gmailapi.MessagePart) string {
	if part == nil {
		return ""
	}
	if text := findPartText(part, "text/plain"); text != "" {
		return text
	}
	if htmlBody := findPartText(part, "text/html"); htmlBody != "" {
		return htmlToText(htmlBody)
	}
	return ""
}

func findPartText(part *gmailapi.MessagePart, mimeType string) string {
	if part.MimeType == mimeType && part.Body != nil && part.Body.Data != "" {
		raw, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(part.Body.Data)
		if err == nil {
			return string(raw)
		}
		// Gmail usually omits padding; a padded variant is the fallback.
		if raw, err := base64.URLEncoding.DecodeString(part.Body.Data); err == nil {
			return string(raw)
		}
		return ""
	}
	for _, child := range part.Parts {
		if text := findPartText(child, mimeType); text != "" {
			return text
		}
	}
	return ""
}
