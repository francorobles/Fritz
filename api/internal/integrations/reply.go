package integrations

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"net/mail"
	"strings"

	gmailapi "google.golang.org/api/gmail/v1"
)

// MailReplier is a mailbox that can answer a message in its thread.
// Only Gmail for now (the gmail.compose scope covers sending).
type MailReplier interface {
	Mailbox
	// PrepareReply resolves who a reply to id goes to and how it
	// threads — read-only, so the confirmation summary can name
	// the recipient before anything is sent.
	PrepareReply(ctx context.Context, id string) (Reply, error)
	// SendReply sends body as a reply. Call it ONLY from a
	// confirmed pending action (the confirmation rule).
	SendReply(ctx context.Context, r Reply, body string) error
}

// Reply is everything needed to answer one message in-thread.
type Reply struct {
	To         string // full address header value
	Name       string // display name to say aloud, falls back to the address
	Subject    string // already "Re: ..."
	ThreadID   string
	InReplyTo  string // the original's Message-ID
	References string
}

// PrepareReply implements MailReplier.
func (g *Gmail) PrepareReply(ctx context.Context, id string) (Reply, error) {
	msg, err := g.svc.Users.Messages.Get("me", id).Format("metadata").
		MetadataHeaders("From", "Reply-To", "Subject", "Message-ID", "References").
		Context(ctx).Do()
	if err != nil {
		return Reply{}, fmt.Errorf("gmail: read %s: %w", id, err)
	}
	headers := map[string]string{}
	if msg.Payload != nil {
		for _, h := range msg.Payload.Headers {
			headers[strings.ToLower(h.Name)] = h.Value
		}
	}
	to := headers["reply-to"]
	if to == "" {
		to = headers["from"]
	}
	addr, err := mail.ParseAddress(to)
	if err != nil {
		return Reply{}, fmt.Errorf("gmail: no usable sender on %s: %w", id, err)
	}
	name := addr.Name
	if name == "" {
		name = addr.Address
	}
	subject := strings.TrimSpace(headers["subject"])
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = strings.TrimSpace("Re: " + subject)
	}
	refs := strings.TrimSpace(headers["references"] + " " + headers["message-id"])
	return Reply{
		To: addr.String(), Name: name, Subject: subject, ThreadID: msg.ThreadId,
		InReplyTo: headers["message-id"], References: refs,
	}, nil
}

// SendReply implements MailReplier.
func (g *Gmail) SendReply(ctx context.Context, r Reply, body string) error {
	raw := buildReply(r, body)
	_, err := g.svc.Users.Messages.Send("me", &gmailapi.Message{
		Raw:      base64.URLEncoding.EncodeToString(raw),
		ThreadId: r.ThreadID,
	}).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("gmail: send reply: %w", err)
	}
	return nil
}

// buildReply renders an RFC 2822 plain-text reply. Header values
// come from someone else's email and the body from the model, so
// CR/LF are stripped from every header: a newline there would let
// either inject extra headers (another recipient, say).
func buildReply(r Reply, body string) []byte {
	clean := func(s string) string {
		return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "To: %s\r\n", clean(r.To))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", clean(r.Subject)))
	if r.InReplyTo != "" {
		fmt.Fprintf(&b, "In-Reply-To: %s\r\n", clean(r.InReplyTo))
	}
	if r.References != "" {
		fmt.Fprintf(&b, "References: %s\r\n", clean(r.References))
	}
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"))
	b.WriteString("\r\n")
	return b.Bytes()
}
