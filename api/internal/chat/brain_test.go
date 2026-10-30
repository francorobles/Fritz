package chat

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/francorobles/fritz/api/internal/memory"
	"github.com/francorobles/fritz/api/internal/meta"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBrainReplyPersistsTurns(t *testing.T) {
	store, err := memory.Open(filepath.Join(t.TempDir(), "fritz.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Remember(memory.KindRoutine, "User clears email around 9 AM", "test", 0.8); err != nil {
		t.Fatal(err)
	}

	var requestBody string
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		requestBody = string(body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"choices":[{"message":{"role":"assistant","content":"It is 9 AM."},"finish_reason":"stop"}]}`)),
		}, nil
	})
	brain := &Brain{
		Client: &meta.Client{APIKey: "k", HTTP: &http.Client{Transport: transport}},
		Store:  store,
	}

	reply, err := brain.Reply(context.Background(), "what time is it")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "It is 9 AM." {
		t.Errorf("reply = %q", reply)
	}
	// Persona + memory snapshot reached the model.
	if !strings.Contains(requestBody, "You are Fritz") || !strings.Contains(requestBody, "clears email around 9 AM") {
		t.Errorf("request missing persona/snapshot: %.200s", requestBody)
	}
	turns, err := store.RecentTurns(10)
	if err != nil || len(turns) != 2 || turns[0].Content != "what time is it" || turns[1].Content != "It is 9 AM." {
		t.Errorf("turns = %+v, %v — the exchange must persist for later context", turns, err)
	}

	// A second message sees the first exchange as history.
	if _, err := brain.Reply(context.Background(), "and tomorrow"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(requestBody, "It is 9 AM.") {
		t.Error("second request did not include the first exchange in history")
	}
}
