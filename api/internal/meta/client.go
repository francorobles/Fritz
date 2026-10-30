// Package meta is Fritz's small client for the Meta Model API
// (https://api.meta.ai/v1, OpenAI-compatible). Plain net/http structs,
// no SDK. See the meta-model-api skill for the contract.
package meta

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
