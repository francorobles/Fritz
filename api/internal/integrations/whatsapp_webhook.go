package integrations

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Replier answers one chat message as Fritz. The shared chat
// brain (api/internal/chat) implements it; keeping the
// interface here means this package never imports the brain.
type Replier interface {
	Reply(ctx context.Context, text string) (string, error)
}

// Webhook is the WhatsApp inbound endpoint: Meta subscribes it
// once (GET challenge), then POSTs every message event. Only
// allow-listed owner numbers are answered; everyone else is
// ignored silently. Replies go out asynchronously so the ACK
// returns fast (Meta retries slow webhooks, and duplicates are
// worse than latency here — message ids are deduped regardless).
//
// Mount it at /webhooks/whatsapp and expose it publicly
// (Tailscale Funnel or your tunnel of choice).
type Webhook struct {
	Client      *WhatsApp
	VerifyToken string
	AppSecret   string // when set, POST signatures are required
	Allow       []string
	Brain       Replier
	Logf        func(string)

	mu   sync.Mutex
	seen map[string]time.Time
}

func (h *Webhook) logf(format string, args ...any) {
	if h.Logf != nil {
		h.Logf(fmt.Sprintf(format, args...))
	}
}

// ServeHTTP implements http.Handler (GET verify, POST events).
func (h *Webhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.verify(w, r)
	case http.MethodPost:
		h.events(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Webhook) verify(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	challenge, ok := VerifySubscription(
		q.Get("hub.mode"), q.Get("hub.verify_token"), q.Get("hub.challenge"), h.VerifyToken)
	if !ok {
		http.Error(w, "verification failed", http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, challenge)
}

func (h *Webhook) events(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	if h.AppSecret != "" && !ValidSignature(h.AppSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		h.logf("whatsapp: webhook with bad signature dropped")
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	msgs, err := ParseInbound(body)
	if err != nil {
		// A malformed POST is not Meta retrying usefully; ACK
		// anyway so it does not loop forever, and log it.
		h.logf("whatsapp: unparseable webhook: %v", err)
		w.WriteHeader(http.StatusOK)
		return
	}
	for _, msg := range msgs {
		if !AllowedNumber(h.Allow, msg.From) {
			h.logf("whatsapp: message from non-owner number ignored")
			continue
		}
		if h.duplicate(msg.ID) {
			continue
		}
		go h.answer(msg)
	}
	w.WriteHeader(http.StatusOK)
}

// duplicate records a message id and reports repeats (Meta
// delivers at least once). Old ids are pruned lazily.
func (h *Webhook) duplicate(id string) bool {
	if id == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.seen == nil {
		h.seen = make(map[string]time.Time)
	}
	if _, ok := h.seen[id]; ok {
		return true
	}
	h.seen[id] = time.Now()
	if len(h.seen) > 1000 {
		cutoff := time.Now().Add(-24 * time.Hour)
		for k, t := range h.seen {
			if t.Before(cutoff) {
				delete(h.seen, k)
			}
		}
	}
	return false
}

func (h *Webhook) answer(msg InboundMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	reply, err := h.Brain.Reply(ctx, msg.Text)
	if err != nil {
		h.logf("whatsapp: brain: %v", err)
		return
	}
	if err := h.Client.SendText(ctx, msg.From, reply); err != nil {
		h.logf("whatsapp: reply send: %v", err)
	}
}
