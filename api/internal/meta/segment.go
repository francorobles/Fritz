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
	"strings"
	"time"
)

// ModelSAM segments objects out of images and video by text prompt.
const ModelSAM = "sam-3.1"

type segmentRequest struct {
	Model  string           `json:"model"`
	Stream bool             `json:"stream"`
	Input  []segmentMessage `json:"input"`
}

type segmentMessage struct {
	Type    string        `json:"type"`
	Role    string        `json:"role"`
	Content []segmentPart `json:"content"`
}

type segmentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

// Segment finds prompt ("person", "face", "keys", ...) in one JPEG
// frame via SAM 3.1 over the Responses API and returns the raw
// output_text lane — one line per frame of box/mask token records
// (see the reading-segmentation docs; api/internal/vision parses
// the boxes). Zero matches is valid and returns "".
func (c *Client) Segment(ctx context.Context, jpeg []byte, prompt string) (string, error) {
	reqBody := segmentRequest{
		Model:  ModelSAM,
		Stream: true,
		Input: []segmentMessage{{
			Type: "message",
			Role: "user",
			Content: []segmentPart{
				{Type: "input_text", Text: prompt},
				{Type: "input_image", ImageURL: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpeg)},
			},
		}},
	}
	body, err := json.Marshal(reqBody)
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
		text, retryable, err := c.segmentOnce(ctx, body)
		if err == nil {
			return text, nil
		}
		partial, lastErr = text, err
		if !retryable {
			break
		}
	}
	return partial, lastErr
}

func (c *Client) segmentOnce(ctx context.Context, body []byte) (text string, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", false, fmt.Errorf("meta: segment request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return "", retry, fmt.Errorf("meta: segment HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var out strings.Builder
	received := false
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var event struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue // other Responses events; only deltas matter
		}
		switch event.Type {
		case "response.output_text.delta":
			received = true
			out.WriteString(event.Delta)
		case "response.completed":
			return out.String(), false, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return out.String(), !received, fmt.Errorf("meta: segment read: %w", err)
	}
	return out.String(), false, nil
}
