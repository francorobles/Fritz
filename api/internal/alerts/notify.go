// Package alerts is Fritz reaching out on his own: an important
// email arrives while you're away, or someone walks up to your
// desk while sentry mode is on. Alerts go to the owner on WhatsApp
// (fritz-integrations skill); your replies come back through the
// webhook to the chat brain, which has the same tools as voice.
//
// The voice process runs the watchers (it has the camera and knows
// whether you're in the room); the HTTP server receives replies.
// They share the SQLite file, so the state they both need lives
// there: the sentry switch, what was already alerted, and when you
// last wrote (WhatsApp's 24 h window).
package alerts

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/francorobles/fritz/api/internal/integrations"
)

// State keys shared by both processes.
const (
	KeySentry       = "sentry"                 // "on" | "off"
	KeyLastInbound  = "whatsapp_last_inbound"  // value unused; updated_at is the time
	KeyPendingPhoto = "whatsapp_pending_photo" // path of a photo waiting for the window
)

// window is WhatsApp's customer-service window: free-form messages
// are allowed for 24 h after the user's last message. A margin
// keeps a slow send from landing just outside it.
const window = 24*time.Hour - 10*time.Minute

// StateStore is the slice of memory.Store alerts need.
type StateStore interface {
	SetState(key, value string) error
	State(key string) (value string, at time.Time, ok bool, err error)
	AddTurn(role, content string) error
}

// Sender is the slice of integrations.WhatsApp alerts need.
type Sender interface {
	SendText(ctx context.Context, to, body string) error
	SendTemplate(ctx context.Context, to, name, language string, params []string) error
	UploadMedia(ctx context.Context, jpeg []byte) (string, error)
	SendImage(ctx context.Context, to, mediaID, caption string) error
}

// Notifier sends alerts to the owner on WhatsApp.
//
// Outside the 24 h window WhatsApp only delivers approved
// templates, and it reports a free-form send there as accepted
// and fails it later, so the window is tracked here (from the
// webhook, via Inbound) rather than learned from errors. The alert
// template must have exactly one body variable, {{1}}, which
// carries the alert text; Meta's sample "hello_world" (no
// variables) works for wiring tests.
type Notifier struct {
	WA       Sender
	To       string // owner number
	Template string // approved alert template
	Language string // template language, default en_US
	State    StateStore
	PhotoDir string // where held-back photos wait
	Logf     func(string)
	Now      func() time.Time
}

// NewNotifier builds the owner's notifier from the WhatsApp config,
// or returns nil when WhatsApp isn't ready or no owner number is
// allowed. Both processes must pass the same dbPath: held-back
// photos live next to the database, where the server finds them.
func NewNotifier(cfg integrations.WhatsAppConfig, wa *integrations.WhatsApp, st StateStore, dbPath string) *Notifier {
	if wa == nil || len(cfg.Allow) == 0 {
		return nil
	}
	dir := filepath.Join(filepath.Dir(dbPath), "sentry")
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return &Notifier{WA: wa, To: cfg.Allow[0], Template: cfg.AlertTemplateOrDefault(), State: st, PhotoDir: dir}
}

func (n *Notifier) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

func (n *Notifier) logf(format string, args ...any) {
	if n.Logf != nil {
		n.Logf(fmt.Sprintf(format, args...))
	}
}

// WindowOpen reports whether free-form messages can reach the
// owner now (they wrote to Fritz within the last ~24 h).
func (n *Notifier) WindowOpen() bool {
	_, at, ok, err := n.State.State(KeyLastInbound)
	return err == nil && ok && n.now().Sub(at) < window
}

// Text sends an alert. The text is also kept as one of Fritz's
// turns, so a reply like "what did it say?" has context.
func (n *Notifier) Text(ctx context.Context, text string) error {
	var err error
	if n.WindowOpen() {
		err = n.WA.SendText(ctx, n.To, text)
	} else {
		err = n.WA.SendTemplate(ctx, n.To, n.Template, n.Language, templateParams(n.Template, text))
	}
	if err != nil {
		return err
	}
	if err := n.State.AddTurn("assistant", text); err != nil {
		n.logf("alerts: remember alert: %v", err)
	}
	return nil
}

// Photo sends a camera snapshot with a caption. With the window
// closed an image can't be sent at all, so the text goes out as a
// template and the photo waits on disk until the owner replies
// (Inbound sends it then).
func (n *Notifier) Photo(ctx context.Context, jpeg []byte, caption string) error {
	if n.WindowOpen() {
		id, err := n.WA.UploadMedia(ctx, jpeg)
		if err != nil {
			return err
		}
		if err := n.WA.SendImage(ctx, n.To, id, caption); err != nil {
			return err
		}
		if err := n.State.AddTurn("assistant", caption+" (sent a photo)"); err != nil {
			n.logf("alerts: remember alert: %v", err)
		}
		return nil
	}
	path, err := n.holdPhoto(jpeg)
	if err != nil {
		n.logf("alerts: could not keep the photo: %v", err)
		return n.Text(ctx, caption)
	}
	if err := n.State.SetState(KeyPendingPhoto, path); err != nil {
		n.logf("alerts: %v", err)
	}
	return n.Text(ctx, caption+" Reply here and I'll send the photo.")
}

func (n *Notifier) holdPhoto(jpeg []byte) (string, error) {
	dir := n.PhotoDir
	if dir == "" {
		dir = os.TempDir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "sentry-"+n.now().Format("20060102-150405")+".jpg")
	return path, os.WriteFile(path, jpeg, 0o600)
}

// Inbound is called by the webhook for every owner message: it
// opens the window and delivers a photo held back while it was
// closed. Errors are logged; the message is answered regardless.
func (n *Notifier) Inbound(ctx context.Context) {
	if err := n.State.SetState(KeyLastInbound, "1"); err != nil {
		n.logf("alerts: %v", err)
	}
	path, at, ok, err := n.State.State(KeyPendingPhoto)
	if err != nil || !ok || path == "" {
		return
	}
	_ = n.State.SetState(KeyPendingPhoto, "")
	if n.now().Sub(at) > 24*time.Hour {
		return // stale: not worth sending
	}
	jpeg, err := os.ReadFile(path)
	if err != nil {
		n.logf("alerts: held photo: %v", err)
		return
	}
	id, err := n.WA.UploadMedia(ctx, jpeg)
	if err == nil {
		err = n.WA.SendImage(ctx, n.To, id, "Here's who came by at "+at.Local().Format("3:04 PM")+".")
	}
	if err != nil {
		n.logf("alerts: send held photo: %v", err)
	}
}

// templateParams fills the alert template's single {{1}}. WhatsApp
// rejects template variables with newlines, tabs or long runs of
// spaces, and caps their length.
func templateParams(template, text string) []string {
	if template == "" || template == "hello_world" {
		return nil
	}
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > 900 {
		text = string(r[:899]) + "…"
	}
	return []string{text}
}
