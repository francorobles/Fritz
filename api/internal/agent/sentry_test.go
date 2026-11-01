package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/francorobles/fritz/api/internal/alerts"
	"github.com/francorobles/fritz/api/internal/memory"
)

func TestSentryTool(t *testing.T) {
	st, err := memory.Open(filepath.Join(t.TempDir(), "fritz.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var changes []bool
	tool := SentryTool(st, func(_ context.Context, on bool) { changes = append(changes, on) })[0]
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"on":true}`)); err != nil {
		t.Fatal(err)
	}
	if on, _ := alerts.SentryArmed(st); !on {
		t.Fatal("sentry not armed")
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"on":false}`)); err != nil {
		t.Fatal(err)
	}
	if on, _ := alerts.SentryArmed(st); on || len(changes) != 2 || changes[1] {
		t.Fatalf("armed=%v changes=%v", on, changes)
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"on":true,"minutes":5}`)); err == nil {
		t.Error("unknown argument accepted")
	}
}
