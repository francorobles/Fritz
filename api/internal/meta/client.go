// Package meta is Fritz's small client for the Meta Model API
// (https://api.meta.ai/v1, OpenAI-compatible). Plain net/http structs,
// no SDK. See the meta-model-api skill for the contract.
package meta

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// BaseURL is a var so tests can point it at a fake server.
var BaseURL = "https://api.meta.ai/v1"

// Model IDs (standard, non-contributor: Fritz handles personal data).
const (
	ModelSpark      = "muse-spark-1.3"            // conversation, tool calling, vision
	ModelWonder     = "muse-wonder-1.0"           // nightly memory consolidation
	ModelTranscribe = "muse-voice-transcribe-1.0" // speech to text
)

// Message is one chat message.
type Message struct {
	Role    string `json:"role"` // system | user | assistant | tool
	Content string `json:"content"`

	// Image, when set on a user message, attaches a JPEG photo:
	// on the wire the content becomes text + image_url parts
	// (Muse Spark vision input), so the model can answer
	// questions about what the camera sees. Never set on history
	// or assistant messages.
	Image []byte `json:"-"`

	// ToolCalls is set on an assistant message that asked for tools;
	// it must be sent back verbatim before the tool results (the
	// API returns 400 otherwise). ToolCallID ties a role "tool"
	// message to the call it answers.
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is one function call the model asked for. Arguments is
// a JSON string, not guaranteed to match the schema: validate it.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall names the function and carries its JSON arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool is one function the model may call. Name must match
// ^[a-zA-Z0-9_.-]+$ with at most one dot; Parameters is a JSON
// Schema object.
type Tool struct {
	Name        string
	Description string
	Parameters  any
}

func (t Tool) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"parameters":  t.Parameters,
		},
	})
}

// wireMessage is the on-the-wire chat message. Content is the
// plain string normally, or text+image parts with a photo, or null
// on an assistant message that only calls tools.
type wireMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type wireImageURL struct {
	URL string `json:"url"`
}

type wireContentPart struct {
	Type     string        `json:"type"` // text | image_url
	Text     string        `json:"text,omitempty"`
	ImageURL *wireImageURL `json:"image_url,omitempty"`
}

func toWireMessages(messages []Message) []wireMessage {
	out := make([]wireMessage, len(messages))
	for i, m := range messages {
		out[i] = wireMessage{Role: m.Role, Content: m.Content, ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID}
		if len(m.ToolCalls) > 0 && m.Content == "" {
			out[i].Content = nil
		}
		if len(m.Image) == 0 {
			continue
		}
		out[i].Content = []wireContentPart{
			{Type: "text", Text: m.Content},
			{Type: "image_url", ImageURL: &wireImageURL{
				URL: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(m.Image),
			}},
		}
	}
	return out
}

// Client calls the Meta Model API with one API key.
type Client struct {
	APIKey string
	HTTP   *http.Client // nil means a default with a 30 s timeout
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []wireMessage `json:"messages"`
	Stream         bool          `json:"stream,omitempty"`
	ResponseFormat any           `json:"response_format,omitempty"`
	Tools          []Tool        `json:"tools,omitempty"`
	ToolChoice     string        `json:"tool_choice,omitempty"` // only "auto" is accepted
}

type chatResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Chat sends messages to Muse Spark and returns the assistant's reply
// text. Retries 429/5xx once with a short backoff, per the skill.
func (c *Client) Chat(ctx context.Context, messages []Message) (string, error) {
	return c.ChatWithModel(ctx, ModelSpark, messages)
}

// ChatWithModel is Chat with an explicit model ID — e.g. ModelWonder
// for the nightly memory consolidation, which wants the biggest
// reasoning model and no latency pressure.
func (c *Client) ChatWithModel(ctx context.Context, model string, messages []Message) (string, error) {
	return c.chatWithFormat(ctx, model, messages, nil)
}

// ChatJSON is ChatWithModel with a JSON Schema in response_format,
// so the reply is constrained to JSON matching schema (structured
// output; name identifies the schema). Callers parse the returned
// JSON themselves — it matches the schema but is still model output.
func (c *Client) ChatJSON(ctx context.Context, model string, messages []Message, name string, schema any) (string, error) {
	return c.chatWithFormat(ctx, model, messages, map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   name,
			"schema": schema,
		},
	})
}

func (c *Client) chatWithFormat(ctx context.Context, model string, messages []Message, format any) (string, error) {
	body, err := json.Marshal(chatRequest{Model: model, Messages: toWireMessages(messages), ResponseFormat: format})
	if err != nil {
		return "", err
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(600 * time.Millisecond):
			}
		}
		reply, retryable, err := c.chatOnce(ctx, body)
		if err == nil {
			return reply, nil
		}
		lastErr = err
		if !retryable {
			return "", err
		}
	}
	return "", lastErr
}

