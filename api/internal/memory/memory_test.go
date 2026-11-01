package memory

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/francorobles/fritz/api/internal/meta"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "fritz.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestRememberValidation(t *testing.T) {
	st := newStore(t)
	if _, err := st.Remember("hobby", "User likes chess", "test", 0.5); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad kind: err = %v, want ErrInvalid", err)
	}
	if _, err := st.Remember(KindRoutine, "  ", "test", 0.5); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty text: err = %v, want ErrInvalid", err)
	}
	// Safety guard: no health/personality judgments, ever.
	for _, text := range []string{
		"User seems depressed in the mornings",
		"User has anxiety about meetings",
		"User's personality is very intense",
	} {
		if _, err := st.Remember(KindRoutine, text, "test", 0.5); !errors.Is(err, ErrBlocked) {
			t.Errorf("%q: err = %v, want ErrBlocked", text, err)
		}
	}
	// Confidence is clamped into [0,1].
	m, err := st.Remember(KindRoutine, "User clears email around 9 AM", "test", 1.7)
	if err != nil {
		t.Fatal(err)
	}
	if m.Confidence != 1.0 {
		t.Errorf("confidence = %v, want clamped to 1", m.Confidence)
	}
	if err := st.AddTurn("system", "hi"); !errors.Is(err, ErrInvalid) {
		t.Errorf("AddTurn system role: err = %v, want ErrInvalid", err)
	}
}

func TestRememberDedupes(t *testing.T) {
	st := newStore(t)
	if _, err := st.Remember(KindPreference, "User prefers short replies", "test", 0.5); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Remember(KindPreference, "user PREFERS short replies", "test", 0.9); err != nil {
		t.Fatal(err)
	}
	mems, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(mems) != 1 || mems[0].Confidence != 0.9 {
		t.Errorf("memories = %+v, want one refreshed memory at 0.9", mems)
	}
}

func TestRecallAndSnapshot(t *testing.T) {
	st := newStore(t)
	_, _ = st.Remember(KindRoutine, "User clears email around 9 AM", "test", 0.8)
	_, _ = st.Remember(KindPreference, "User prefers short replies", "test", 0.9)
	_, _ = st.Remember(KindPerson, "Sarah reviews the budget", "test", 0.6)

	hits, err := st.Recall("email", 10)
	if err != nil || len(hits) != 1 || !strings.Contains(hits[0].Text, "email") {
		t.Errorf("Recall(email) = %+v, %v", hits, err)
	}
	snap, err := st.Snapshot(2)
	if err != nil || len(snap) != 2 || snap[0].Confidence != 0.9 {
		t.Errorf("Snapshot(2) = %+v, %v, want most confident first", snap, err)
	}
	text, err := st.SnapshotText(30)
	if err != nil || !strings.Contains(text, "[routine] User clears email") {
		t.Errorf("SnapshotText = %q, %v", text, err)
	}
	if text, _ := newStore(t).SnapshotText(30); text != "" {
		t.Errorf("empty store SnapshotText = %q, want empty", text)
	}
}

