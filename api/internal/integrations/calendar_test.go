package integrations

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	calendarapi "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

func fakeCalendar(t *testing.T) *Calendar {
	t.Helper()
	transport := transportFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(req.URL.Path, "/calendars/primary/events") {
			t.Errorf("path = %q", req.URL.Path)
		}
		return jsonResponse(200, `{"items": [
			{"id": "e1", "summary": "Dentist", "location": "Clinic",
			 "start": {"dateTime": "2030-10-31T08:00:00+08:00"},
			 "end": {"dateTime": "2030-10-31T09:00:00+08:00"}},
			{"id": "e2", "summary": "Raya holiday",
			 "start": {"date": "2030-11-01"}, "end": {"date": "2030-11-02"}}
		]}`), nil
	})
	svc, err := calendarapi.NewService(context.Background(),
		option.WithHTTPClient(&http.Client{Transport: transport}),
		option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	return &Calendar{svc: svc}
}

func TestCalendarCreateEvent(t *testing.T) {
	transport := transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/calendars/primary/events") {
			t.Errorf("%s %s, want POST events insert", req.Method, req.URL.Path)
		}
		body, _ := io.ReadAll(req.Body)
		for _, want := range []string{`"summary":"Dentist"`, `"dateTime":"2030-10-31T08:00:00+08:00"`, `"dateTime":"2030-10-31T09:00:00+08:00"`} {
			if !strings.Contains(string(body), want) {
				t.Errorf("insert body missing %s: %s", want, body)
			}
		}
		return jsonResponse(200, `{"id": "new1", "summary": "Dentist", "location": "Clinic"}`), nil
	})
	svc, err := calendarapi.NewService(context.Background(),
		option.WithHTTPClient(&http.Client{Transport: transport}),
		option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	cal := &Calendar{svc: svc}

	start := time.Date(2030, 10, 31, 8, 0, 0, 0, time.FixedZone("PHT", 8*3600))
	ev, err := cal.CreateEvent(context.Background(), "Dentist", start, start.Add(time.Hour), "Clinic")
	if err != nil {
		t.Fatal(err)
	}
	if ev.ID != "new1" || ev.Summary != "Dentist" {
		t.Errorf("created = %+v", ev)
	}
	if _, err := cal.CreateEvent(context.Background(), "  ", start, start.Add(time.Hour), ""); err == nil {
		t.Error("empty title must error")
	}
	if _, err := cal.CreateEvent(context.Background(), "X", start, start, ""); err == nil {
		t.Error("end == start must error")
	}
}

// A Calendar that never built (nil receiver or nil service)
// must return an error, never panic — the agent offered the
// tool with a nil client once and crashed voicecheck.
func TestCalendarNotConnected(t *testing.T) {
	var nilCal *Calendar
	if _, err := nilCal.ListEvents(context.Background(), time.Now(), time.Now(), 5); err == nil {
		t.Error("nil Calendar ListEvents: want error, not panic/nil")
	}
	if _, err := nilCal.CreateEvent(context.Background(), "X", time.Now(), time.Now().Add(time.Hour), ""); err == nil {
		t.Error("nil Calendar CreateEvent: want error, not panic/nil")
	}
	empty := &Calendar{}
	if _, err := empty.ListEvents(context.Background(), time.Now(), time.Now(), 5); err == nil {
		t.Error("svc-less Calendar ListEvents: want error, not panic/nil")
	}
	if _, err := empty.CreateEvent(context.Background(), "X", time.Now(), time.Now().Add(time.Hour), ""); err == nil {
		t.Error("svc-less Calendar CreateEvent: want error, not panic/nil")
	}
}

func TestCalendarListEvents(t *testing.T) {
	cal := fakeCalendar(t)
	from := time.Date(2030, 10, 31, 0, 0, 0, 0, time.UTC)
	events, err := cal.ListEvents(context.Background(), from, from.AddDate(0, 0, 7), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v, want 2", events)
	}
	if events[0].Summary != "Dentist" || events[0].AllDay || events[0].Start.Hour() != 8 {
		t.Errorf("timed event = %+v", events[0])
	}
	if !events[1].AllDay || events[1].Summary != "Raya holiday" {
		t.Errorf("all-day event = %+v", events[1])
	}
}
