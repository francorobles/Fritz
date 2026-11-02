package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/francorobles/fritz/api/internal/actions"
	"github.com/francorobles/fritz/api/internal/activity"
	"github.com/francorobles/fritz/api/internal/agent"
)

func actionsCall(t *testing.T, h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestActionsEndpoints(t *testing.T) {
	store, err := actions.Open(filepath.Join(t.TempDir(), "fritz.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	executed := 0
	exec := map[string]agent.Executor{
		"calendar_create": func(_ context.Context, _ json.RawMessage) (string, error) {
			executed++
			return "Done — created 'Dentist'.", nil
		},
	}
	const token = "test-app-token"
	h := NewRouter(Deps{Actions: store, ActionExec: exec, AppToken: token})

	// No token, no routes at all.
	open := NewRouter(Deps{Actions: store, ActionExec: exec})
	if rec := actionsCall(t, open, "GET", "/actions/pending", token); rec.Code != http.StatusNotFound {
		t.Errorf("no app token: status %d, want 404 (routes off)", rec.Code)
	}

	action, err := store.Create("calendar_create", "create 'Dentist' on Fri?", `{}`)
	if err != nil {
		t.Fatal(err)
	}

	// Auth is enforced.
	if rec := actionsCall(t, h, "GET", "/actions/pending", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no auth header: status %d, want 401", rec.Code)
	}
	if rec := actionsCall(t, h, "GET", "/actions/pending", "wrong"); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: status %d, want 401", rec.Code)
	}

	rec := actionsCall(t, h, "GET", "/actions/pending", token)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Dentist") {
		t.Errorf("pending list: %d %s", rec.Code, rec.Body)
	}

	// Approve executes exactly once; the repeat is a conflict.
	rec = actionsCall(t, h, "POST", "/actions/"+action.ID+"/approve", token)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"executed"`) || executed != 1 {
		t.Errorf("approve: %d %s (executed=%d)", rec.Code, rec.Body, executed)
	}
	if rec := actionsCall(t, h, "POST", "/actions/"+action.ID+"/approve", token); rec.Code != http.StatusConflict {
		t.Errorf("second approve: status %d, want 409", rec.Code)
	}
	if rec := actionsCall(t, h, "POST", "/actions/"+action.ID+"/decline", token); rec.Code != http.StatusConflict {
		t.Errorf("decline after approve: status %d, want 409", rec.Code)
	}
	if rec := actionsCall(t, h, "POST", "/actions/a_nope/approve", token); rec.Code != http.StatusNotFound {
		t.Errorf("unknown id: status %d, want 404", rec.Code)
	}

	// Decline path.
	second, _ := store.Create("calendar_create", "create 'Lunch'?", `{}`)
	if rec := actionsCall(t, h, "POST", "/actions/"+second.ID+"/decline", token); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"status":"declined"`) {
		t.Errorf("decline: %d %s", rec.Code, rec.Body)
	}
	if executed != 1 {
		t.Errorf("executed = %d, want still 1", executed)
	}
}

// TestActivityRecordsPhoneApproval: approving from the phone lands
// in /activity, which the dashboard reads.
func TestActivityRecordsPhoneApproval(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fritz.db")
	store, err := actions.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	log, err := activity.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })
	exec := map[string]agent.Executor{
		"email_reply": func(context.Context, json.RawMessage) (string, error) { return "Sent. I replied to Sarah.", nil },
	}
	h := NewRouter(Deps{Actions: store, ActionExec: exec, AppToken: "tok", Activity: log})

	if rec := actionsCall(t, NewRouter(Deps{}), "GET", "/activity", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no log: status %d, want 503", rec.Code)
	}
	a, err := store.Create("email_reply", `reply to Sarah: "Thursday works"?`, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if rec := actionsCall(t, h, "POST", "/actions/"+a.ID+"/approve", "tok"); rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body)
	}
	if rec := actionsCall(t, h, "GET", "/activity", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("activity without the app token: status %d, want 401", rec.Code)
	}
	rec := actionsCall(t, h, "GET", "/activity", "tok")
	var entries []activity.Entry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil || len(entries) != 1 {
		t.Fatalf("activity = %s (%v)", rec.Body, err)
	}
	if e := entries[0]; e.Source != "app" || e.Name != "email_reply" || !e.OK || !strings.Contains(e.Detail, "replied to Sarah") {
		t.Errorf("entry = %+v", e)
	}
}
