// Package actions is Fritz's pending-actions store: things
// that change the world (create an event, send an email) wait
// here until the user confirms — by spoken yes at the desk, by
// WhatsApp yes, or by Approve in the phone app. The shared store
// and its exactly-once approval are the confirmation rule's
// enforcement point (fritz-agent + fritz-mobile-backend skills).
package actions

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS actions (
	id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	summary TEXT NOT NULL,
	payload_json TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'pending',
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	result TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS actions_status ON actions(status, created_at);
`

// Statuses (skill lifecycle).
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusExecuted = "executed"
	StatusDeclined = "declined"
	StatusExpired  = "expired"
	StatusFailed   = "failed"
)

// DefaultTTL: pending actions expire after 15 minutes.
const DefaultTTL = 15 * time.Minute

var (
	// ErrNotFound: no action with that id.
	ErrNotFound = errors.New("actions: not found")
	// ErrNotPending: the action exists but already left pending
	// (approved, declined, or expired) — approvals race-safe.
	ErrNotPending = errors.New("actions: no longer pending")
)

// Action is one pending (or finished) action. Payload is the
// tool's JSON, internal to executors — never served to clients.
type Action struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Summary   string    `json:"summary"`
	Payload   string    `json:"-"`
	Status    string    `json:"status"`
	Result    string    `json:"result,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Store is the SQLite-backed store (same Fritz database file as
// memory; WAL lets voice, server, and phone share it).
type Store struct {
	db *sql.DB
}

// Open creates or opens the store at path.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("actions: create db dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("actions: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		schema,
	} {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("actions: init: %w", err)
		}
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "a_" + hex.EncodeToString(b[:])
}

// Create files a pending action with the default 15-minute TTL.
func (s *Store) Create(kind, summary, payload string) (Action, error) {
	return s.CreateWithTTL(kind, summary, payload, DefaultTTL)
}

// CreateWithTTL files a pending action with an explicit TTL.
func (s *Store) CreateWithTTL(kind, summary, payload string, ttl time.Duration) (Action, error) {
	now := time.Now().UTC()
	a := Action{
		ID:        newID(),
		Kind:      kind,
		Summary:   summary,
		Payload:   payload,
		Status:    StatusPending,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	_, err := s.db.Exec(
		`INSERT INTO actions(id, kind, summary, payload_json, status, created_at, expires_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Kind, a.Summary, a.Payload, a.Status, fmtTime(a.CreatedAt), fmtTime(a.ExpiresAt))
	if err != nil {
		return Action{}, fmt.Errorf("actions: create: %w", err)
	}
	return a, nil
}

func scanAction(row interface{ Scan(...any) error }) (Action, error) {
	var a Action
	var created, expires string
	err := row.Scan(&a.ID, &a.Kind, &a.Summary, &a.Payload, &a.Status, &created, &expires, &a.Result)
	if err != nil {
		return Action{}, err
	}
	a.CreatedAt, a.ExpiresAt = parseTime(created), parseTime(expires)
	return a, nil
}

const actionCols = `id, kind, summary, payload_json, status, created_at, expires_at, result`

// expireOld marks stale pendings expired. Called on reads so
// nothing stale can be listed, confirmed, or executed.
func (s *Store) expireOld() {
	_, _ = s.db.Exec(
		`UPDATE actions SET status = ? WHERE status = ? AND expires_at <= ?`,
		StatusExpired, StatusPending, fmtTime(time.Now().UTC()))
}

// LatestPending returns the newest unexpired pending action —
// the one a spoken "yes" refers to.
func (s *Store) LatestPending() (Action, bool, error) {
	s.expireOld()
	row := s.db.QueryRow(
		`SELECT `+actionCols+` FROM actions WHERE status = ?
		 ORDER BY created_at DESC LIMIT 1`, StatusPending)
	a, err := scanAction(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Action{}, false, nil
	}
	if err != nil {
		return Action{}, false, fmt.Errorf("actions: latest: %w", err)
	}
	return a, true, nil
}

// PendingList returns unexpired pending actions, newest first
// (the phone's approval queue).
func (s *Store) PendingList(limit int) ([]Action, error) {
	if limit <= 0 {
		limit = 50
	}
	s.expireOld()
	rows, err := s.db.Query(
		`SELECT `+actionCols+` FROM actions WHERE status = ?
		 ORDER BY created_at DESC LIMIT ?`, StatusPending, limit)
	if err != nil {
		return nil, fmt.Errorf("actions: list: %w", err)
	}
	defer rows.Close()
	var out []Action
	for rows.Next() {
		a, err := scanAction(rows)
		if err != nil {
			return nil, fmt.Errorf("actions: scan: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// claim atomically moves one pending action to next. Exactly
// one caller wins: voice and phone approving at the same moment
// can never both execute (the skill's double-send guard).
func (s *Store) claim(id, next string) (Action, error) {
	s.expireOld()
	res, err := s.db.Exec(
		`UPDATE actions SET status = ? WHERE id = ? AND status = ?`,
		next, id, StatusPending)
	if err != nil {
		return Action{}, fmt.Errorf("actions: claim: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Distinguish "no such action" from "already handled".
		var status string
		err := s.db.QueryRow(`SELECT status FROM actions WHERE id = ?`, id).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return Action{}, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		if err != nil {
			return Action{}, fmt.Errorf("actions: claim: %w", err)
		}
		return Action{}, fmt.Errorf("%w: %s is %s", ErrNotPending, id, status)
	}
	row := s.db.QueryRow(`SELECT `+actionCols+` FROM actions WHERE id = ?`, id)
	a, err := scanAction(row)
	if err != nil {
		return Action{}, fmt.Errorf("actions: claim reload: %w", err)
	}
	return a, nil
}

// Approve atomically claims a pending action for execution.
func (s *Store) Approve(id string) (Action, error) { return s.claim(id, StatusApproved) }

// Decline atomically drops a pending action (phone Decline, or
// a spoken non-yes at the desk).
func (s *Store) Decline(id string) error {
	_, err := s.claim(id, StatusDeclined)
	return err
}

// Finish records an approved action's outcome.
func (s *Store) Finish(id, result string, failed bool) error {
	status := StatusExecuted
	if failed {
		status = StatusFailed
	}
	_, err := s.db.Exec(
		`UPDATE actions SET status = ?, result = ? WHERE id = ? AND status = ?`,
		status, result, id, StatusApproved)
	if err != nil {
		return fmt.Errorf("actions: finish: %w", err)
	}
	return nil
}
