package integrations

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

func fakeGmail(t *testing.T) *Gmail {
	t.Helper()
	bodyText := "Thursday works for me.\n\nOn Mon, Sarah wrote:\n> old stuff\n"
	encoded := base64.RawURLEncoding.EncodeToString([]byte(bodyText))
	transport := transportFunc(func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "/messages") {
			return jsonResponse(200, `{"messages":[{"id":"m1"}]}`), nil
		}
		if strings.HasSuffix(req.URL.Path, "/messages/m1") {
			return jsonResponse(200, `{
				"id": "m1", "snippet": "Thursday works", "labelIds": ["UNREAD", "INBOX"],
				"payload": {
					"mimeType": "multipart/mixed",
					"headers": [
						{"name": "From", "value": "Sarah <sarah@example.com>"},
						{"name": "Subject", "value": "Thursday"},
						{"name": "Date", "value": "Tue, 15 Oct 2030 09:00:00 +0800"}
					],
					"parts": [
						{"mimeType": "text/plain", "body": {"data": "`+encoded+`"}}
					]
				}}`), nil
		}
		return jsonResponse(404, `{"error":{"message":"nope"}}`), nil
	})
	svc, err := gmailapi.NewService(context.Background(),
		option.WithHTTPClient(&http.Client{Transport: transport}),
		option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	return &Gmail{svc: svc}
}

func TestGmailSearchAndRead(t *testing.T) {
	g := fakeGmail(t)
	sums, err := g.Search(context.Background(), "thursday", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 {
		t.Fatalf("Search = %d summaries, want 1", len(sums))
	}
	s := sums[0]
	if s.From != "Sarah <sarah@example.com>" || s.Subject != "Thursday" || !s.Unread || s.Date.Year() != 2030 {
		t.Errorf("summary = %+v", s)
	}
	msg, err := g.Read(context.Background(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.Body, "Thursday works for me.") || strings.Contains(msg.Body, "old stuff") {
		t.Errorf("body = %q, want new text without the quoted tail", msg.Body)
	}
}
