// Command consolidate runs Fritz's nightly memory job once: it
// folds recent conversation into long-term memory with Muse Wonder
// and prunes old turns. Run it from cron or a systemd timer
// (fritz-consolidate.timer); POST /memory/consolidate does the same
// thing on demand for the demo.
//
//	MODEL_API_KEY=... FRITZ_DB=/var/lib/fritz/fritz.db \
//	  go run ./cmd/consolidate [--days 1] [--prune-days 30]
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/francorobles/fritz/api/internal/memory"
	"github.com/francorobles/fritz/api/internal/meta"
)

func main() {
	dbPath := flag.String("db", "", "SQLite path (default $FRITZ_DB or fritz.db)")
	days := flag.Int("days", 1, "days of conversation to fold into memory")
	pruneDays := flag.Int("prune-days", 30, "delete turns older than this (0 keeps everything)")
	flag.Parse()

	apiKey := os.Getenv("MODEL_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "MODEL_API_KEY is unset — export it first")
		os.Exit(1)
	}
	path := *dbPath
	if path == "" {
		path = os.Getenv("FRITZ_DB")
	}
	if path == "" {
		path = "fritz.db"
	}

	store, err := memory.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "memory store:", err)
		os.Exit(1)
	}
	defer store.Close()

	// Wonder is a big reasoning model with no latency pressure;
	// give it minutes, not the chat client's 30 s default.
	client := &meta.Client{APIKey: apiKey, HTTP: &http.Client{Timeout: 10 * time.Minute}}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	since := time.Now().Add(-time.Duration(*days) * 24 * time.Hour)
	turns, err := store.TurnsSince(since, 1000)
	if err != nil {
		fmt.Fprintln(os.Stderr, "turns:", err)
		os.Exit(1)
	}
	current, err := store.List()
	if err != nil {
		fmt.Fprintln(os.Stderr, "memories:", err)
		os.Exit(1)
	}
	fmt.Printf("consolidate: %d turns, %d existing memories\n", len(turns), len(current))

	diff, _, err := memory.Consolidate(ctx, client, turns, current)
	if err != nil {
		fmt.Fprintln(os.Stderr, "consolidate:", err)
		os.Exit(1)
	}
	source := "consolidation:" + time.Now().UTC().Format("2006-01-02")
	applied, err := store.Apply(diff, source)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apply:", err)
		os.Exit(1)
	}
	fmt.Printf("consolidate: %s (source %s)\n", applied, source)
	for _, skipped := range applied.Skipped {
		fmt.Println("  skipped:", skipped)
	}

	if *pruneDays > 0 {
		cutoff := time.Now().Add(-time.Duration(*pruneDays) * 24 * time.Hour)
		n, err := store.DeleteTurnsBefore(cutoff)
		if err != nil {
			fmt.Fprintln(os.Stderr, "prune:", err)
			os.Exit(1)
		}
		fmt.Printf("consolidate: pruned %d turns older than %d days\n", n, *pruneDays)
	}
}
