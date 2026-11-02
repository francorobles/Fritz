package agent

import (
	"context"
	"encoding/json"

	"github.com/francorobles/fritz/api/internal/alerts"
)

type sentryArgs struct {
	On bool `json:"on"`
}

// SentryTool is sentry_mode: arm it on the way out ("Hey Fritz,
// sentry mode") and anyone the camera sees gets you a WhatsApp
// photo; turn it off when you're back (by voice or by replying on
// WhatsApp). The switch lives in the shared database, so both
// processes see it. onChange, when set, runs after every change
// (the voice side uses it to tell the owner on WhatsApp when the
// mode is switched off at the desk — whoever is there may not be
// them).
func SentryTool(flags alerts.FlagStore, onChange func(ctx context.Context, on bool)) []Tool {
	return []Tool{{
		Name: "sentry_mode",
		Description: "Turn sentry mode on or off. While it's on, Fritz sends the owner a WhatsApp photo whenever someone " +
			"comes to the desk (he can't tell who it is). Use it when the user is leaving and asks you to keep watch, " +
			"or says they're back / to stop watching.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"on": map[string]any{"type": "boolean", "description": "true to start watching, false to stop."},
			},
			"required": []string{"on"},
		},
		Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args sentryArgs
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if err := alerts.SetSentry(flags, args.On); err != nil {
				return nil, err
			}
			if onChange != nil {
				onChange(ctx, args.On)
			}
			if args.On {
				return map[string]any{"sentry": "on",
					"note": "Watching. Anyone who comes to the desk gets the owner a WhatsApp photo. Tell them it's on; don't promise to recognize anyone."}, nil
			}
			return map[string]any{"sentry": "off", "note": "Stopped watching."}, nil
		},
		Confirm: func(out any) string {
			if m, _ := out.(map[string]any); m["sentry"] == "on" {
				return "Sentry mode is on. If anyone comes to your desk, I'll send you a photo on WhatsApp."
			}
			return "Okay, sentry mode is off."
		},
	}}
}
