package integrations

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/mail"
)

// WorkMail reads an AWS WorkMail mailbox over IMAP/TLS —
// WorkMail offers no Gmail-style REST API, and IMAP is its
// supported programmatic path. The password comes from the
// environment (never config); one connection per operation
// keeps failure handling simple at mailbox scale.
type WorkMail struct {
	cfg      WorkMailConfig
	password string
}

// NewWorkMail builds the client. The password is resolved by
// the caller from cfg.PasswordEnv and kept in memory only.
func NewWorkMail(cfg WorkMailConfig, password string) *WorkMail {
	return &WorkMail{cfg: cfg, password: password}
}

// Name implements Mailbox.
func (m *WorkMail) Name() string { return "workmail" }

func (m *WorkMail) connect(ctx context.Context) (*imapclient.Client, error) {
	addr := net.JoinHostPort(m.cfg.HostOrDefault(), fmt.Sprint(m.cfg.PortOrDefault()))
	opts := &imapclient.Options{
		TLSConfig: &tls.Config{ServerName: m.cfg.HostOrDefault()},
	}
	// imapclient dials without ctx in this version; dials are
	// quick, and ctx cancellation still bounds the HTTP-style
	// callers around us.
	_ = ctx
	c, err := imapclient.DialTLS(addr, opts)
	if err != nil {
		return nil, fmt.Errorf("workmail: dial %s: %w", addr, err)
	}
	if err := c.Login(m.cfg.Address, m.password).Wait(); err != nil {
		c.Close()
		return nil, fmt.Errorf("workmail: login as %s: %w", m.cfg.Address, err)
	}
	return c, nil
}

// Search implements Mailbox: free-text SEARCH (or ALL for an
// empty query), newest first.
func (m *WorkMail) Search(ctx context.Context, query string, max int) ([]Summary, error) {
	if max <= 0 || max > 25 {
		max = 10
	}
	c, err := m.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if _, err := c.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, fmt.Errorf("workmail: select INBOX: %w", err)
	}
	criteria := &imap.SearchCriteria{}
	if strings.TrimSpace(query) != "" {
		criteria.Text = []string{query}
	}
	data, err := c.Search(criteria, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("workmail: search: %w", err)
	}
	seqNums, ok := data.All.(imap.SeqSet)
	if !ok {
		return nil, fmt.Errorf("workmail: unexpected search result set %T", data.All)
	}
	nums, _ := seqNums.Nums()
	if len(nums) == 0 {
		return nil, nil
	}
	// Newest = highest sequence numbers; take the tail, reversed.
	sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })
	if len(nums) > max {
		nums = nums[len(nums)-max:]
	}
	msgs, err := c.Fetch(imap.SeqSetNum(nums...), &imap.FetchOptions{
		Envelope: true, Flags: true, InternalDate: true, UID: true,
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("workmail: fetch: %w", err)
	}
	out := make([]Summary, 0, len(msgs))
	for _, msg := range msgs {
		out = append(out, summaryFromEnvelope(msg.Envelope, uint32(msg.UID), msg.Flags))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date.After(out[j].Date) })
	return out, nil
}

// Read implements Mailbox by IMAP UID.
func (m *WorkMail) Read(ctx context.Context, id string) (Message, error) {
	var uid uint32
	if _, err := fmt.Sscanf(id, "%d", &uid); err != nil || uid == 0 {
		return Message{}, fmt.Errorf("workmail: bad message id %q", id)
	}
	c, err := m.connect(ctx)
	if err != nil {
		return Message{}, err
	}
	defer c.Close()
	if _, err := c.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return Message{}, fmt.Errorf("workmail: select INBOX: %w", err)
	}
	msgs, err := c.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{
		Envelope: true, Flags: true, InternalDate: true, UID: true,
		BodySection: []*imap.FetchItemBodySection{{Peek: true}},
	}).Collect()
	if err != nil {
		return Message{}, fmt.Errorf("workmail: fetch: %w", err)
	}
	if len(msgs) == 0 || len(msgs[0].BodySection) == 0 {
		return Message{}, fmt.Errorf("workmail: message %s not found", id)
	}
	sum := summaryFromEnvelope(msgs[0].Envelope, uint32(msgs[0].UID), msgs[0].Flags)
	body, err := extractPlainText(msgs[0].BodySection[0].Bytes)
	if err != nil {
		return Message{}, err
	}
	return Message{Summary: sum, Body: stripQuotedReply(body)}, nil
}

func summaryFromEnvelope(env *imap.Envelope, uid uint32, flags []imap.Flag) Summary {
	sum := Summary{Account: "workmail", Unread: true}
	if uid > 0 {
		sum.ID = fmt.Sprint(uid)
	}
	for _, f := range flags {
		if f == imap.FlagSeen {
			sum.Unread = false
		}
	}
	if env == nil {
		return sum
	}
	sum.Subject = env.Subject
	sum.Date = env.Date
	if len(env.From) > 0 {
		addr := env.From[0]
		if addr.Name != "" {
			sum.From = fmt.Sprintf("%s <%s>", addr.Name, addr.Addr())
		} else {
			sum.From = addr.Addr()
		}
	}
	return sum
}

// extractPlainText walks a raw RFC 2822 message and returns its
// first text/plain part (HTML flattened as a fallback).
func extractPlainText(raw []byte) (string, error) {
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		// Not parseable as structured mail: return it raw rather
		// than losing the message entirely.
		return string(raw), nil
	}
	var plain, htmlBody string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		// PartHeader has no parsed ContentType in this version;
		// the raw header + media-type prefix is enough here.
		contentType := strings.ToLower(strings.TrimSpace(strings.Split(part.Header.Get("Content-Type"), ";")[0]))
		if contentType == "" {
			contentType = "text/plain" // RFC 2822 default
		}
		body, err := io.ReadAll(part.Body)
		if err != nil {
			continue
		}
		switch {
		case contentType == "text/plain" && plain == "":
			plain = string(body)
		case contentType == "text/html" && htmlBody == "":
			htmlBody = string(body)
		}
	}
	if plain != "" {
		return plain, nil
	}
	if htmlBody != "" {
		return htmlToText(htmlBody), nil
	}
	return "", nil
}