// chatChunk is one SSE data line of a streaming chat response.
type chatChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// StreamResult is one streamed reply: the text that was emitted,
// plus any tool calls the model asked for (assembled from their
// deltas, in index order).
type StreamResult struct {
	Text         string
	ToolCalls    []ToolCall
	FinishReason string
}

// ChatStream sends messages to Muse Spark with streaming enabled,
// calls emit for each content delta as it arrives, and returns the
// full reply text. emit must be non-nil; it runs on the caller's
// goroutine, so slow handlers stall the stream (that is how the
// voice loop paces speech against synthesis).
//
// Like Chat it retries 429/5xx once — but only when nothing was
// received yet. A stream that dies mid-reply returns the partial
// text with the error, so callers can still use what arrived.
func (c *Client) ChatStream(ctx context.Context, messages []Message, emit func(delta string)) (string, error) {
	res, err := c.ChatStreamTools(ctx, messages, nil, emit)
	return res.Text, err
}

// ChatStreamTools is ChatStream with tools the model may call.
// Text deltas are emitted as they arrive (a model often says "Let
// me check." before calling a tool); tool calls come back whole in
// the result, for the caller to run and answer. No tools means a
// plain chat request.
func (c *Client) ChatStreamTools(ctx context.Context, messages []Message, tools []Tool, emit func(delta string)) (StreamResult, error) {
	req := chatRequest{Model: ModelSpark, Messages: toWireMessages(messages), Stream: true}
	if len(tools) > 0 {
		req.Tools, req.ToolChoice = tools, "auto"
	}
	body, err := json.Marshal(req)
	if err != nil {
		return StreamResult{}, err
	}
	var lastErr error
	var partial StreamResult
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return partial, ctx.Err()
			case <-time.After(600 * time.Millisecond):
			}
		}
		res, retryable, err := c.streamOnce(ctx, body, emit)
		if err == nil {
			return res, nil
		}
		partial, lastErr = res, err
		if !retryable {
			return partial, err
		}
	}
	return partial, lastErr
}

func (c *Client) streamOnce(ctx context.Context, body []byte, emit func(delta string)) (res StreamResult, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return res, false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return res, false, fmt.Errorf("meta: chat stream request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return res, retry, fmt.Errorf("meta: chat stream HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var full strings.Builder
	// Tool calls stream in pieces keyed by index: the first piece
	// usually carries the id and name, later ones append arguments.
	calls := map[int]*ToolCall{}
	var order []int
	received := false
	finish := func() StreamResult {
		out := StreamResult{Text: full.String(), FinishReason: res.FinishReason}
		sort.Ints(order)
		for _, i := range order {
			call := *calls[i]
			if call.Type == "" {
				call.Type = "function"
			}
			out.ToolCalls = append(out.ToolCalls, call)
		}
		return out
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue // blank, comment, or another SSE field
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return finish(), false, fmt.Errorf("meta: chat stream chunk: %w", err)
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				res.FinishReason = choice.FinishReason
			}
			for _, d := range choice.Delta.ToolCalls {
				received = true
				call, ok := calls[d.Index]
				if !ok {
					call = &ToolCall{}
					calls[d.Index] = call
					order = append(order, d.Index)
				}
				if d.ID != "" {
					call.ID = d.ID
				}
				if d.Type != "" {
					call.Type = d.Type
				}
				if d.Function.Name != "" {
					call.Function.Name = d.Function.Name
				}
				call.Function.Arguments += d.Function.Arguments
			}
			if choice.Delta.Content == "" {
				continue
			}
			received = true
			full.WriteString(choice.Delta.Content)
			emit(choice.Delta.Content)
		}
	}
	if err := scanner.Err(); err != nil {
		// A failure before anything arrived is worth one retry,
		// like a failed non-streaming call; afterwards the caller
		// speaks the partial reply instead.
		return finish(), !received, fmt.Errorf("meta: chat stream read: %w", err)
	}
	return finish(), false, nil
}

func (c *Client) chatOnce(ctx context.Context, body []byte) (reply string, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", false, fmt.Errorf("meta: chat request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusOK {
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return "", retry, fmt.Errorf("meta: chat HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	var out chatResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", false, fmt.Errorf("meta: chat response: %w", err)
	}
	if out.Error != nil {
		return "", false, fmt.Errorf("meta: chat error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", false, fmt.Errorf("meta: chat returned no choices: %s", string(respBody))
	}
	return out.Choices[0].Message.Content, false, nil
}
