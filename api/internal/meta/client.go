// Package meta is Fritz's small client for the Meta Model API
// (https://api.meta.ai/v1, OpenAI-compatible). Plain net/http structs,
// no SDK. See the meta-model-api skill for the contract.
package meta

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream,omitempty"`
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
	body, err := json.Marshal(chatRequest{Model: ModelSpark, Messages: messages})
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
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
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
	body, err := json.Marshal(chatRequest{Model: ModelSpark, Messages: messages, Stream: true})
	if err != nil {
		return "", err
	}
	var lastErr error
	var partial string
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return partial, ctx.Err()
			case <-time.After(600 * time.Millisecond):
			}
		}
		text, retryable, err := c.streamOnce(ctx, body, emit)
		if err == nil {
			return text, nil
		}
		partial, lastErr = text, err
		if !retryable {
			return partial, err
		}
	}
	return partial, lastErr
}

func (c *Client) streamOnce(ctx context.Context, body []byte, emit func(delta string)) (text string, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", false, fmt.Errorf("meta: chat stream request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return "", retry, fmt.Errorf("meta: chat stream HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var full strings.Builder
	received := false
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
			return full.String(), false, fmt.Errorf("meta: chat stream chunk: %w", err)
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content == "" {
				continue
			}
			received = true
			full.WriteString(choice.Delta.Content)
			emit(choice.Delta.Content)
		}
	}
	if err := scanner.Err(); err != nil {
		// A failure before any content is worth one retry, like a
		// failed non-streaming call; afterwards the caller speaks
		// the partial reply instead.
		return full.String(), !received, fmt.Errorf("meta: chat stream read: %w", err)
	}
	return full.String(), false, nil
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
