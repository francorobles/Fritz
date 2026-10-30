// Package httpapi defines the Fritz HTTP API routes.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/francorobles/fritz/api/internal/memory"
	"github.com/francorobles/fritz/api/internal/meta"
)

// Deps are the router's collaborators. A nil Store disables the
// memory routes (503); a nil Meta disables consolidation (503).
type Deps struct {
	Store *memory.Store
	Meta  *meta.Client
}

// NewRouter returns the API's HTTP handler.
func NewRouter(d Deps) http.Handler {
	h := &handlers{store: d.Store, meta: d.Meta}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Route("/memories", func(r chi.Router) {
		r.Use(middleware.Timeout(30 * time.Second))
		r.Get("/", h.listMemories)
		r.Post("/", h.createMemory)
		r.Patch("/{id}", h.updateMemory)
		r.Delete("/{id}", h.deleteMemory)
	})
	r.With(middleware.Timeout(30*time.Second)).Get("/turns", h.listTurns)
	r.With(middleware.Timeout(30*time.Second)).Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "fritz-api"})
	})
	r.With(middleware.Timeout(30*time.Second)).Get("/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"name": "Fritz API", "version": "0.1.0"})
	})
	// Consolidation calls Muse Wonder, which thinks slowly —
	// it gets minutes, not the dashboard's 30 s.
	r.With(middleware.Timeout(10*time.Minute)).Post("/memory/consolidate", h.consolidate)
	return r
}

type handlers struct {
	store *memory.Store
	meta  *meta.Client
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// storeOrError writes a 503 and returns false when memory is off.
func (h *handlers) storeOrError(w http.ResponseWriter) bool {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("memory store unavailable"))
		return false
	}
	return true
}

func parseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, errors.New("id must be a positive integer"))
		return 0, false
	}
	return id, true
}

// memoryError maps store errors to HTTP statuses.
func memoryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, memory.ErrNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, memory.ErrInvalid), errors.Is(err, memory.ErrBlocked):
		writeError(w, http.StatusBadRequest, err)
	default:
		writeError(w, http.StatusInternalServerError, err)
	}
}

func (h *handlers) listMemories(w http.ResponseWriter, _ *http.Request) {
	if !h.storeOrError(w) {
		return
	}
	mems, err := h.store.List()
	if err != nil {
		memoryError(w, err)
		return
	}
	if mems == nil {
		mems = []memory.Memory{}
	}
	writeJSON(w, http.StatusOK, mems)
}

type memoryRequest struct {
	Kind       *string  `json:"kind"`
	Text       *string  `json:"text"`
	Source     *string  `json:"source"`
	Confidence *float64 `json:"confidence"`
}

func decodeMemoryRequest(w http.ResponseWriter, r *http.Request) (memoryRequest, bool) {
	var req memoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return req, false
	}
	return req, true
}

func (h *handlers) createMemory(w http.ResponseWriter, r *http.Request) {
	if !h.storeOrError(w) {
		return
	}
	req, ok := decodeMemoryRequest(w, r)
	if !ok {
		return
	}
	kind, text, source, conf := "", "", "dashboard", 0.5
	if req.Kind != nil {
		kind = *req.Kind
	}
	if req.Text != nil {
		text = *req.Text
	}
	if req.Source != nil {
		source = *req.Source
	}
	if req.Confidence != nil {
		conf = *req.Confidence
	}
	m, err := h.store.Remember(kind, text, source, conf)
	if err != nil {
		memoryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (h *handlers) updateMemory(w http.ResponseWriter, r *http.Request) {
	if !h.storeOrError(w) {
		return
	}
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	req, ok := decodeMemoryRequest(w, r)
	if !ok {
		return
	}
	m, err := h.store.Get(id)
	if err != nil {
		memoryError(w, err)
		return
	}
	if req.Kind != nil {
		m.Kind = *req.Kind
	}
	if req.Text != nil {
		m.Text = *req.Text
	}
	if req.Confidence != nil {
		m.Confidence = *req.Confidence
	}
	m, err = h.store.Update(m)
	if err != nil {
		memoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (h *handlers) deleteMemory(w http.ResponseWriter, r *http.Request) {
	if !h.storeOrError(w) {
		return
	}
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := h.store.Forget(id); err != nil {
		memoryError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) listTurns(w http.ResponseWriter, r *http.Request) {
	if !h.storeOrError(w) {
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	turns, err := h.store.RecentTurns(limit)
	if err != nil {
		memoryError(w, err)
		return
	}
	if turns == nil {
		turns = []memory.Turn{}
	}
	writeJSON(w, http.StatusOK, turns)
}

type consolidateRequest struct {
	Days int `json:"days"`
}

type consolidateResponse struct {
	Turns    int            `json:"turns"`
	Memories int            `json:"memories_before"`
	Diff     memory.Diff    `json:"diff"`
	Applied  memory.Applied `json:"applied"`
}

// consolidate folds recent conversation into long-term memory via
// Muse Wonder — the demo-friendly trigger for the nightly job
// (cmd/consolidate does the same on a schedule).
func (h *handlers) consolidate(w http.ResponseWriter, r *http.Request) {
	if !h.storeOrError(w) {
		return
	}
	if h.meta == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("MODEL_API_KEY is unset — consolidation unavailable"))
		return
	}
	days := 1
	if r.ContentLength > 0 {
		var req consolidateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if req.Days > 0 {
			days = req.Days
		}
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	turns, err := h.store.TurnsSince(since, 1000)
	if err != nil {
		memoryError(w, err)
		return
	}
	current, err := h.store.List()
	if err != nil {
		memoryError(w, err)
		return
	}
	diff, _, err := memory.Consolidate(r.Context(), h.meta, turns, current)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	applied, err := h.store.Apply(diff, "consolidation:"+time.Now().UTC().Format("2006-01-02"))
	if err != nil {
		memoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, consolidateResponse{
		Turns:    len(turns),
		Memories: len(current),
		Diff:     diff,
		Applied:  applied,
	})
}
