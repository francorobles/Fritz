package integrations

import (
	"context"
	"time"
)

// Summary is one message as lists show it (never the full body).
type Summary struct {
	Account string    `json:"account"` // gmail
	ID      string    `json:"id"`
	From    string    `json:"from"`
	Subject string    `json:"subject"`
	Date    time.Time `json:"date"`
	Snippet string    `json:"snippet"`
	Unread  bool      `json:"unread"`
}

// Message is one fully-read message. Body is plain text (HTML
// converted, quoted reply tails stripped) ready for the model.
type Message struct {
	Summary
	Body string `json:"body"`
}

// Mailbox is one readable inbox. Search treats query as
// free-text "contains" (each backend maps it to its own search
// syntax); an empty query lists the newest messages. Drafting
// and sending arrive later with the agent, behind the
// confirmation rule — this pass is read-only by design.
type Mailbox interface {
	Name() string
	Search(ctx context.Context, query string, max int) ([]Summary, error)
	Read(ctx context.Context, id string) (Message, error)
}
