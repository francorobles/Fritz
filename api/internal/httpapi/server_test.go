package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/francorobles/fritz/api/internal/memory"
	"github.com/francorobles/fritz/api/internal/meta"
)

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	NewRouter(Deps{}).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz: got status %d, want %d", rec.Code, http.StatusOK)
	}
}

func newStore(t *testing.T) *memory.Store {
	t.Helper()
	st, err := memory.Open(filepath.Join(t.TempDir(), "fritz.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func call(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return v
}

func TestMemoriesCRUD(t *testing.T) {
	h := NewRouter(Deps{Store: newStore(t)})

	rec := call(t, h, "POST", "/memories", `{"kind":"routine","text":"User clears email around 9 AM","confidence":0.8}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST: status %d (%s)", rec.Code, rec.Body)
	}
	created := decode[memory.Memory](t, rec)
	if created.ID <= 0 || created.Text == "" {
		t.Fatalf("created = %+v", created)
	}

	rec = call(t, h, "GET", "/memories", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "clears email") {
		t.Errorf("GET list: %d %s", rec.Code, rec.Body)
	}

	path := fmt.Sprintf("/memories/%d", created.ID)
	rec = call(t, h, "PATCH", path, `{"text":"User clears email around 9:30 AM","confidence":0.9}`)
	updated := decode[memory.Memory](t, rec)
	if rec.Code != http.StatusOK || updated.Confidence != 0.9 || !strings.Contains(updated.Text, "9:30") {
		t.Errorf("PATCH: %d %+v", rec.Code, updated)
	}

	if rec := call(t, h, "PATCH", "/memories/9999", `{"text":"ghost"}`); rec.Code != http.StatusNotFound {
		t.Errorf("PATCH missing: status %d, want 404", rec.Code)
	}
	if rec := call(t, h, "POST", "/memories", `{"kind":"hobby","text":"x"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("POST bad kind: status %d, want 400 (%s)", rec.Code, rec.Body)
	}
	if rec := call(t, h, "POST", "/memories", `{"kind":"routine","text":"User seems depressed"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("POST blocked text: status %d, want 400 (%s)", rec.Code, rec.Body)
	}

	if rec := call(t, h, "DELETE", path, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: status %d, want 204", rec.Code)
	}
	rec = call(t, h, "GET", "/memories", "")
	if mems := decode[[]memory.Memory](t, rec); len(mems) != 0 {
		t.Errorf("list after delete = %+v, want empty", mems)
	}
}

func TestMemoryRoutesUnavailable(t *testing.T) {
	h := NewRouter(Deps{})
	if rec := call(t, h, "GET", "/memories", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /memories without store: status %d, want 503", rec.Code)
	}
	if rec := call(t, h, "POST", "/memory/consolidate", `{}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("consolidate without store: status %d, want 503", rec.Code)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestConsolidateEndpoint(t *testing.T) {
	st := newStore(t)
	if err := st.AddTurn("user", "I always clear my email around 9"); err != nil {
		t.Fatal(err)
	}
	diffJSON := `{"add":[{"kind":"routine","text":"User clears email around 9 AM","confidence":0.8}],"update":[],"remove":[]}`
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		content := `{"choices":[{"message":{"role":"assistant","content":` +
			strconv.Quote(diffJSON) + `},"finish_reason":"stop"}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(content)),
		}, nil
	})
	client := &meta.Client{APIKey: "k", HTTP: &http.Client{Transport: transport}}
	h := NewRouter(Deps{Store: st, Meta: client})

	rec := call(t, h, "POST", "/memory/consolidate", `{"days":1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("consolidate: status %d (%s)", rec.Code, rec.Body)
	}
	resp := decode[consolidateResponse](t, rec)
	if resp.Applied.Added != 1 || resp.Turns != 1 {
		t.Errorf("response = %+v", resp)
	}
	rec = call(t, h, "GET", "/memories", "")
	if mems := decode[[]memory.Memory](t, rec); len(mems) != 1 {
		t.Errorf("memories after consolidate = %+v", mems)
	}
}
