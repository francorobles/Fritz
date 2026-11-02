package httpapi

import (
	"net/http"
	"time"

	"github.com/francorobles/fritz/api/internal/alerts"
)

// statusResponse is the dashboard's "right now" card: the modes
// the voice process sets in the shared database.
type statusResponse struct {
	Focus struct {
		On    bool       `json:"on"`
		Until *time.Time `json:"until,omitempty"`
	} `json:"focus"`
	Held     []alerts.Held    `json:"held"`
	Progress *alerts.Progress `json:"progress,omitempty"`
	Sentry   struct {
		On    bool       `json:"on"`
		Since *time.Time `json:"since,omitempty"`
	} `json:"sentry"`
	Rules []string `json:"rules"`
}

func (h *handlers) status(w http.ResponseWriter, _ *http.Request) {
	if !h.storeOrError(w) {
		return
	}
	var out statusResponse
	focus := &alerts.Focus{State: h.store}
	if until, on := focus.Active(); on {
		out.Focus.On, out.Focus.Until = true, &until
	}
	out.Held = focus.Held()
	if out.Held == nil {
		out.Held = []alerts.Held{}
	}
	if p, ok := alerts.LoadProgress(h.store); ok {
		out.Progress = &p
	}
	if on, since := alerts.SentryArmed(h.store); on {
		out.Sentry.On, out.Sentry.Since = true, &since
	}
	mems, err := h.store.List()
	if err != nil {
		memoryError(w, err)
		return
	}
	texts := make([]string, len(mems))
	for i, m := range mems {
		texts[i] = m.Text
	}
	out.Rules = alerts.Rules(texts)
	if out.Rules == nil {
		out.Rules = []string{}
	}
	writeJSON(w, http.StatusOK, out)
}
