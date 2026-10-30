package main

import "testing"

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
