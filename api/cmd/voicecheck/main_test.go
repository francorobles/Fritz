package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/francorobles/fritz/api/internal/activity"
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

	got := arrivalPrompt(leftAt, time.Now(), log)
	if !strings.Contains(got, "about 20 minutes away") || !strings.Contains(got, "reply to Sarah") {
		t.Errorf("prompt = %q", got)
	}
	for _, unwanted := range []string{"OLD", "FAILED", "TOOL"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("prompt includes %s: %q", unwanted, got)
		}
	}
	if first := arrivalPrompt(time.Time{}, time.Now(), nil); strings.Contains(first, "away") {
		t.Errorf("first arrival should not mention time away: %q", first)
	}
}
