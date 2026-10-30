package agent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/francorobles/fritz/api/internal/actions"
)

func TestIsAffirmative(t *testing.T) {
	yes := []string{"yes", "Yes!", "yeah", "send it", "do it", "go ahead", "Fritz, yes", "ok"}
	for _, s := range yes {
		if !IsAffirmative(s) {
			t.Errorf("IsAffirmative(%q) = false, want true", s)
		}
	}
	no := []string{"no", "yes but make it Saturday", "yesterday", "I said yes to Sarah", "", "create it Friday"}
	for _, s := range no {
		if IsAffirmative(s) {
			t.Errorf("IsAffirmative(%q) = true, want false (strict whole-utterance)", s)
		}
	}
}

func newActionStore(t *testing.T) *actions.Store {
	t.Helper()
	s, err := actions.Open(filepath.Join(t.TempDir(), "fritz.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestConfirmerFlows(t *testing.T) {
	store := newActionStore(t)
	executed := 0
	confirmer := &Confirmer{
		Store: store,
		Exec: map[string]Executor{
			"calendar_create": func(_ context.Context, payload json.RawMessage) (string, error) {
				executed++
				return "Done — created it.", nil
			},
		},
	}
	ctx := context.Background()

	// Nothing pending: falls through to the model.
	if handled, _ := confirmer.HandleTurn(ctx, "yes"); handled {
		t.Error("no pending action must not handle the turn")
	}

	// Non-yes declines the pending action and falls through.
	if _, err := store.Create("calendar_create", "create 'Dentist'?", `{}`); err != nil {
		t.Fatal(err)
	}
	if handled, _ := confirmer.HandleTurn(ctx, "actually make it Saturday"); handled {
		t.Error("non-yes must fall through")
	}
	if _, ok, _ := store.LatestPending(); ok {
		t.Error("non-yes must decline the pending action")
	}
	if executed != 0 {
		t.Error("executor ran without a yes")
	}

	// A clear yes executes exactly once.
	if _, err := store.Create("calendar_create", "create 'Dentist'?", `{}`); err != nil {
		t.Fatal(err)
	}
	handled, reply := confirmer.HandleTurn(ctx, "yes")
	if !handled || reply != "Done — created it." || executed != 1 {
		t.Errorf("yes flow: handled=%v reply=%q executed=%d", handled, reply, executed)
	}
	// And afterwards there's nothing left to confirm.
	if handled, _ := confirmer.HandleTurn(ctx, "yes"); handled {
		t.Error("consumed action must not handle again")
	}

	// Executor failure is spoken, not silent.
	bad := &Confirmer{Store: store, Exec: map[string]Executor{
		"calendar_create": func(_ context.Context, _ json.RawMessage) (string, error) {
			return "", errors.New("kaput")
		},
	}}
	if _, err := store.Create("calendar_create", "create 'Dentist'?", `{}`); err != nil {
		t.Fatal(err)
	}
	handled, reply = bad.HandleTurn(ctx, "yes")
	if !handled || !strings.Contains(reply, "didn't work") {
		t.Errorf("executor failure: handled=%v reply=%q", handled, reply)
	}
}
