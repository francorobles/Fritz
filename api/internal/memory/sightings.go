package memory

import (
	"fmt"
	"time"
)

// Sighting is where Fritz last saw an object ("keys": "on your
// right", 3:40 PM), so "where are my keys?" has an answer after a
// restart, or after the keys left the frame. Gone is when a later
// scan stopped finding it (zero while still in view).
type Sighting struct {
	Name   string    `json:"name"`
	Place  string    `json:"place"`
	SeenAt time.Time `json:"seen_at"`
	GoneAt time.Time `json:"gone_at,omitzero"`
}

// SaveSighting upserts one object's latest sighting.
func (s *Store) SaveSighting(v Sighting) error {
	gone := ""
	if !v.GoneAt.IsZero() {
		gone = fmtTime(v.GoneAt)
	}
	_, err := s.db.Exec(
		`INSERT INTO sightings(name, place, seen_at, gone_at) VALUES(?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET place=excluded.place, seen_at=excluded.seen_at, gone_at=excluded.gone_at`,
		v.Name, v.Place, fmtTime(v.SeenAt), gone)
	if err != nil {
		return fmt.Errorf("memory: save sighting: %w", err)
	}
	return nil
}

// Sightings returns every remembered sighting, most recent first.
func (s *Store) Sightings() ([]Sighting, error) {
	rows, err := s.db.Query(`SELECT name, place, seen_at, gone_at FROM sightings ORDER BY seen_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("memory: sightings: %w", err)
	}
	defer rows.Close()
	out := []Sighting{}
	for rows.Next() {
		var v Sighting
		var seen, gone string
		if err := rows.Scan(&v.Name, &v.Place, &seen, &gone); err != nil {
			return nil, fmt.Errorf("memory: scan sighting: %w", err)
		}
		v.SeenAt = parseTime(seen)
		if gone != "" {
			v.GoneAt = parseTime(gone)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
