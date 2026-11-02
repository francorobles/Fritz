package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/francorobles/fritz/api/internal/alerts"
	"github.com/francorobles/fritz/api/internal/memory"
)

func TestFocusTools(t *testing.T) {
	st, err := memory.Open(filepath.Join(t.TempDir(), "fritz.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	focus := &alerts.Focus{State: st}
	var rules []string
	byName := map[string]Tool{}
	for _, tool := range FocusTools(focus, func(rule string) error { rules = append(rules, rule); return nil }) {
		byName[tool.Name] = tool
	}
	run := func(name, args string) map[string]any {
		t.Helper()
		out, err := byName[name].Run(context.Background(), json.RawMessage(args))
		if err != nil {
			t.Fatalf("%s(%s): %v", name, args, err)
		}
		return out.(map[string]any)
	}

	if out := run("focus_mode", `{"action":"start","minutes":25}`); out["focus"] != "on" || out["minutes"] != 25 {
		t.Errorf("start = %v", out)
	}
	if _, on := focus.Active(); !on {
		t.Fatal("focus not on")
	}
	_ = focus.Hold(`Email from Dana: "Q4 update"`)
	if out := run("focus_mode", `{"action":"status"}`); out["focus"] != "on" || len(out["held"].([]string)) != 1 {
		t.Errorf("status = %v", out)
	}
	if out := run("focus_mode", `{"action":"stop"}`); out["focus"] != "off" || len(out["held"].([]string)) != 1 {
		t.Errorf("stop = %v", out)
	}
	if len(focus.Held()) != 0 {
		t.Error("stop did not hand over the held messages")
	}
	if _, err := byName["focus_mode"].Run(context.Background(), json.RawMessage(`{"action":"pause"}`)); err == nil {
		t.Error("unknown action accepted")
	}

	run("interruption_rule", `{"rule":"hold project updates unless there's a deadline"}`)
	if len(rules) != 1 {
		t.Errorf("rules = %v", rules)
	}

	if out := run("where_was_i", `{}`); out["saved"] != false {
		t.Errorf("where_was_i before saving = %v", out)
	}
	run("save_progress", `{"task":"stopped at the pricing section","next":"the discount table"}`)
	if out := run("where_was_i", `{}`); out["task"] != "stopped at the pricing section" || out["next"] != "the discount table" {
		t.Errorf("where_was_i = %v", out)
	}
}

func TestFocusConfirmations(t *testing.T) {
	st, err := memory.Open(filepath.Join(t.TempDir(), "fritz.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tools := map[string]Tool{}
	for _, tool := range FocusTools(&alerts.Focus{State: st}, func(string) error { return nil }) {
		tools[tool.Name] = tool
	}
	out, _ := tools["focus_mode"].Run(context.Background(), json.RawMessage(`{"action":"start","minutes":30}`))
	if c := tools["focus_mode"].Confirm(out); !strings.HasPrefix(c, "I'll keep things quiet until ") || strings.Contains(c, "PM") || strings.Contains(c, "AM") {
		t.Errorf("start confirmation = %q", c)
	}
	out, _ = tools["focus_mode"].Run(context.Background(), json.RawMessage(`{"action":"status"}`))
	if c := tools["focus_mode"].Confirm(out); c != "" {
		t.Errorf("status must be phrased by the model, got %q", c)
	}
	for _, name := range []string{"interruption_rule", "save_progress"} {
		if tools[name].Confirm == nil {
			t.Errorf("%s has no confirmation", name)
		}
	}
	if tools["where_was_i"].Confirm != nil {
		t.Error("where_was_i must be phrased by the model")
	}
}
