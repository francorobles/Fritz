package actions

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "fritz.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestActionLifecycle(t *testing.T) {
	s := newStore(t)
	a, err := s.Create("calendar_create", "create 'Dentist' Friday?", `{"title":"Dentist"}`)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == "" || a.Status != StatusPending {
		t.Fatalf("created = %+v", a)
	}
	latest, ok, err := s.LatestPending()
	if err != nil || !ok || latest.ID != a.ID {
		t.Errorf("LatestPending = %+v, %v, %v", latest, ok, err)
	}
	// Approve claims it; a second approve (or a decline racing
	// it) must lose — exactly one execution, ever.
	claimed, err := s.Approve(a.ID)
	if err != nil || claimed.Status != StatusApproved {
		t.Fatalf("Approve = %+v, %v", claimed, err)
	}
	if _, err := s.Approve(a.ID); !errors.Is(err, ErrNotPending) {
		t.Errorf("second Approve err = %v, want ErrNotPending", err)
	}
	if err := s.Decline(a.ID); !errors.Is(err, ErrNotPending) {
		t.Errorf("Decline after Approve err = %v, want ErrNotPending", err)
	}
	if err := s.Finish(a.ID, "Done — created 'Dentist'.", false); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.LatestPending(); ok {
		t.Error("finished action must not be pending")
	}
	if _, err := s.Approve("a_missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Approve missing err = %v, want ErrNotFound", err)
	}
}

func TestActionDeclineAndExpiry(t *testing.T) {
	s := newStore(t)
	a, _ := s.Create("calendar_create", "one", `{}`)
	if err := s.Decline(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.LatestPending(); ok {
		t.Error("declined action must not be pending")
	}

	// Expired actions can neither be listed nor approved.
	stale, _ := s.CreateWithTTL("calendar_create", "stale", `{}`, 30*time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	if _, ok, _ := s.LatestPending(); ok {
		t.Error("expired action still pending")
	}
	if _, err := s.Approve(stale.ID); !errors.Is(err, ErrNotPending) {
		t.Errorf("Approve expired err = %v, want ErrNotPending", err)
	}
	if list, _ := s.PendingList(10); len(list) != 0 {
		t.Errorf("PendingList = %v, want empty", list)
	}
}

func TestConcurrentApproveExecutesOnce(t *testing.T) {
	s := newStore(t)
	a, _ := s.Create("calendar_create", "race me", `{}`)
	var wg sync.WaitGroup
	wins := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Approve(a.ID)
			wins <- err
		}()
	}
	wg.Wait()
	close(wins)
	succeeded := 0
	for err := range wins {
		if err == nil {
			succeeded++
		} else if !errors.Is(err, ErrNotPending) {
			t.Errorf("losing approve err = %v, want ErrNotPending", err)
		}
	}
	if succeeded != 1 {
		t.Errorf("approvals succeeded = %d, want exactly 1", succeeded)
	}
}
