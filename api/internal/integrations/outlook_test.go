package integrations

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func fakeOutlook(t *testing.T) *Outlook {
	t.Helper()
	transport := transportFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/me/messages/") {
			return jsonResponse(200, `{
				"id": "A1", "subject": "Build broken", "receivedDateTime": "2030-10-15T01:00:00Z",
				"bodyPreview": "CI failed", "isRead": false,
				"from": {"emailAddress": {"name": "CI Bot", "address": "ci@corp.example"}},
				"body": {"contentType": "html", "content": "<p>Hello <b>there</b></p>"}
			}`), nil
		}
		if got := req.Header.Get("ConsistencyLevel"); strings.Contains(req.URL.RawQuery, "%24search") && got != "eventual" {
			t.Errorf("$search without ConsistencyLevel: eventual (got %q)", got)
		}
		return jsonResponse(200, `{"value": [{
			"id": "A1", "subject": "Build broken", "receivedDateTime": "2030-10-15T01:00:00Z",
			"bodyPreview": "CI failed", "isRead": false,
			"from": {"emailAddress": {"name": "CI Bot", "address": "ci@corp.example"}}
		}]}`), nil
	})
	return &Outlook{http: &http.Client{Transport: transport}, base: "https://graph.example"}
}

func TestOutlookSearchAndRead(t *testing.T) {
	o := fakeOutlook(t)
	sums, err := o.Search(context.Background(), "build", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].From != "CI Bot <ci@corp.example>" || !sums[0].Unread {
		t.Errorf("Search = %+v", sums)
	}
	msg, err := o.Read(context.Background(), "A1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(msg.Body) != "Hello there" {
		t.Errorf("Read body = %q, want HTML flattened to text", msg.Body)
	}
}