func TestUpdateAndForget(t *testing.T) {
	st := newStore(t)
	m, err := st.Remember(KindTask, "User wants a standup reminder", "test", 0.5)
	if err != nil {
		t.Fatal(err)
	}
	m.Text = "User wants a standup reminder at 9:30"
	m.Confidence = 0.9
	up, err := st.Update(m)
	if err != nil || up.Text != m.Text || up.Confidence != 0.9 {
		t.Errorf("Update = %+v, %v", up, err)
	}
	if err := st.Forget(m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get(m.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Forget: err = %v, want ErrNotFound", err)
	}
	if err := st.Forget(m.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Forget twice: err = %v, want ErrNotFound", err)
	}
}

func TestTurnLog(t *testing.T) {
	st := newStore(t)
	for i, c := range []string{"one", "two", "three", "four", "five"} {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if err := st.AddTurn(role, c); err != nil {
			t.Fatal(err)
		}
	}
	recent, err := st.RecentTurns(2)
	if err != nil || len(recent) != 2 || recent[0].Content != "four" || recent[1].Content != "five" {
		t.Errorf("RecentTurns(2) = %+v, %v, want last two in order", recent, err)
	}
	all, err := st.TurnsSince(time.Now().Add(-time.Hour), 100)
	if err != nil || len(all) != 5 || all[0].Content != "one" {
		t.Errorf("TurnsSince = %+v, %v", all, err)
	}
	n, err := st.DeleteTurnsBefore(time.Now().Add(time.Hour))
	if err != nil || n != 5 {
		t.Errorf("DeleteTurnsBefore = %d, %v, want 5", n, err)
	}
}

func TestApplyDiffSkipsBadEntries(t *testing.T) {
	st := newStore(t)
	keep, err := st.Remember(KindTask, "User wants a standup reminder", "test", 0.5)
	if err != nil {
		t.Fatal(err)
	}
	conf := 0.9
	diff := Diff{
		Add: []NewMemory{
			{Kind: KindRoutine, Text: "User reads news with coffee", Confidence: 0.7},
			{Kind: "hobby", Text: "User likes chess", Confidence: 0.5},
			{Kind: KindRoutine, Text: "User seems depressed lately", Confidence: 0.5},
		},
		Update: []Update{
			{ID: keep.ID, Text: "User wants a standup reminder at 9:30", Confidence: &conf},
			{ID: 9999, Text: "ghost"},
		},
		Remove: []int64{keep.ID}, // removed after update: gone wins
	}
	applied, err := st.Apply(diff, "consolidation:test")
	if err != nil {
		t.Fatal(err)
	}
	if applied.Added != 1 || applied.Updated != 1 || applied.Removed != 1 || len(applied.Skipped) != 3 {
		t.Errorf("Applied = %s (%v), want added 1 updated 1 removed 1 skipped 3", applied, applied.Skipped)
	}
	mems, err := st.List()
	if err != nil || len(mems) != 1 || !strings.Contains(mems[0].Text, "news with coffee") {
		t.Errorf("store after Apply = %+v, %v", mems, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func wonderClient(t *testing.T, diffJSON string, check func(body string)) *meta.Client {
	t.Helper()
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		if check != nil {
			check(string(body))
		}
		content := `{"choices":[{"message":{"role":"assistant","content":` +
			strconv.Quote(diffJSON) + `},"finish_reason":"stop"}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(content)),
		}, nil
	})
	return &meta.Client{APIKey: "k", HTTP: &http.Client{Transport: transport}}
}

func TestConsolidateAsksWonderForDiff(t *testing.T) {
	diffJSON := `{"add":[{"kind":"routine","text":"User clears email around 9 AM","confidence":0.8}],"update":[],"remove":[]}`
	client := wonderClient(t, diffJSON, func(body string) {
		if !strings.Contains(body, meta.ModelWonder) {
			t.Errorf("request does not use %s: %s", meta.ModelWonder, body)
		}
		if !strings.Contains(body, "json_schema") {
			t.Errorf("request has no structured-output schema: %s", body)
		}
	})
	diff, raw, err := Consolidate(context.Background(), client,
		[]Turn{{Role: "user", Content: "I always clear my email around 9"}},
		nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Add) != 1 || diff.Add[0].Kind != "routine" || raw == "" {
		t.Errorf("diff = %+v raw = %q", diff, raw)
	}
}

func TestConsolidateNoTurnsSkipsModel(t *testing.T) {
	called := false
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("must not be called")
	})
	client := &meta.Client{APIKey: "k", HTTP: &http.Client{Transport: transport}}
	diff, _, err := Consolidate(context.Background(), client, nil, nil)
	if err != nil || called {
		t.Errorf("no-turn consolidation: diff=%+v err=%v called=%v", diff, err, called)
	}
}

func TestSightingsUpsert(t *testing.T) {
	st := newStore(t)
	seen := time.Date(2026, 11, 2, 15, 40, 0, 0, time.UTC)
	if err := st.SaveSighting(Sighting{Name: "keys", Place: "on your right", SeenAt: seen}); err != nil {
		t.Fatal(err)
	}
	gone := seen.Add(12 * time.Minute)
	if err := st.SaveSighting(Sighting{Name: "keys", Place: "on your right", SeenAt: seen, GoneAt: gone}); err != nil {
		t.Fatal(err)
	}
	got, err := st.Sightings()
	if err != nil || len(got) != 1 {
		t.Fatalf("Sightings = %+v, %v; want one row per object", got, err)
	}
	if !got[0].SeenAt.Equal(seen) || !got[0].GoneAt.Equal(gone) || got[0].Place != "on your right" {
		t.Errorf("sighting = %+v", got[0])
	}
}

func TestStateAndAlerted(t *testing.T) {
	st := newStore(t)
	if _, _, ok, err := st.State("sentry"); ok || err != nil {
		t.Fatalf("unset key: ok=%v err=%v", ok, err)
	}
	if err := st.SetState("sentry", "on"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetState("sentry", "off"); err != nil {
		t.Fatal(err)
	}
	v, at, ok, err := st.State("sentry")
	if v != "off" || !ok || err != nil || time.Since(at) > time.Minute {
		t.Fatalf("State = %q %v %v %v", v, at, ok, err)
	}
	if seen, _ := st.Alerted("mail:gmail:1"); seen {
		t.Fatal("unmarked key reported as alerted")
	}
	if added, err := st.MarkAlerted("mail:gmail:1", "mail", "Starlink"); !added || err != nil {
		t.Fatalf("first mark: %v %v", added, err)
	}
	if added, _ := st.MarkAlerted("mail:gmail:1", "mail", "Starlink"); added {
		t.Fatal("second mark of the same key must not count as new")
	}
	if seen, _ := st.Alerted("mail:gmail:1"); !seen {
		t.Fatal("marked key not reported")
	}
}
