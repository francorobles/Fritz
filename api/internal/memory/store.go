// Package memory is Fritz's long-term memory: a SQLite store of
// durable facts about the user — routines, preferences, people,
// tasks — plus the conversation log, folded together nightly by
// Muse Wonder (see consolidate.go). See the fritz-agent skill.
//
// The store never holds personality, health, or mood judgments
// (hackathon-banned, and the wrong product); Remember refuses such
// text. Routines are fine: "user clears email around 9".
package memory

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS memories (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	kind TEXT NOT NULL,
	text TEXT NOT NULL,
	source TEXT NOT NULL DEFAULT '',
	confidence REAL NOT NULL DEFAULT 0.5,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS turns (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	role TEXT NOT NULL,
	content TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS turns_created ON turns(created_at);
CREATE TABLE IF NOT EXISTS sightings (
	name TEXT PRIMARY KEY,
	place TEXT NOT NULL,
	seen_at TEXT NOT NULL,
	gone_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS state (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS alerts (
	key TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	text TEXT NOT NULL DEFAULT '',
	at TEXT NOT NULL
);
`

// Store is a SQLite-backed memory store. Safe for concurrent use:
// a single connection serializes access (memory ops are tiny), and
// WAL mode lets other processes (the HTTP server) read meanwhile.
type Store struct {
	db *sql.DB
}

// Open creates or opens the store at path, making parent
// directories as needed.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("memory: create db dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("memory: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		schema,
	} {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("memory: init: %w", err)
		}
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Memory kinds. Only these are stored: routines, preferences,
// people, and open tasks — never judgments.
const (
	KindRoutine    = "routine"
	KindPreference = "preference"
	KindPerson     = "person"
	KindTask       = "task"
)

// ValidKind reports whether kind is a storable memory kind.
func ValidKind(kind string) bool {
	switch kind {
	case KindRoutine, KindPreference, KindPerson, KindTask:
		return true
	}
	return false
}

// Memory is one durable fact.
type Memory struct {
	ID         int64     `json:"id"`
	Kind       string    `json:"kind"`
	Text       string    `json:"text"`
	Source     string    `json:"source"` // conversation | consolidation:2026-10-30 | ...
	Confidence float64   `json:"confidence"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Turn is one conversation message.
type Turn struct {
	ID        int64     `json:"id"`
	Role      string    `json:"role"` // user | assistant
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

var (
	// ErrNotFound is returned when a memory id does not exist.
	ErrNotFound = errors.New("memory: not found")
	// ErrInvalid covers a bad kind, empty text, or a bad role.
	ErrInvalid = errors.New("memory: invalid")
	// ErrBlocked covers text refused by the safety guard below.
	ErrBlocked = errors.New("memory: blocked")
)

// bannedFragments never enter memory: no personality, health, or
// mood judgments about the user (see the package comment). This is
// a first-pass substring filter on lowercased text — the nightly
// consolidation prompt instructs the model the same way, and the
// dashboard lets the user delete anything that slips by.
var bannedFragments = []string{
	"depress", "anxi", "bipolar", "schizo", "autis", "diagnos",
	"disorder", "therap", "suicid", "self-harm", "self harm",
	"personality", "mental health", "mood",
}

func checkText(text string) error {
	lower := strings.ToLower(text)
	for _, f := range bannedFragments {
		if strings.Contains(lower, f) {
			return fmt.Errorf("%w: text mentions %q — memories hold routines, preferences, people, and tasks, never personality, health, or mood judgments", ErrBlocked, f)
		}
	}
	return nil
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// Remember stores a durable fact. Re-remembering the same kind+text
// (any case) refreshes that memory's confidence and timestamp
// instead of duplicating it — the nightly consolidation relies on
// this to be safely re-runnable.
func (s *Store) Remember(kind, text, source string, confidence float64) (Memory, error) {
	if !ValidKind(kind) {
		return Memory{}, fmt.Errorf("%w: kind %q (want routine, preference, person, or task)", ErrInvalid, kind)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Memory{}, fmt.Errorf("%w: text is empty", ErrInvalid)
	}
	if err := checkText(text); err != nil {
		return Memory{}, err
	}
	confidence = clamp01(confidence)

	var existingID int64
	err := s.db.QueryRow(
		`SELECT id FROM memories WHERE kind = ? AND LOWER(text) = LOWER(?)`,
		kind, text).Scan(&existingID)
	if err == nil {
		m, gerr := s.Get(existingID)
		if gerr != nil {
			return Memory{}, gerr
		}
		m.Confidence = confidence
		return s.Update(m)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Memory{}, fmt.Errorf("memory: remember: %w", err)
	}

	now := time.Now().UTC()
	res, err := s.db.Exec(
		`INSERT INTO memories(kind, text, source, confidence, created_at, updated_at)
		 VALUES(?, ?, ?, ?, ?, ?)`,
		kind, text, source, confidence, fmtTime(now), fmtTime(now))
	if err != nil {
		return Memory{}, fmt.Errorf("memory: remember: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Memory{}, fmt.Errorf("memory: remember: %w", err)
	}
	return s.Get(id)
}

// Get returns one memory by id (ErrNotFound when missing).
func (s *Store) Get(id int64) (Memory, error) {
	row := s.db.QueryRow(
		`SELECT id, kind, text, source, confidence, created_at, updated_at
		 FROM memories WHERE id = ?`, id)
	m, err := scanMemory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Memory{}, fmt.Errorf("%w: id %d", ErrNotFound, id)
	}
	return m, err
}

// Update rewrites a memory's kind, text, and confidence and bumps
// updated_at. Validated the same way as Remember.
func (s *Store) Update(m Memory) (Memory, error) {
	if !ValidKind(m.Kind) {
		return Memory{}, fmt.Errorf("%w: kind %q", ErrInvalid, m.Kind)
	}
	m.Text = strings.TrimSpace(m.Text)
	if m.Text == "" {
		return Memory{}, fmt.Errorf("%w: text is empty", ErrInvalid)
	}
	if err := checkText(m.Text); err != nil {
		return Memory{}, err
	}
	res, err := s.db.Exec(
		`UPDATE memories SET kind = ?, text = ?, confidence = ?, updated_at = ?
		 WHERE id = ?`,
		m.Kind, m.Text, clamp01(m.Confidence), fmtTime(time.Now().UTC()), m.ID)
	if err != nil {
		return Memory{}, fmt.Errorf("memory: update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Memory{}, fmt.Errorf("%w: id %d", ErrNotFound, m.ID)
	}
	return s.Get(m.ID)
}

// Forget deletes one memory (ErrNotFound when missing).
func (s *Store) Forget(id int64) error {
	res, err := s.db.Exec(`DELETE FROM memories WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("memory: forget: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: id %d", ErrNotFound, id)
	}
	return nil
}

// List returns every memory, most recently updated first.
func (s *Store) List() ([]Memory, error) {
	return s.queryMemories(`SELECT id, kind, text, source, confidence, created_at, updated_at
		FROM memories ORDER BY updated_at DESC`)
}

// Snapshot returns the top memories by confidence, then recency —
// the ones injected into the system prompt. limit defaults to 30.
func (s *Store) Snapshot(limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 30
	}
	return s.queryMemories(`SELECT id, kind, text, source, confidence, created_at, updated_at
		FROM memories ORDER BY confidence DESC, updated_at DESC LIMIT ?`, limit)
}

// SnapshotText renders the snapshot as prompt-ready lines (""
// when nothing is remembered yet).
func (s *Store) SnapshotText(limit int) (string, error) {
	mems, err := s.Snapshot(limit)
	if err != nil {
		return "", err
	}
	if len(mems) == 0 {
		return "", nil
	}
	var b strings.Builder
	for _, m := range mems {
		fmt.Fprintf(&b, "- [%s] %s\n", m.Kind, m.Text)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// Recall searches memories whose text contains query
// (case-insensitive; empty query returns the snapshot).
func (s *Store) Recall(query string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 20
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return s.Snapshot(limit)
	}
	like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(query) + "%"
	return s.queryMemories(`SELECT id, kind, text, source, confidence, created_at, updated_at
		FROM memories WHERE text LIKE ? ESCAPE '\'
		ORDER BY confidence DESC, updated_at DESC LIMIT ?`, like, limit)
}

// AddTurn appends one conversation message to the log.
func (s *Store) AddTurn(role, content string) error {
	if role != "user" && role != "assistant" {
		return fmt.Errorf("%w: role %q (want user or assistant)", ErrInvalid, role)
	}
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("%w: content is empty", ErrInvalid)
	}
	_, err := s.db.Exec(
		`INSERT INTO turns(role, content, created_at) VALUES(?, ?, ?)`,
		role, content, fmtTime(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("memory: add turn: %w", err)
	}
	return nil
}

// RecentTurns returns the newest limit turns in chronological
// (speaking) order — used to restore conversation context.
func (s *Store) RecentTurns(limit int) ([]Turn, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(
		`SELECT id, role, content, created_at FROM (
			SELECT id, role, content, created_at FROM turns ORDER BY id DESC LIMIT ?
		) ORDER BY id ASC`, limit)
	if err != nil {
		return nil, fmt.Errorf("memory: recent turns: %w", err)
	}
	return scanTurns(rows)
}

// TurnsSince returns turns at or after since, oldest first — the
// consolidation's view of a day's conversation.
func (s *Store) TurnsSince(since time.Time, limit int) ([]Turn, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.db.Query(
		`SELECT id, role, content, created_at FROM turns
		 WHERE created_at >= ? ORDER BY id ASC LIMIT ?`,
		fmtTime(since), limit)
	if err != nil {
		return nil, fmt.Errorf("memory: turns since: %w", err)
	}
	return scanTurns(rows)
}

// DeleteTurnsBefore prunes old conversation turns, keeping the
// store bounded. Returns how many were deleted.
func (s *Store) DeleteTurnsBefore(t time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM turns WHERE created_at < ?`, fmtTime(t))
	if err != nil {
		return 0, fmt.Errorf("memory: prune turns: %w", err)
	}
	return res.RowsAffected()
}

type rowScanner interface{ Scan(dest ...any) error }

func scanMemory(row rowScanner) (Memory, error) {
	var m Memory
	var created, updated string
	if err := row.Scan(&m.ID, &m.Kind, &m.Text, &m.Source, &m.Confidence, &created, &updated); err != nil {
		return Memory{}, err
	}
	m.CreatedAt, m.UpdatedAt = parseTime(created), parseTime(updated)
	return m, nil
}

func (s *Store) queryMemories(query string, args ...any) ([]Memory, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: query: %w", err)
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, fmt.Errorf("memory: scan: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory: query: %w", err)
	}
	return out, nil
}

func scanTurns(rows *sql.Rows) ([]Turn, error) {
	defer rows.Close()
	var out []Turn
	for rows.Next() {
		var t Turn
		var created string
		if err := rows.Scan(&t.ID, &t.Role, &t.Content, &created); err != nil {
			return nil, fmt.Errorf("memory: scan turn: %w", err)
		}
		t.CreatedAt = parseTime(created)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory: turns: %w", err)
	}
	return out, nil
}
