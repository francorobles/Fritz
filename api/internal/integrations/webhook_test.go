package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type stubBrain struct {
	mu  sync.Mutex
	got []string
}

func (b *stubBrain) Reply(_ context.Context, text string) (string, error) {
	b.mu.Lock()
	b.got = append(b.got, text)
	b.mu.Unlock()
	return "On it.", nil
}

func (b *stubBrain) calls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.got)
}

func signedPost(t *testing.T, hook *Webhook, secret, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/whatsapp", strings.NewReader(body))
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(body))
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	rec := httptest.NewRecorder()
	hook.ServeHTTP(rec, req)
	return rec
}

func TestWebhookTwoWayChat(t *testing.T) {
	brain := &stubBrain{}
	hook := &Webhook{
		Client:      fakeWhatsApp(t, nil),
		VerifyToken: "vtok",
		AppSecret:   "appsecret",
		Allow:       []string{"639175550101"},
		Brain:       brain,
	}

	// Subscription challenge.
	req := httptest.NewRequest(http.MethodGet,
		"/webhooks/whatsapp?hub.mode=subscribe&hub.verify_token=vtok&hub.challenge=42", nil)
	rec := httptest.NewRecorder()
	hook.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "42" {
		t.Errorf("verify: %d %q", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet,
		"/webhooks/whatsapp?hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=42", nil)
	rec = httptest.NewRecorder()
	hook.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("bad verify token: %d, want 403", rec.Code)
	}

	// Owner texts Fritz -> brain answers (async) -> reply sent.
	if rec := signedPost(t, hook, "appsecret", webhookBody); rec.Code != http.StatusOK {
		t.Fatalf("owner POST: %d", rec.Code)
	}
	deadline := time.Now().Add(2 * time.Second)
	for brain.calls() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if brain.calls() != 1 {
		t.Fatalf("brain calls = %d, want 1", brain.calls())
	}

	// Meta retrying the same message id must not re-answer.
	_ = signedPost(t, hook, "appsecret", webhookBody)
	time.Sleep(50 * time.Millisecond)
	if brain.calls() != 1 {
		t.Errorf("duplicate delivery: brain calls = %d, want 1", brain.calls())
	}

	// A stranger's number is ignored.
	stranger := strings.Replace(webhookBody, "639175550101", "639999999999", 1)
	stranger = strings.Replace(stranger, "wamid.HBg1", "wamid.XXX", 1)
	_ = signedPost(t, hook, "appsecret", stranger)
	time.Sleep(50 * time.Millisecond)
	if brain.calls() != 1 {
		t.Errorf("stranger answered: brain calls = %d, want 1", brain.calls())
	}

	// Bad signature is refused.
	bad := httptest.NewRequest(http.MethodPost, "/webhooks/whatsapp", strings.NewReader(webhookBody))
	bad.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	rec = httptest.NewRecorder()
	hook.ServeHTTP(rec, bad)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("bad signature: %d, want 401", rec.Code)
	}
}
