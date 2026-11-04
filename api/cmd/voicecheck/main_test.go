package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/francorobles/fritz/api/internal/activity"
	"github.com/francorobles/fritz/api/internal/alerts"
	"github.com/francorobles/fritz/api/internal/memory"
)

func TestLooksVisual(t *testing.T) {
	visual := []string{
		"what shirt am I wearing",
		"Hi Fritz, can you see me",
		"what color is my shirt",
		"look at this",
		"what am I holding",
		"read this label for me",
		"how do I look today",
	}
	for _, s := range visual {
		if !looksVisual(s) {
			t.Errorf("looksVisual(%q) = false, want true", s)
		}
	}
	plain := []string{
		"what time is it",
		"what is the value of peso to dollar right now",
		"remind me to buy milk",
		"I saw a great movie yesterday",
	}
	for _, s := range plain {
		if looksVisual(s) {
			t.Errorf("looksVisual(%q) = true, want false", s)
		}
	}
}

// TestArrivalPrompt: the greeting mentions time away and what Fritz
// finished remotely while the user was gone — but not things done
// before they left, by voice, or that failed.
func TestArrivalPrompt(t *testing.T) {
	log, err := activity.Open(filepath.Join(t.TempDir(), "fritz.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	leftAt := time.Now().Add(-20 * time.Minute)
	_ = log.Add(activity.Entry{At: leftAt.Add(-time.Minute), Source: "app", Kind: activity.KindAction, Detail: "OLD", OK: true})
	_ = log.Add(activity.Entry{At: leftAt.Add(5 * time.Minute), Source: "whatsapp", Kind: activity.KindAction, Detail: "approved: reply to Sarah", OK: true})
	_ = log.Add(activity.Entry{At: leftAt.Add(6 * time.Minute), Source: "app", Kind: activity.KindAction, Detail: "FAILED", OK: false})
	_ = log.Add(activity.Entry{At: leftAt.Add(7 * time.Minute), Source: "voice", Kind: activity.KindTool, Detail: "TOOL", OK: true})

	focus := &alerts.Focus{State: openStore(t)}
	got, check, _ := arrivalPrompt(leftAt, time.Now(), log, focus)
	if !strings.Contains(got, "about 20 minutes away") || !strings.Contains(got, "reply to Sarah") {
		t.Errorf("prompt = %q", got)
	}
	// The hello comes first and uses no tools; the email check is
	// its own turn after it.
	if !strings.Contains(got, "Don't use any tools") || strings.Contains(got, "email") {
		t.Errorf("greeting should not check email: %q", got)
	}
	if !strings.Contains(check, "email") || strings.Contains(check, "Greet") {
		t.Errorf("check = %q", check)
	}
	for _, unwanted := range []string{"OLD", "FAILED", "TOOL"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("prompt includes %s: %q", unwanted, got)
		}
	}
	if first, _, _ := arrivalPrompt(time.Time{}, time.Now(), nil, focus); strings.Contains(first, "away") {
		t.Errorf("first arrival should not mention time away: %q", first)
	}
}

// TestArrivalPromptWhereWasI: coming back, Fritz reminds the user
// where they stopped and what he held, once, without tool calls.
func TestArrivalPromptWhereWasI(t *testing.T) {
	st := openStore(t)
	focus := &alerts.Focus{State: st}
	leftAt := time.Now().Add(-10 * time.Minute)
	_ = alerts.SaveProgress(st, alerts.Progress{Task: "the pricing section", Next: "the discount table", At: leftAt})
	_ = focus.Hold(`Email from Dana: "Q4 update"`)

	got, check, announced := arrivalPrompt(leftAt, time.Now(), nil, focus)
	for _, want := range []string{"pricing section", "discount table", "Q4 update", "Don't use any tools"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q: %q", want, got)
		}
	}
	if check != "" {
		t.Errorf("news to tell already: no email check, got %q", check)
	}
	// The greeting failed (announced not called): nothing is lost.
	retry, _, announced := arrivalPrompt(leftAt, time.Now(), nil, focus)
	if !strings.Contains(retry, "pricing section") || !strings.Contains(retry, "Q4 update") {
		t.Fatalf("a failed greeting lost the reminder: %q", retry)
	}
	// Delivered: told once, not repeated.
	announced()
	again, _, _ := arrivalPrompt(leftAt, time.Now(), nil, focus)
	if strings.Contains(again, "pricing") || strings.Contains(again, "Q4") {
		t.Errorf("second arrival repeated the reminder: %q", again)
	}
}

func openStore(t *testing.T) *memory.Store {
	t.Helper()
	st, err := memory.Open(filepath.Join(t.TempDir(), "fritz.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// The log that motivated pendingTurn: "Hey Fritz can you check my
// email" was endpointed at a pause, then "…Gmail" arrived while
// Fritz was still thinking. The reply must be taken back and the
// whole sentence answered once.
func TestPendingTurnTakeBack(t *testing.T) {
	var p pendingTurn
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := "Hey Fritz can you check my email"
	p.open(first, cancel)

	// A late restatement of the same words is not new speech.
	if _, ok := p.takeBack("Hey Fritz can you"); ok {
		t.Fatal("restatement took the reply back")
	}
	heard, ok := p.takeBack("Hey Fritz can you check my email Gmail")
	if !ok || heard != first {
		t.Fatalf("takeBack = %q, %v; want %q, true", heard, ok, first)
	}
	if ctx.Err() == nil {
		t.Fatal("reply context not cancelled")
	}
	if p.commit(ctx) {
		t.Fatal("commit allowed a sound from a taken-back reply")
	}
	now := time.Now()
	if got, merged := p.merge("Gmail", now); !merged || got != first+" Gmail" {
		t.Fatalf("merge = %q, %v", got, merged)
	}
	// The carry is used once.
	if got, merged := p.merge("thanks", now); merged || got != "thanks" {
		t.Fatalf("second merge = %q, %v", got, merged)
	}
}

func TestPendingTurnClosesWhenTalking(t *testing.T) {
	var p pendingTurn
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.open("Hey Fritz what time is it", cancel)
	if !p.commit(ctx) {
		t.Fatal("commit refused a live reply")
	}
	// Once Fritz is talking, more words never cancel the reply.
	if _, ok := p.takeBack("Hey Fritz what time is it in Tokyo"); ok || ctx.Err() != nil {
		t.Fatal("reply taken back after Fritz started talking")
	}
}

func TestPendingTurnCarryExpires(t *testing.T) {
	var p pendingTurn
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.open("Hey Fritz check my", cancel)
	p.takeBack("Hey Fritz check my calendar")
	if got, merged := p.merge("calendar", time.Now().Add(continuationWindow+time.Second)); merged || got != "calendar" {
		t.Fatalf("stale carry merged: %q", got)
	}
}
