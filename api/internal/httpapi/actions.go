package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/francorobles/fritz/api/internal/actions"
	"github.com/francorobles/fritz/api/internal/agent"
)

// appAuth guards the phone-facing routes (fritz-mobile-backend
// skill): Authorization: Bearer <FRITZ_APP_TOKEN>, compared in
// constant time. The routes are only mounted when a token is
// configured at all.
func appAuth(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := r.Header.Get("Authorization")
			if subtle.ConstantTimeCompare([]byte(got), []byte("Bearer "+token)) != 1 {
				writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type actionsHandlers struct {
	store *actions.Store
	exec  map[string]agent.Executor
}

type pendingView struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Summary   string `json:"summary"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
}

func (h *actionsHandlers) listPending(w http.ResponseWriter, _ *http.Request) {
	pending, err := h.store.PendingList(50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]pendingView, 0, len(pending))
	for _, a := range pending {
		out = append(out, pendingView{
			ID:        a.ID,
			Kind:      a.Kind,
			Summary:   a.Summary,
			CreatedAt: a.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			ExpiresAt: a.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func actionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, actions.ErrNotFound):
		writeError(w, http.StatusNotFound, errors.New("unknown action"))
	case errors.Is(err, actions.ErrNotPending):
		writeError(w, http.StatusConflict, errors.New("action no longer pending"))
	default:
		writeError(w, http.StatusInternalServerError, err)
	}
}

// approve claims the action atomically (exactly one execution,
// even if voice and phone approve together), runs it, and
// reports the spoken-style result.
func (h *actionsHandlers) approve(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	action, err := h.store.Approve(id)
	if err != nil {
		actionError(w, err)
		return
	}
	exec := h.exec[action.Kind]
	if exec == nil {
		_ = h.store.Finish(id, "no executor for "+action.Kind, true)
		writeError(w, http.StatusInternalServerError, errors.New("no executor for "+action.Kind))
		return
	}
	result, err := exec(r.Context(), json.RawMessage(action.Payload))
	if err != nil {
		_ = h.store.Finish(id, err.Error(), true)
		writeError(w, http.StatusBadGateway, err)
		return
	}
	_ = h.store.Finish(id, result, false)
	writeJSON(w, http.StatusOK, map[string]string{
		"id": id, "status": "executed", "result": result,
	})
}

func (h *actionsHandlers) decline(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.store.Decline(id); err != nil {
		actionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "declined"})
}
