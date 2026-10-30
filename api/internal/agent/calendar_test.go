package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/francorobles/fritz/api/internal/integrations"
)

type fakeCal struct {
	events []integrations.Event
	from   time.Time
	to     time.Time
}

func (f *fakeCal) ListEvents(_ context.Context, from, to time.Time, _ int) ([]integrations.Event, error) {
	f.from, f.to = from, to
	return f.events, nil
}

type fakeCreator struct {
	created []integrations.Event
}

func (f *fakeCreator) CreateEvent(_ context.Context, summary string, start, end time.Time, location string) (integrations.Event, error) {
	ev := integrations.Event{Summary: summary, Start: start, End: end, Location: location}
	f.created = append(f.created, ev)
	return ev, nil
}

func TestCalendarCreateToolGatesOnConfirmation(t *testing.T) {
	creator := &fakeCreator{}
	store := newActionStore(t)
	if tools := CalendarCreateTool(nil, store); tools != nil {
		t.Error("no calendar -> no create tool")
	}
	if tools := CalendarCreateTool(creator, nil); tools != nil {
		t.Error("no action store -> no create tool (nowhere to file the pending action)")
	}
	tool := CalendarCreateTool(creator, store)[0]
	if tool.Name != "calendar_create" {
		t.Fatalf("tool = %q", tool.Name)
	}
	out, err := tool.Run(context.Background(),
		json.RawMessage(`{"title":"Dentist","start":"2030-10-31T08:00:00+08:00","end":"2030-10-31T09:00:00+08:00"}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(out)
	if !strings.Contains(string(data), "needs_confirmation") || !strings.Contains(string(data), "create 'Dentist'") {
		t.Errorf("tool output = %s, want needs_confirmation with the spoken summary", data)
	}
	// The whole point: calling the tool created NOTHING.
	if len(creator.created) != 0 {
		t.Fatalf("tool created an event without confirmation: %v", creator.created)
	}
	pending, ok, err := store.LatestPending()
	if err != nil || !ok || pending.Kind != "calendar_create" {
		t.Fatalf("pending = %+v, %v, %v", pending, ok, err)
	}

	// The executor (post-confirmation) creates with the filed times.
	result, err := CalendarCreateExecutor(creator)(context.Background(), json.RawMessage(pending.Payload))
	if err != nil {
		t.Fatal(err)
	}
	if len(creator.created) != 1 || !strings.Contains(result, "Done") || !strings.Contains(result, "Dentist") {
		t.Errorf("executor: created=%v result=%q", creator.created, result)
	}
	// Bad times are rejected before filing anything.
	for _, args := range []string{
		`{"title":"X","start":"Friday 2pm"}`,
		`{"title":"X","start":"2030-10-31T09:00:00+08:00","end":"2030-10-31T08:00:00+08:00"}`,
		`{"title":"","start":"2030-10-31T09:00:00+08:00"}`,
	} {
		if _, err := tool.Run(context.Background(), json.RawMessage(args)); err == nil {
			t.Errorf("args %s: want an error", args)
		}
	}
}

func TestCalendarListTool(t *testing.T) {
	if tools := CalendarTools(nil); tools != nil {
		t.Errorf("CalendarTools(nil) = %v, want nil (no tool offered)", tools)
	}
	// Typed nil: a nil *fakeCal in the interface is not == nil,
	// and offering the tool then panics on first use (the Pi crash).
	if tools := CalendarTools((*fakeCal)(nil)); tools != nil {
		t.Errorf("CalendarTools(typed nil) = %v, want nil", tools)
	}
	if tools := CalendarCreateTool((*fakeCreator)(nil), newActionStore(t)); tools != nil {
		t.Errorf("CalendarCreateTool(typed nil) = %v, want nil", tools)
	}
	local := time.Local
	fake := &fakeCal{events: []integrations.Event{
		{Summary: "Dentist", Start: time.Date(2030, 10, 31, 8, 0, 0, 0, local),
			End: time.Date(2030, 10, 31, 12, 0, 0, 0, local), Location: "Clinic"},
		{Summary: "Raya holiday", Start: time.Date(2030, 11, 1, 0, 0, 0, 0, local),
			End: time.Date(2030, 11, 2, 0, 0, 0, 0, local), AllDay: true},
	}}
	tool := CalendarTools(fake)[0]
	if tool.Name != "calendar_list" {
		t.Fatalf("tool = %q", tool.Name)
	}
	out, err := tool.Run(context.Background(),
		json.RawMessage(`{"from":"2030-10-31T08:00:00+08:00","to":"2030-10-31T12:00:00+08:00"}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(out)
	if !strings.Contains(string(data), "Dentist") || !strings.Contains(string(data), "8:00 AM – 12:00 PM") {
		t.Errorf("tool output = %s, want the dentist with a spoken-style range", data)
	}
	if !strings.Contains(string(data), "all day") {
		t.Errorf("tool output = %s, want the all-day event marked", data)
	}
	// Defaults: no from/to still runs (now → +7d).
	if _, err := tool.Run(context.Background(), json.RawMessage(`{}`)); err != nil {
		t.Errorf("default window: %v", err)
	}
	// Bad input is told to the model, never panics.
	for _, args := range []string{
		`{"from":"tomorrow"}`,
		`{"from":"2030-10-31T12:00:00+08:00","to":"2030-10-31T08:00:00+08:00"}`,
		`{"from":"2030-01-01T00:00:00Z","to":"2031-01-01T00:00:00Z"}`,
		`{"bogus":1}`,
	} {
		if _, err := tool.Run(context.Background(), json.RawMessage(args)); err == nil {
			t.Errorf("args %s: want an error", args)
		}
	}
}
