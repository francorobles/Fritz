package chat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/francorobles/fritz/api/internal/agent"
	"github.com/francorobles/fritz/api/internal/alerts"
	"github.com/francorobles/fritz/api/internal/memory"
	"github.com/francorobles/fritz/api/internal/meta"
)

// "I'm back" on WhatsApp reaches the same sentry switch the voice
// process reads: the brain runs the tool loop, not a plain chat.
func TestBrainUsesTools(t *testing.T) {
	st, err := memory.Open(filepath.Join(t.TempDir(), "fritz.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := alerts.SetSentry(st, true); err != nil {
		t.Fatal(err)
	}
	round := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		round++
		w.Header().Set("Content-Type", "text/event-stream")
		if round == 1 {
			if !strings.Contains(string(body), `"sentry_mode"`) {
				t.Errorf("tools not offered to the model: %s", body)
			}
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"type\":\"function\",\"function\":{\"name\":\"sentry_mode\",\"arguments\":\"{\\\"on\\\":false}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		} else {
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Welcome back, I've stopped watching.\"}}]}\n\n")
		}
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	old := meta.BaseURL
	meta.BaseURL = server.URL
	defer func() { meta.BaseURL = old }()

	brain := &Brain{Client: &meta.Client{APIKey: "k"}, Store: st, Tools: agent.SentryTool(st, nil)}
	reply, err := brain.Reply(context.Background(), "I'm back")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "Welcome back, I've stopped watching." {
		t.Errorf("reply = %q", reply)
	}
	if on, _ := alerts.SentryArmed(st); on {
		t.Error("sentry still on after the tool call")
	}
}
