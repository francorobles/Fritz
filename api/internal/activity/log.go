// Package activity is Fritz's "what did I do" log: every tool call
// and every confirmed or declined action, newest first, for the web
// dashboard (fritz-agent skill). It lives in the same SQLite file
// as memory and pending actions.
//
// Entries hold a short detail, not full tool output: the dashboard
// needs "searched email for 'is:unread' → 3 messages", not the
// bodies of everyone's mail.
package activity

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS activity (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	at TEXT NOT NULL,
	source TEXT NOT NULL,
	kind TEXT NOT NULL,
	name TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT '',
	ok INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS activity_at ON activity(at);
`

// Kinds of entries.
const (
	KindTool   = "tool"   // a tool Spark called
	KindAction = "action" // a pending action confirmed, declined, or failed
	KindAlert  = "alert"  // Fritz reached out: important email, sentry photo
)

// MaxDetail caps an entry's detail in characters.
const MaxDetail = 300

// Entry is one logged event.
type Entry struct {
	ID     int64     `json:"id"`
	At     time.Time `json:"at"`
	Source string    `json:"source"` // voice | whatsapp | app | alerts
	Kind   string    `json:"kind"`
	Name   string    `json:"name"` // tool name or action kind
	Detail string    `json:"detail"`
	OK     bool      `json:"ok"`
}

// Log is the SQLite-backed activity log.
type Log struct {
	db *sql.DB
}

// Open creates or opens the log at path.
func Open(path string) (*Log, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("activity: create db dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("activity: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		schema,
	} {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("activity: init: %w", err)
		}
	}
	return &Log{db: db}, nil
}

// Close closes the database.
func (l *Log) Close() error { return l.db.Close() }

// Add records one entry (At defaults to now; Detail is truncated).
// A nil Log is a no-op, so callers can log unconditionally.
func (l *Log) Add(e Entry) error {
	if l == nil {
		return nil
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	_, err := l.db.Exec(
		`INSERT INTO activity(at, source, kind, name, detail, ok) VALUES(?, ?, ?, ?, ?, ?)`,
		e.At.UTC().Format(time.RFC3339Nano), e.Source, e.Kind, e.Name, Truncate(e.Detail, MaxDetail), e.OK)
	if err != nil {
		return fmt.Errorf("activity: add: %w", err)
	}
	return nil
}

// Recent returns the newest entries first.
func (l *Log) Recent(limit int) ([]Entry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := l.db.Query(
		`SELECT id, at, source, kind, name, detail, ok FROM activity ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("activity: recent: %w", err)
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var at string
		if err := rows.Scan(&e.ID, &at, &e.Source, &e.Kind, &e.Name, &e.Detail, &e.OK); err != nil {
			return nil, fmt.Errorf("activity: scan: %w", err)
		}
		e.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Truncate shortens s to at most n characters, marking the cut.
func Truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
