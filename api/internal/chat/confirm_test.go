package chat

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/francorobles/fritz/api/internal/actions"
	"github.com/francorobles/fritz/api/internal/agent"
	"github.com/francorobles/fritz/api/internal/memory"
	"github.com/francorobles/fritz/api/internal/meta"
)

// A pending action filed at the desk can be confirmed by a
// WhatsApp "yes": same store, same Go-checked rule.
func TestBrainConfirmsPendingAction(t *testing.T) {
	memStore, err := memory.Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer memStore.Close()
	actStore, err := actions.Open(filepath.Join(t.TempDir(), "actions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer actStore.Close()
	if _, err := actStore.Create("calendar_create", "create 'Dentist'?", `{}`); err != nil {
		t.Fatal(err)
	}
	executed := 0
	brain := &Brain{
		Client: &meta.Client{APIKey: "k"},
		Store:  memStore,
		Confirmer: &agent.Confirmer{
			Store: actStore,
			Exec: map[string]agent.Executor{
				"calendar_create": func(_ context.Context, _ json.RawMessage) (string, error) {
					executed++
					return "Done — created 'Dentist'.", nil
				},
			},
		},
	}
	reply, err := brain.Reply(context.Background(), "yes")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "Done — created 'Dentist'." || executed != 1 {
		t.Errorf("reply = %q, executed = %d — the WhatsApp yes must run the action", reply, executed)
	}
	turns, err := memStore.RecentTurns(10)
	if err != nil || len(turns) != 2 {
		t.Errorf("turns = %+v, %v — confirmed exchanges persist like any reply", turns, err)
	}
}
