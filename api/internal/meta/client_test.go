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

func TestChatStreamAgainstFakeServer(t *testing.T) {
	var gotStream bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Stream bool `json:"stream"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("request JSON: %v", err)
		}
		gotStream = req.Stream
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, ": comment lines are ignored\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"It is \"}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"9 AM.\"}}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	old := BaseURL
	BaseURL = server.URL
	defer func() { BaseURL = old }()

	client := &Client{APIKey: "test-key"}
	var deltas []string
	reply, err := client.ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "what time is it"}},
		func(delta string) { deltas = append(deltas, delta) })
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if reply != "It is 9 AM." {
		t.Errorf("reply = %q", reply)
	}
	if strings.Join(deltas, "") != reply || len(deltas) != 2 {
		t.Errorf("deltas = %q, want the reply in two pieces", deltas)
	}
	if !gotStream {
		t.Error("request did not set stream:true")
	}
}

func TestChatStreamReturnsPartialOnMidError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"It is \"}}]}\n\n")
		io.WriteString(w, "data: not json\n\n")
	}))
	defer server.Close()

	old := BaseURL
	BaseURL = server.URL
	defer func() { BaseURL = old }()

	client := &Client{APIKey: "k"}
	reply, err := client.ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}},
		func(delta string) {})
	if err == nil {
		t.Fatal("expected an error on a malformed chunk")
	}
	if reply != "It is " {
		t.Errorf("partial reply = %q, want what arrived before the error", reply)
	}
}

func TestChatWithModelAndJSON(t *testing.T) {
	var gotModel, gotFormat string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Model          string `json:"model"`
			ResponseFormat struct {
				Type       string `json:"type"`
				JSONSchema struct {
					Name string `json:"name"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("request JSON: %v", err)
		}
		gotModel = req.Model
		gotFormat = req.ResponseFormat.Type + "/" + req.ResponseFormat.JSONSchema.Name
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"{\"add\":[]}"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	old := BaseURL
	BaseURL = server.URL
	defer func() { BaseURL = old }()

	client := &Client{APIKey: "test-key"}
	reply, err := client.ChatJSON(context.Background(), ModelWonder,
		[]Message{{Role: "user", Content: "consolidate"}},
		"memory_diff", map[string]any{"type": "object"})
	if err != nil {
		t.Fatalf("ChatJSON: %v", err)
	}
	if reply != `{"add":[]}` {
		t.Errorf("reply = %q", reply)
	}
	if gotModel != ModelWonder {
		t.Errorf("model = %q, want %q", gotModel, ModelWonder)
	}
	if gotFormat != "json_schema/memory_diff" {
		t.Errorf("response_format = %q, want json_schema/memory_diff", gotFormat)
	}

	// Plain Chat still defaults to Spark with no response_format.
	reply, err = client.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if gotModel != ModelSpark {
		t.Errorf("Chat model = %q, want %q", gotModel, ModelSpark)
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

// TestChatStreamToolCalls checks the tool-calling contract: tools
// and tool_choice "auto" go out, a call streamed in pieces comes
// back whole, and a follow-up request carries the assistant's
// tool_calls (content null) and the matching tool result.
func TestChatStreamToolCalls(t *testing.T) {
	var gotBodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("request JSON: %v", err)
		}
		gotBodies = append(gotBodies, req)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Let me check.\"}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"email_search\",\"arguments\":\"\"}}]}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"query\\\":\"}}]}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"is:unread\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	old := BaseURL
	BaseURL = server.URL
	defer func() { BaseURL = old }()

	client := &Client{APIKey: "k"}
	tools := []Tool{{Name: "email_search", Description: "Search mail.", Parameters: map[string]any{"type": "object"}}}
	var emitted strings.Builder
	res, err := client.ChatStreamTools(context.Background(),
		[]Message{{Role: "user", Content: "any new email?"}}, tools,
		func(delta string) { emitted.WriteString(delta) })
	if err != nil {
		t.Fatalf("ChatStreamTools: %v", err)
	}
	if res.Text != "Let me check." || emitted.String() != res.Text {
		t.Errorf("text = %q, emitted %q", res.Text, emitted.String())
	}
	if res.FinishReason != "tool_calls" || len(res.ToolCalls) != 1 {
		t.Fatalf("result = %+v, want one tool call", res)
	}
	call := res.ToolCalls[0]
	if call.ID != "call_1" || call.Type != "function" || call.Function.Name != "email_search" ||
		call.Function.Arguments != `{"query":"is:unread"}` {
		t.Errorf("call = %+v, want the pieces joined", call)
	}
	if gotBodies[0]["tool_choice"] != "auto" {
		t.Errorf("tool_choice = %v, want auto", gotBodies[0]["tool_choice"])
	}
	fn := gotBodies[0]["tools"].([]any)[0].(map[string]any)
	if fn["type"] != "function" || fn["function"].(map[string]any)["name"] != "email_search" {
		t.Errorf("tools[0] = %v, want a nested function definition", fn)
	}

	// Round two: the history the agent sends back.
	_, _ = client.ChatStreamTools(context.Background(), []Message{
		{Role: "user", Content: "any new email?"},
		{Role: "assistant", ToolCalls: res.ToolCalls},
		{Role: "tool", ToolCallID: "call_1", Content: `{"messages":[]}`},
	}, tools, func(string) {})
	msgs := gotBodies[1]["messages"].([]any)
	asst, tool := msgs[1].(map[string]any), msgs[2].(map[string]any)
	if c, ok := asst["content"]; !ok || c != nil {
		t.Errorf("assistant content = %v (present %v), want null", c, ok)
	}
	if _, ok := asst["tool_calls"]; !ok {
		t.Error("assistant message lost its tool_calls")
	}
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_1" {
		t.Errorf("tool message = %v", tool)
	}
	if _, ok := msgs[0].(map[string]any)["tool_calls"]; ok {
		t.Error("plain user message must not carry tool_calls")
	}
}
