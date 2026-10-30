package integrations

import (
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/oauth2/google"
	calendarapi "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

// Event is one calendar event as lists show it. Times are the
// event's own (Google returns them with offsets); AllDay events
// carry dates, not instants.
type Event struct {
	ID       string
	Summary  string
	Start    time.Time
	End      time.Time
	AllDay   bool
	Location string
}

// Calendar reads the user's primary Google Calendar with the
// same OAuth token as Gmail (cmd/googleauth consents to
// calendar.events alongside the Gmail scopes).
type Calendar struct {
	svc *calendarapi.Service
}

// NewGoogleCalendar builds the client from the shared Google
// OAuth files in the config dir.
func NewGoogleCalendar(ctx context.Context) (*Calendar, error) {
	clientJSON, err := readConfigFile(GoogleClientFile(), "google-client.json (create an OAuth Desktop client; see cmd/googleauth)")
	if err != nil {
		return nil, err
	}
	config, err := google.ConfigFromJSON(clientJSON, GoogleScopes...)
	if err != nil {
		return nil, fmt.Errorf("integrations: calendar client config: %w", err)
	}
	tok, err := loadTokenFile(GoogleTokenFile())
	if err != nil {
		return nil, fmt.Errorf("integrations: calendar token: %w (run: go run ./cmd/googleauth)", err)
	}
	src := newPersistingTokenSource(config.TokenSource(ctx, tok), GoogleTokenFile())
	svc, err := calendarapi.NewService(ctx, option.WithTokenSource(src))
	if err != nil {
		return nil, fmt.Errorf("integrations: calendar service: %w", err)
	}
	return &Calendar{svc: svc}, nil
}

// ListEvents returns events overlapping [from, to), soonest
// first, recurring events expanded (SingleEvents per the
// fritz-integrations skill).
func (c *Calendar) ListEvents(ctx context.Context, from, to time.Time, max int) ([]Event, error) {
	if c == nil || c.svc == nil {
		return nil, fmt.Errorf("calendar: not connected (enable calendar in integrations.json and check the startup status)")
	}
	if max <= 0 || max > 50 {
		max = 20
	}
	resp, err := c.svc.Events.List("primary").
		TimeMin(from.Format(time.RFC3339)).
		TimeMax(to.Format(time.RFC3339)).
		SingleEvents(true).
		OrderBy("startTime").
		MaxResults(int64(max)).
		Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("calendar: list: %w", err)
	}
	events := make([]Event, 0, len(resp.Items))
	for _, item := range resp.Items {
		ev := Event{ID: item.Id, Summary: item.Summary, Location: item.Location}
		if ev.Summary == "" {
			ev.Summary = "(no title)"
		}
		switch {
		case item.Start != nil && item.Start.DateTime != "":
			start, err1 := time.Parse(time.RFC3339, item.Start.DateTime)
			end, err2 := time.Parse(time.RFC3339, item.End.DateTime)
			if err1 != nil || err2 != nil {
				continue // undated or unparseable: not speakable
			}
			ev.Start, ev.End = start, end
		case item.Start != nil && item.Start.Date != "":
			start, err1 := time.Parse("2006-01-02", item.Start.Date)
			end, err2 := time.Parse("2006-01-02", item.End.Date)
			if err1 != nil || err2 != nil {
				continue
			}
			ev.Start, ev.End, ev.AllDay = start, end, true
		default:
			continue
		}
		events = append(events, ev)
	}
	return events, nil
}

// CreateEvent inserts one timed event and returns it as
// created. This is an action: agent callers must route it
// through the confirmation rule (pending action + explicit
// user yes), never call it speculatively.
func (c *Calendar) CreateEvent(ctx context.Context, summary string, start, end time.Time, location string) (Event, error) {
	if c == nil || c.svc == nil {
		return Event{}, fmt.Errorf("calendar: not connected (enable calendar in integrations.json and check the startup status)")
	}
	if strings.TrimSpace(summary) == "" {
		return Event{}, fmt.Errorf("calendar: event title is empty")
	}
	if !end.After(start) {
		return Event{}, fmt.Errorf("calendar: event end must be after its start")
	}
	ev := &calendarapi.Event{
		Summary:  summary,
		Location: location,
		Start:    &calendarapi.EventDateTime{DateTime: start.Format(time.RFC3339)},
		End:      &calendarapi.EventDateTime{DateTime: end.Format(time.RFC3339)},
	}
	created, err := c.svc.Events.Insert("primary", ev).Context(ctx).Do()
	if err != nil {
		return Event{}, fmt.Errorf("calendar: create: %w", err)
	}
	return Event{
		ID:       created.Id,
		Summary:  created.Summary,
		Location: created.Location,
		Start:    start,
		End:      end,
	}, nil
}
