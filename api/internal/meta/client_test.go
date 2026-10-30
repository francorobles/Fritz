package meta

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatAgainstFakeServer(t *testing.T) {
	var gotAuth, gotModel string
	var gotMessages []Message
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Model    string    `json:"model"`
			Messages []Message `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("request JSON: %v", err)
		}
		gotModel, gotMessages = req.Model, req.Messages
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"It is 9 AM."},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	old := BaseURL
	BaseURL = server.URL
	defer func() { BaseURL = old }()

	client := &Client{APIKey: "test-key"}
	reply, err := client.Chat(context.Background(), []Message{
		{Role: "system", Content: "be brief"},
		{Role: "user", Content: "what time is it"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reply != "It is 9 AM." {
		t.Errorf("reply = %q", reply)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Errorf("Authorization = %q, want a Bearer token", gotAuth)
	}
	if gotModel != ModelSpark || len(gotMessages) != 2 {
		t.Errorf("model/messages = %q/%+v", gotModel, gotMessages)
	}
}

func TestChatRetriesOnceOn500(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `{"error":{"message":"boom"}}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	old := BaseURL
	BaseURL = server.URL
	defer func() { BaseURL = old }()

	client := &Client{APIKey: "k"}
	reply, err := client.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil || reply != "ok" || calls != 2 {
		t.Errorf("reply=%q err=%v calls=%d, want ok after one retry", reply, err, calls)
	}
}

func TestChatDoesNotRetryOn400(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"message":"bad request"}}`)
	}))
	defer server.Close()

	old := BaseURL
	BaseURL = server.URL
	defer func() { BaseURL = old }()

	client := &Client{APIKey: "k"}
	if _, err := client.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}); err == nil {
		t.Fatal("expected an error on HTTP 400")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry on 400)", calls)
	}
}
