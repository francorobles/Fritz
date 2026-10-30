package integrations

import (
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
)

func TestSummaryFromEnvelope(t *testing.T) {
	env := &imap.Envelope{
		Subject: "Deploy at 5",
		Date:    time.Date(2030, 10, 15, 9, 0, 0, 0, time.UTC),
		From:    []imap.Address{{Name: "Sarah", Mailbox: "sarah", Host: "corp.example"}},
	}
	seen := summaryFromEnvelope(env, 42, []imap.Flag{imap.FlagSeen})
	if seen.ID != "42" || seen.Unread || seen.From != "Sarah <sarah@corp.example>" || seen.Subject != "Deploy at 5" {
		t.Errorf("seen summary = %+v", seen)
	}
	unseen := summaryFromEnvelope(env, 43, nil)
	if !unseen.Unread {
		t.Errorf("no \\Seen flag must mean unread: %+v", unseen)
	}
	if got := summaryFromEnvelope(nil, 0, nil); got.Account != "workmail" || got.Unread != true {
		t.Errorf("nil envelope summary = %+v", got)
	}
}

func TestExtractPlainText(t *testing.T) {
	simple := "From: a@b.example\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nHello body\r\n"
	if got, err := extractPlainText([]byte(simple)); err != nil || !strings.Contains(got, "Hello body") {
		t.Errorf("simple = %q, %v", got, err)
	}
	multi := "Content-Type: multipart/alternative; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/html\r\n\r\n<p>html version</p>\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nplain version\r\n--B--\r\n"
	got, err := extractPlainText([]byte(multi))
	if err != nil || !strings.Contains(got, "plain version") {
		t.Errorf("multipart = %q, %v, want the plain part", got, err)
	}
}

func TestTextHelpers(t *testing.T) {
	if got := htmlToText("<p>Hello <b>there</b></p><p>Next &amp; more</p>"); got != "Hello there\nNext & more" {
		t.Errorf("htmlToText = %q", got)
	}
	quoted := "Sounds good.\n\nOn Tue, Sarah wrote:\n> earlier\n> stuff\n"
	if got := stripQuotedReply(quoted); strings.Contains(got, "earlier") || !strings.Contains(got, "Sounds good") {
		t.Errorf("stripQuotedReply = %q", got)
	}
	if got := truncate("hello world", 5); got != "hello…" {
		t.Errorf("truncate = %q", got)
	}
}
