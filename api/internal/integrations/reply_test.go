package integrations

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// TestGmailReply walks the whole reply path against a fake Gmail:
// the recipient comes from Reply-To, the subject gains one "Re:",
// threading headers are set, and the sent message stays in the
// original thread.
func TestGmailReply(t *testing.T) {
	var sent gmailapi.Message
	transport := transportFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/messages/m1"):
			return jsonResponse(200, `{
				"id": "m1", "threadId": "t1",
				"payload": {"headers": [
					{"name": "From", "value": "Sarah Lim <sarah@example.com>"},
					{"name": "Reply-To", "value": "Sarah Lim <sarah.lim@example.com>"},
					{"name": "Subject", "value": "Thursday?"},
					{"name": "Message-ID", "value": "<abc@example.com>"},
					{"name": "References", "value": "<root@example.com>"}
				]}}`), nil
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/messages/send"):
			body, _ := io.ReadAll(req.Body)
			if err := json.Unmarshal(body, &sent); err != nil {
				t.Errorf("send body: %v", err)
			}
			return jsonResponse(200, `{"id":"m2","threadId":"t1"}`), nil
		}
		return jsonResponse(404, `{"error":{"message":"nope"}}`), nil
	})
	svc, err := gmailapi.NewService(context.Background(),
		option.WithHTTPClient(&http.Client{Transport: transport}), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	g := &Gmail{svc: svc}

	r, err := g.PrepareReply(context.Background(), "m1")
	if err != nil {
		t.Fatalf("PrepareReply: %v", err)
	}
	if r.Name != "Sarah Lim" || !strings.Contains(r.To, "sarah.lim@example.com") || r.Subject != "Re: Thursday?" ||
		r.ThreadID != "t1" || r.References != "<root@example.com> <abc@example.com>" {
		t.Errorf("reply = %+v", r)
	}
	if err := g.SendReply(context.Background(), r, "Thursday works for me.\nSee you then."); err != nil {
		t.Fatalf("SendReply: %v", err)
	}
	raw, err := base64.URLEncoding.DecodeString(sent.Raw)
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	msg := string(raw)
	for _, want := range []string{"To: \"Sarah Lim\" <sarah.lim@example.com>\r\n", "Subject: Re: Thursday?\r\n",
		"In-Reply-To: <abc@example.com>\r\n", "\r\n\r\nThursday works for me.\r\nSee you then.\r\n"} {
		if !strings.Contains(msg, want) {
			t.Errorf("sent message missing %q:\n%s", want, msg)
		}
	}
	if sent.ThreadId != "t1" {
		t.Errorf("threadId = %q, want t1", sent.ThreadId)
	}
}

func TestBuildReplyBlocksHeaderInjection(t *testing.T) {
	raw := string(buildReply(Reply{To: "a@example.com", Subject: "Re: hi\r\nBcc: evil@example.com"}, "body"))
	if strings.Contains(raw, "\r\nBcc:") {
		t.Errorf("header injection got through:\n%s", raw)
	}
}
