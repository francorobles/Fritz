package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/francorobles/fritz/api/internal/vision"
)

// ObjectFinder is the part of vision.Finder the tool needs.
type ObjectFinder interface {
	Find(ctx context.Context, name string) (vision.Sighting, bool, error)
}

// Looker turns the eyes toward a point for a while
// (vision.Tracker.Look).
type Looker interface {
	Look(x, y float64, d time.Duration)
}

// lookFor is how long the eyes stay on a found object: long enough
// to be seen on camera in the demo, short enough to go back to the
// user while Fritz is still talking.
const lookFor = 4 * time.Second

// FindObjectTool is find_object: SAM 3.1 looks for the object in
// the live camera frame; if it's there the eyes turn to it, and if
// not Fritz answers from where it last saw it. eyes may be nil
// (no face): the answer is still spoken.
func FindObjectTool(finder ObjectFinder, eyes Looker, cfg vision.Config) []Tool {
	if isNil(finder) {
		return nil
	}
	return []Tool{{
		Name: "find_object",
		Description: "Find a physical object on or around the user's desk with the camera (\"where are my keys?\"). " +
			"If it's in view your eyes turn to it and you get where it is from the user's seat; if not, you get " +
			"when and where you last saw it, and when it disappeared. Use a short, simple noun: keys, wallet, glasses, phone, mug.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string", "description": "The object, a short noun."},
			},
			"required": []string{"name"},
		},
		Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Name string `json:"name"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if strings.TrimSpace(args.Name) == "" {
				return nil, fmt.Errorf("name is required")
			}
			s, found, err := finder.Find(ctx, args.Name)
			if err != nil && !errors.Is(err, vision.ErrNoFrame) {
				return nil, err
			}
			now := time.Now()
			out := map[string]any{"object": s.Name, "in_view": found}
			if errors.Is(err, vision.ErrNoFrame) {
				out["camera"] = "not available right now"
			}
			switch {
			case found:
				out["where"] = s.Where
				if !isNil(eyes) {
					x, y := vision.LookAt(s.Box, cfg)
					eyes.Look(x, y, lookFor)
					out["note"] = "Your eyes are looking at it now."
				}
			case !s.SeenAt.IsZero():
				out["last_seen"] = when(s.SeenAt, now)
				out["last_place"] = s.Where
				if !s.GoneAt.IsZero() {
					out["gone_since"] = when(s.GoneAt, now)
				}
			default:
				out["note"] = "Not in view, and you haven't seen it before."
			}
			return out, nil
		},
	}}
}

// when says a past time the way it should be spoken: "3:40 PM, 25
// minutes ago", or a weekday for older ones.
func when(t, now time.Time) string {
	d := now.Sub(t)
	local := t.Local()
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%s, %d minutes ago", local.Format("3:04 PM"), int(d.Minutes()))
	case d < 24*time.Hour && local.Day() == now.Local().Day():
		return "today at " + local.Format("3:04 PM")
	case d < 48*time.Hour:
		return "yesterday at " + local.Format("3:04 PM")
	}
	return local.Format("Monday at 3:04 PM")
}
