package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/francorobles/fritz/api/internal/alerts"
)

type focusArgs struct {
	Action  string `json:"action"`
	Minutes int    `json:"minutes,omitempty"`
}

// FocusTools are the heads-down tools: focus_mode ("let me focus
// for 30 minutes" — mail that can wait is held for a digest),
// interruption_rule ("hold project updates unless there's a
// deadline" — applies from the next email), and save_progress /
// where_was_i ("I'm stopping at the pricing section"). remember
// stores a rule as a memory (memory.Store.Remember), so it shows
// on the dashboard and can be deleted there.
func FocusTools(focus *alerts.Focus, remember func(rule string) error) []Tool {
	return []Tool{
		{
			Name: "focus_mode",
			Description: "Start, stop, or check focus mode. While it's on, Fritz holds email that can wait and only interrupts for " +
				"something urgent; when it ends he gives one short digest of what was held. Use start when the user wants to " +
				"focus or not be disturbed (default 30 minutes); stop when they're done early; status for \"anything I missed?\". " +
				"stop and status return the held messages — tell the user about them briefly.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action":  map[string]any{"type": "string", "enum": []string{"start", "stop", "status"}},
					"minutes": map[string]any{"type": "integer", "description": "How long to focus (start only). Default 30."},
				},
				"required": []string{"action"},
			},
			Run: func(_ context.Context, raw json.RawMessage) (any, error) {
				var args focusArgs
				if err := decodeArgs(raw, &args); err != nil {
					return nil, err
				}
				switch args.Action {
				case "start":
					minutes := args.Minutes
					if minutes <= 0 {
						minutes = 30
					}
					minutes = min(minutes, 240)
					until, err := focus.Start(time.Duration(minutes) * time.Minute)
					if err != nil {
						return nil, err
					}
					return map[string]any{"focus": "on", "until": until.Local().Format("3:04 PM"), "minutes": minutes,
						"note": "Confirm in one short sentence and then stay quiet."}, nil
				case "stop":
					if err := focus.Stop(); err != nil {
						return nil, err
					}
					return map[string]any{"focus": "off", "held": heldList(focus.TakeHeld())}, nil
				case "status":
					out := map[string]any{"focus": "off", "held": heldList(focus.Held())}
					if until, on := focus.Active(); on {
						out["focus"], out["until"] = "on", until.Local().Format("3:04 PM")
					}
					return out, nil
				}
				return nil, fmt.Errorf("action must be start, stop or status")
			},
		},
		{
			Name: "interruption_rule",
			Description: "Remember a rule about what may interrupt the user, in their words, e.g. \"hold project updates unless " +
				"there's a deadline\" or \"always tell me about anything from my manager\". It applies from the next email on. " +
				"Use it whenever the user says what should or shouldn't interrupt them.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"rule": map[string]any{"type": "string", "description": "The rule as one short sentence."},
				},
				"required": []string{"rule"},
			},
			Run: func(_ context.Context, raw json.RawMessage) (any, error) {
				var args struct {
					Rule string `json:"rule"`
				}
				if err := decodeArgs(raw, &args); err != nil {
					return nil, err
				}
				rule := strings.TrimSpace(args.Rule)
				if rule == "" {
					return nil, fmt.Errorf("rule is empty")
				}
				if err := remember(rule); err != nil {
					return nil, err
				}
				return map[string]any{"saved": rule, "note": "It applies from the next email. Confirm briefly in your own words."}, nil
			},
		},
		{
			Name: "save_progress",
			Description: "Remember where the user stopped working, when they say something like \"I'm stopping at the pricing " +
				"section\" or \"remember where I am\". When they come back, Fritz reminds them.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task": map[string]any{"type": "string", "description": "What they were working on and where they stopped."},
					"next": map[string]any{"type": "string", "description": "Their next step, if they said one."},
				},
				"required": []string{"task"},
			},
			Run: func(_ context.Context, raw json.RawMessage) (any, error) {
				var args struct {
					Task string `json:"task"`
					Next string `json:"next,omitempty"`
				}
				if err := decodeArgs(raw, &args); err != nil {
					return nil, err
				}
				if strings.TrimSpace(args.Task) == "" {
					return nil, fmt.Errorf("task is empty")
				}
				p := alerts.Progress{Task: strings.TrimSpace(args.Task), Next: strings.TrimSpace(args.Next), At: time.Now()}
				if err := alerts.SaveProgress(focus.State, p); err != nil {
					return nil, err
				}
				return map[string]any{"saved": true, "note": "Confirm in a few words."}, nil
			},
		},
		{
			Name:        "where_was_i",
			Description: "Tell the user where they stopped working (saved with save_progress). Use for \"where was I?\".",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			Run: func(_ context.Context, raw json.RawMessage) (any, error) {
				p, ok := alerts.LoadProgress(focus.State)
				if !ok {
					return map[string]any{"saved": false, "note": "Nothing saved. Say you don't know yet."}, nil
				}
				out := map[string]any{"task": p.Task, "saved_at": p.At.Local().Format("Mon 3:04 PM")}
				if p.Next != "" {
					out["next"] = p.Next
				}
				return out, nil
			},
		},
	}
}

func heldList(held []alerts.Held) []string {
	out := make([]string, len(held))
	for i, h := range held {
		out[i] = fmt.Sprintf("%s (%s)", h.Text, h.At.Local().Format("3:04 PM"))
	}
	return out
}
