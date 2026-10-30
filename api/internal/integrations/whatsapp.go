package integrations

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// WhatsApp is the WhatsApp Business Cloud API, both ways:
// Fritz sends you alerts (template messages — required for
// business-initiated contact) and replies to your messages
// (free-form text, allowed inside the 24 h window your message
// opens). Inbound arrives at the webhook (whatsapp_webhook.go).
type WhatsApp struct {
	http    *http.Client
	base    string // overridable in tests
	version string
	phoneID string
	token   string
}

// NewWhatsApp builds the client. The token is resolved by the
// caller from cfg.TokenEnv and kept in memory only.
func NewWhatsApp(cfg WhatsAppConfig, token string) *WhatsApp {
	return &WhatsApp{
		http:    &http.Client{Timeout: 30 * time.Second},
		base:    "https://graph.facebook.com",
		version: cfg.GraphVersionOrDefault(),
		phoneID: cfg.PhoneNumberID,
		token:   token,
	}
}

// SendText sends a free-form message — valid for replies within
// 24 h of the user's last message (i.e. all chat replies).
func (w *WhatsApp) SendText(ctx context.Context, to, body string) error {
	return w.post(ctx, map[string]any{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                digitsOnly(to),
		"type":              "text",
		"text":              map[string]any{"body": body, "preview_url": false},
	})
}

// SendTemplate sends a pre-approved template (required for
// Fritz-initiated alerts outside the 24 h window). params fill
// the template's {{1}}..{{n}} body placeholders.
func (w *WhatsApp) SendTemplate(ctx context.Context, to, name, language string, params []string) error {
	if language == "" {
		language = "en_US"
	}
	template := map[string]any{"name": name, "language": map[string]string{"code": language}}
	if len(params) > 0 {
		parameters := make([]map[string]string, len(params))
		for i, p := range params {
			parameters[i] = map[string]string{"type": "text", "text": p}
		}
		template["components"] = []any{map[string]any{"type": "body", "parameters": parameters}}
	}
	return w.post(ctx, map[string]any{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                digitsOnly(to),
		"type":              "template",
		"template":          template,
	})
}

func (w *WhatsApp) post(ctx context.Context, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/%s/%s/messages", w.base, w.version, w.phoneID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+w.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.http.Do(req)
	if err != nil {
		return fmt.Errorf("whatsapp: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var graphErr struct {
			Error struct {
				Message string `json:"message"`
				Code    int    `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(respBody, &graphErr); err == nil && graphErr.Error.Message != "" {
			return fmt.Errorf("whatsapp: HTTP %d (code %d): %s", resp.StatusCode, graphErr.Error.Code, graphErr.Error.Message)
		}
		return fmt.Errorf("whatsapp: HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 300))
	}
	return nil
}

// --- Inbound (webhook payloads) ---

// InboundMessage is one text message from the user. WhatsApp
// also delivers status receipts (sent/delivered/read) on the
// same webhook; ParseInbound ignores everything that is not a
// user text message.
type InboundMessage struct {
	ID        string
	From      string // digits, with country code
	Text      string
	Timestamp time.Time
}

type webhookPayload struct {
	Entry []struct {
		Changes []struct {
			Value struct {
				Messages []struct {
					ID        string `json:"id"`
					From      string `json:"from"`
					Timestamp string `json:"timestamp"`
					Type      string `json:"type"`
					Text      struct {
						Body string `json:"body"`
					} `json:"text"`
				} `json:"messages"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

// ParseInbound extracts user text messages from a webhook body.
func ParseInbound(body []byte) ([]InboundMessage, error) {
	var payload webhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("whatsapp: parse webhook: %w", err)
	}
	var out []InboundMessage
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			for _, msg := range change.Value.Messages {
				if msg.Type != "text" || msg.Text.Body == "" {
					continue
				}
				inbound := InboundMessage{ID: msg.ID, From: msg.From, Text: msg.Text.Body}
				if ts, err := parseUnix(msg.Timestamp); err == nil {
					inbound.Timestamp = ts
				}
				out = append(out, inbound)
			}
		}
	}
	return out, nil
}

func parseUnix(s string) (time.Time, error) {
	var sec int64
	if _, err := fmt.Sscanf(s, "%d", &sec); err != nil {
		return time.Time{}, err
	}
	return time.Unix(sec, 0), nil
}

// VerifySubscription answers Meta's webhook registration
// challenge (GET with hub.mode/hub.verify_token/hub.challenge).
func VerifySubscription(mode, token, challenge, expectedToken string) (string, bool) {
	if mode == "subscribe" && expectedToken != "" && token == expectedToken {
		return challenge, true
	}
	return "", false
}

// ValidSignature checks X-Hub-Signature-256 ("sha256=<hex>")
// against the app secret — proves the POST really came from Meta.
func ValidSignature(appSecret string, body []byte, header string) bool {
	if appSecret == "" || !strings.HasPrefix(header, "sha256=") {
		return false
	}
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(header))
}

// AllowedNumber reports whether from is an owner number.
// Comparison is digits-only (config and webhook both carry
// country-code forms); an empty allowlist allows nobody.
func AllowedNumber(allow []string, from string) bool {
	from = digitsOnly(from)
	if from == "" {
		return false
	}
	for _, entry := range allow {
		if digitsOnly(entry) == from {
			return true
		}
	}
	return false
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
