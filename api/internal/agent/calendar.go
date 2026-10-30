package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/francorobles/fritz/api/internal/actions"
	"github.com/francorobles/fritz/api/internal/integrations"
)

// isNil reports whether v is nil, including a typed nil
// pointer wrapped in an interface — the classic Go trap where
// a nil *integrations.Calendar passes `cal == nil` as an
// interface and the tool gets offered with no client behind it
// (it panicked on first use before this check existed).
func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		return rv.IsNil()
	}
	return false
}

// CalendarLister is the slice of the calendar client the tool
// needs (a fake in tests).
type CalendarLister interface {
	ListEvents(ctx context.Context, from, to time.Time, max int) ([]integrations.Event, error)
}

// CalendarTools are the read-only calendar tools. Creating
// events stays out of the agent until the confirmation rule
// has a home here (fritz-agent skill).
func CalendarTools(cal CalendarLister) []Tool {
	if isNil(cal) {
		return nil
	}
	return []Tool{
		{
			Name: "calendar_list",
			Description: "List the user's Google Calendar events between two times. " +
				"Use for 'do I have anything tomorrow morning', 'what's on Friday'. " +
				"Times are RFC 3339 with offset (e.g. 2030-10-31T08:00:00+08:00). " +
				"Defaults when omitted: from now, to 7 days later. Answer with day + local times.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"from": map[string]any{"type": "string", "description": "Window start, RFC 3339. Default: now."},
					"to":   map[string]any{"type": "string", "description": "Window end, RFC 3339. Default: 7 days after from."},
					"max":  map[string]any{"type": "integer", "description": "How many events, 1-20 (default 10)."},
				},
			},
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var args struct {
					From string `json:"from"`
					To   string `json:"to"`
					Max  int    `json:"max"`
				}
				if err := decodeArgs(raw, &args); err != nil {
					return nil, err
				}
				return listEvents(ctx, cal, args.From, args.To, args.Max)
			},
		},
	}
}

type calEvent struct {
	Summary  string `json:"summary"`
	When     string `json:"when"` // "Fri 31 Oct 8:00 AM – 12:00 PM" in local time
	AllDay   bool   `json:"all_day,omitempty"`
	Location string `json:"location,omitempty"`
}

func listEvents(ctx context.Context, cal CalendarLister, fromArg, toArg string, max int) (any, error) {
	now := time.Now()
	from := now
	to := now.AddDate(0, 0, 7)
	var err error
	if strings.TrimSpace(fromArg) != "" {
		if from, err = time.Parse(time.RFC3339, fromArg); err != nil {
			return nil, fmt.Errorf("from must be RFC 3339 (e.g. 2030-10-31T08:00:00+08:00): %w", err)
		}
	}
	if strings.TrimSpace(toArg) != "" {
		if to, err = time.Parse(time.RFC3339, toArg); err != nil {
			return nil, fmt.Errorf("to must be RFC 3339: %w", err)
		}
	}
	if !to.After(from) {
		return nil, fmt.Errorf("to must be after from")
	}
	if to.Sub(from) > 62*24*time.Hour {
		return nil, fmt.Errorf("window too large (max 62 days) — narrow it down")
	}
	if max <= 0 || max > 20 {
		max = 10
	}
	events, err := cal.ListEvents(ctx, from, to, max)
	if err != nil {
		return nil, err
	}
	out := struct {
		From   string     `json:"from"`
		To     string     `json:"to"`
		Now    string     `json:"now"`
		Events []calEvent `json:"events"`
	}{
		From:   from.Format("Mon 2 Jan 3:04 PM"),
		To:     to.Format("Mon 2 Jan 3:04 PM"),
		Now:    now.Format("Mon 2 Jan 3:04 PM"),
		Events: []calEvent{},
	}
	for _, ev := range events {
		out.Events = append(out.Events, calEvent{
			Summary:  ev.Summary,
			When:     whenString(ev),
			AllDay:   ev.AllDay,
			Location: ev.Location,
		})
	}
	return out, nil
}

// CalendarCreator creates events (integrations.Calendar
// satisfies it).
type CalendarCreator interface {
	CreateEvent(ctx context.Context, summary string, start, end time.Time, location string) (integrations.Event, error)
}

