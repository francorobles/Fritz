package activity

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLogNewestFirstAndTruncated(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "fritz.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.Add(Entry{Source: "voice", Kind: KindTool, Name: "email_search", Detail: `{"query":"is:unread"}`, OK: true}); err != nil {
		t.Fatal(err)
	}
	if err := l.Add(Entry{Source: "app", Kind: KindAction, Name: "email_reply", Detail: strings.Repeat("x", 1000), OK: false}); err != nil {
		t.Fatal(err)
	}
	got, err := l.Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "email_reply" || got[1].Name != "email_search" {
		t.Fatalf("Recent = %+v, want newest first", got)
	}
	if n := len([]rune(got[0].Detail)); n != MaxDetail || got[0].OK || !got[1].OK || got[1].At.IsZero() {
		t.Errorf("entry = %+v (detail %d chars)", got[0], n)
	}
	var nilLog *Log
	if err := nilLog.Add(Entry{Name: "x"}); err != nil {
		t.Errorf("nil Log must be a no-op, got %v", err)
	}
}
