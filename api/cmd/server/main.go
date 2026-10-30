package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/francorobles/fritz/api/internal/httpapi"
	"github.com/francorobles/fritz/api/internal/memory"
	"github.com/francorobles/fritz/api/internal/meta"
)

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	dbPath := os.Getenv("FRITZ_DB")
	if dbPath == "" {
		dbPath = "fritz.db"
	}
	store, err := memory.Open(dbPath)
	if err != nil {
		log.Fatalf("memory store: %v (set FRITZ_DB to a writable path)", err)
	}
	defer store.Close()

	// Consolidation calls Muse Wonder (a slow reasoning model);
	// without a key the memory endpoints still work, consolidation
	// reports 503.
	var metaClient *meta.Client
	if key := os.Getenv("MODEL_API_KEY"); key != "" {
		metaClient = &meta.Client{APIKey: key, HTTP: &http.Client{Timeout: 10 * time.Minute}}
	} else {
		log.Print("MODEL_API_KEY unset — POST /memory/consolidate will be unavailable")
	}

	log.Printf("fritz-api listening on %s (memory: %s)", addr, dbPath)
	log.Fatal(http.ListenAndServe(addr, httpapi.NewRouter(httpapi.Deps{
		Store: store,
		Meta:  metaClient,
	})))
}
