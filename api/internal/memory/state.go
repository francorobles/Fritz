package memory

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// State is a tiny key/value table for flags both processes need:
// the voice loop (camera, mic) and the HTTP server (WhatsApp
// webhook) share this database file, so "sentry mode is on" set
// by voice is seen by a WhatsApp reply and vice versa, with no
// IPC of their own.

// SetState stores value under key, stamped now.
func (s *Store) SetState(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO state(key, value, updated_at) VALUES(?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, fmtTime(time.Now()))
	if err != nil {
		return fmt.Errorf("memory: set state %s: %w", key, err)
	}
	return nil
}

// State returns the value under key and when it was set; ok is
// false when the key was never set.
func (s *Store) State(key string) (value string, at time.Time, ok bool, err error) {
	var stamp string
	err = s.db.QueryRow(`SELECT value, updated_at FROM state WHERE key = ?`, key).Scan(&value, &stamp)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, fmt.Errorf("memory: state %s: %w", key, err)
	}
	return value, parseTime(stamp), true, nil
}

// Alerted reports whether key was already recorded by MarkAlerted
// (an email already judged, a photo already sent).
func (s *Store) Alerted(key string) (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM alerts WHERE key = ?`, key).Scan(&n); err != nil {
		return false, fmt.Errorf("memory: alerted %s: %w", key, err)
	}
	return n > 0, nil
}

// MarkAlerted records key once. added is false when it was
// already there, so concurrent pollers can't alert twice.
func (s *Store) MarkAlerted(key, kind, text string) (added bool, err error) {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO alerts(key, kind, text, at) VALUES(?, ?, ?, ?)`,
		key, kind, text, fmtTime(time.Now()))
	if err != nil {
		return false, fmt.Errorf("memory: mark alerted %s: %w", key, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
