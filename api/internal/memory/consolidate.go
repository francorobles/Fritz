package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/francorobles/fritz/api/internal/meta"
)

// Diff is Muse Wonder's proposed memory update: durable facts to
// add, memories to reword or re-score, and ids to drop (stale or
// contradicted). The model returns exactly this shape via
// structured output (see diffSchema).
type Diff struct {
	Add    []NewMemory `json:"add"`
	Update []Update    `json:"update"`
	Remove []int64     `json:"remove"`
}

// NewMemory is one fact to store.
type NewMemory struct {
	Kind       string  `json:"kind"`
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
}

// Update rewords and/or re-scores one existing memory by id. Nil
// pointer fields mean "leave unchanged".
type Update struct {
	ID         int64    `json:"id"`
	Text       string   `json:"text,omitempty"`
	Confidence *float64 `json:"confidence,omitempty"`
}

// Applied summarizes what Store.Apply did with a Diff. Skipped
// entries name why (unknown id, invalid kind, blocked text) — a
// model diff is applied defensively, never trusted blindly.
type Applied struct {
	Added   int      `json:"added"`
	Updated int      `json:"updated"`
	Removed int      `json:"removed"`
	Skipped []string `json:"skipped,omitempty"`
}

func (a Applied) String() string {
	return fmt.Sprintf("added %d, updated %d, removed %d, skipped %d",
		a.Added, a.Updated, a.Removed, len(a.Skipped))
}

// diffSchema constrains Wonder's reply (JSON Schema, flat — the
// API rejects recursive schemas).
var diffSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"add": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"kind":       map[string]any{"type": "string"},
					"text":       map[string]any{"type": "string"},
					"confidence": map[string]any{"type": "number"},
				},
				"required": []string{"kind", "text", "confidence"},
			},
		},
		"update": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":         map[string]any{"type": "integer"},
					"text":       map[string]any{"type": "string"},
					"confidence": map[string]any{"type": "number"},
				},
				"required": []string{"id"},
			},
		},
		"remove": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "integer"},
		},
	},
	"required": []string{"add", "update", "remove"},
}

const consolidateSystem = `
You maintain Fritz's long-term memory of one user. Fritz is a desk
companion that "studies" the user: it may notice routines,
preferences, people who matter, and open tasks. It must NEVER store
personality, health, or mood judgments about the user — no
categories like that at all.

You are given the current memories (each with an id) and one day of
conversation. Reply with a JSON diff:
- add: durable facts learned today that are not already remembered.
  kind is one of routine, preference, person, task. Write each fact
  as one short sentence from the user's perspective ("User clears
  email around 9 AM"). confidence: 0.5 if seen once, 0.8 if clearly
  established, 0.95 if the user stated it directly.
- update: existing memories (by id) that today reworded, confirmed,
  or weakened. Higher confidence when confirmed; lower when shaky.
- remove: ids that are stale (a routine that plainly stopped) or
  contradicted by today's conversation.
Keep every category free of personality, health, or mood judgments.
Prefer few high-quality changes over many speculative ones; when in
doubt, return an empty diff.`

// Consolidate asks Muse Wonder (structured output) to fold turns
// into the memory store, returning the diff and the model's raw
// reply (for logging). An empty turns list is a no-op — the model
// is not called, which also keeps the nightly job cheap.
func Consolidate(ctx context.Context, client *meta.Client, turns []Turn, current []Memory) (Diff, string, error) {
	if len(turns) == 0 {
		return Diff{}, "", nil
	}
	var b strings.Builder
	b.WriteString("Current memories:\n")
	if len(current) == 0 {
		b.WriteString("(none yet)\n")
	}
	for _, m := range current {
		fmt.Fprintf(&b, "- [%d] [%s] (confidence %.2f) %s\n", m.ID, m.Kind, m.Confidence, m.Text)
	}
	b.WriteString("\nToday's conversation:\n")
	for _, t := range turns {
		fmt.Fprintf(&b, "%s %s: %s\n", t.CreatedAt.Format("15:04"), t.Role, t.Content)
	}

	raw, err := client.ChatJSON(ctx, meta.ModelWonder, []meta.Message{
		{Role: "system", Content: strings.TrimSpace(consolidateSystem)},
		{Role: "user", Content: b.String()},
	}, "memory_diff", diffSchema)
	if err != nil {
		return Diff{}, "", err
	}
	var diff Diff
	if err := json.Unmarshal([]byte(raw), &diff); err != nil {
		snippet := raw
		if len(snippet) > 300 {
			snippet = snippet[:300] + "..."
		}
		return Diff{}, raw, fmt.Errorf("memory: decode Wonder diff: %w (reply: %s)", err, snippet)
	}
	return diff, raw, nil
}

// Apply defensively applies a Diff: invalid or blocked entries are
// skipped (and reported), unknown ids are skipped, database errors
// are fatal. source is stamped on memories the diff adds.
func (s *Store) Apply(diff Diff, source string) (Applied, error) {
	var a Applied
	skip := func(format string, args ...any) {
		a.Skipped = append(a.Skipped, fmt.Sprintf(format, args...))
	}
	for _, add := range diff.Add {
		if _, err := s.Remember(add.Kind, add.Text, source, add.Confidence); err != nil {
			if errors.Is(err, ErrInvalid) || errors.Is(err, ErrBlocked) {
				skip("add %q: %v", add.Text, err)
				continue
			}
			return a, err
		}
		a.Added++
	}
	for _, up := range diff.Update {
		m, err := s.Get(up.ID)
		if errors.Is(err, ErrNotFound) {
			skip("update %d: not found", up.ID)
			continue
		}
		if err != nil {
			return a, err
		}
		if up.Text != "" {
			m.Text = up.Text
		}
		if up.Confidence != nil {
			m.Confidence = *up.Confidence
		}
		if _, err := s.Update(m); err != nil {
			if errors.Is(err, ErrInvalid) || errors.Is(err, ErrBlocked) {
				skip("update %d: %v", up.ID, err)
				continue
			}
			return a, err
		}
		a.Updated++
	}
	for _, id := range diff.Remove {
		if err := s.Forget(id); err != nil {
			if errors.Is(err, ErrNotFound) {
				skip("remove %d: not found", id)
				continue
			}
			return a, err
		}
		a.Removed++
	}
	return a, nil
}