type createEventArgs struct {
	Title    string `json:"title"`
	Start    string `json:"start"` // RFC 3339
	End      string `json:"end"`   // RFC 3339, optional: start + 1h
	Location string `json:"location"`
}

func (a createEventArgs) times() (time.Time, time.Time, error) {
	start, err := time.Parse(time.RFC3339, a.Start)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("start must be RFC 3339 (e.g. 2030-10-31T14:00:00+08:00): %w", err)
	}
	end := start.Add(time.Hour)
	if strings.TrimSpace(a.End) != "" {
		if end, err = time.Parse(time.RFC3339, a.End); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("end must be RFC 3339: %w", err)
		}
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("end must be after start")
	}
	return start, end, nil
}

// CalendarCreateTool is calendar_create, the confirmation-gated
// action tool: calling it only files a pending action — nothing
// is created until the user confirms with a spoken/chat yes
// (checked by Go) or Approve in the app. The result tells the
// model to read the summary aloud and ask; it must never claim
// the event exists yet.
func CalendarCreateTool(cal CalendarCreator, store *actions.Store) []Tool {
	if isNil(cal) || store == nil {
		return nil
	}
	return []Tool{
		{
			Name: "calendar_create",
			Description: "Propose creating a Google Calendar event. This does NOT create it yet: " +
				"it files a pending action and returns a summary you must read aloud, then ask " +
				"the user to confirm (they say yes, or approve on the phone). Only confirm " +
				"after they agree. Times are RFC 3339 with offset; end defaults to start + 1 hour.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":    map[string]any{"type": "string", "description": "Event title."},
					"start":    map[string]any{"type": "string", "description": "Start, RFC 3339 with offset."},
					"end":      map[string]any{"type": "string", "description": "End, RFC 3339. Default: start + 1 hour."},
					"location": map[string]any{"type": "string", "description": "Where (optional)."},
				},
				"required": []string{"title", "start"},
			},
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var args createEventArgs
				if err := decodeArgs(raw, &args); err != nil {
					return nil, err
				}
				if strings.TrimSpace(args.Title) == "" {
					return nil, fmt.Errorf("title is required")
				}
				start, end, err := args.times()
				if err != nil {
					return nil, err
				}
				span := whenString(integrations.Event{Start: start, End: end})
				summary := fmt.Sprintf("create '%s' on %s?", args.Title, span)
				payload, err := json.Marshal(args)
				if err != nil {
					return nil, err
				}
				action, err := store.Create("calendar_create", summary, string(payload))
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"status":  "needs_confirmation",
					"summary": summary,
					"action":  action.ID,
					"note":    "Do not say it is created. Read the summary aloud and ask the user to say yes (or approve on their phone). It expires in 15 minutes.",
				}, nil
			},
		},
	}
}

// CalendarCreateExecutor runs a confirmed calendar_create. It is
// the ONLY path that creates events: voice yes, WhatsApp yes,
// and app Approve all claim the pending action first (exactly
// once), then land here.
func CalendarCreateExecutor(cal CalendarCreator) Executor {
	return func(ctx context.Context, payload json.RawMessage) (string, error) {
		var args createEventArgs
		if err := json.Unmarshal(payload, &args); err != nil {
			return "", fmt.Errorf("bad pending payload: %w", err)
		}
		start, end, err := args.times()
		if err != nil {
			return "", err
		}
		if _, err := cal.CreateEvent(ctx, args.Title, start, end, args.Location); err != nil {
			return "", err
		}
		span := whenString(integrations.Event{Start: start, End: end})
		return fmt.Sprintf("Done — created '%s' on %s.", args.Title, span), nil
	}
}

// whenString renders an event's span in local time the way a
// person says it: same-day ranges share the day.
func whenString(ev integrations.Event) string {
	if ev.AllDay {
		return ev.Start.Format("Mon 2 Jan") + " (all day)"
	}
	start := ev.Start.Local()
	end := ev.End.Local()
	day := start.Format("Mon 2 Jan")
	if start.Format("2006-01-02") == end.Format("2006-01-02") {
		return fmt.Sprintf("%s %s – %s", day, start.Format("3:04 PM"), end.Format("3:04 PM"))
	}
	return fmt.Sprintf("%s %s – %s %s", day, start.Format("3:04 PM"),
		end.Format("Mon 2 Jan"), end.Format("3:04 PM"))
}
