package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/francorobles/fritz/api/internal/actions"
	"github.com/francorobles/fritz/api/internal/agent"
	"github.com/francorobles/fritz/api/internal/chat"
	"github.com/francorobles/fritz/api/internal/httpapi"
	"github.com/francorobles/fritz/api/internal/integrations"
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

	// One Meta client: Spark for chat, Wonder for consolidation
	// (a slow reasoning model — minutes, not the 30 s default).
	var metaClient *meta.Client
	if key := os.Getenv("MODEL_API_KEY"); key != "" {
		metaClient = &meta.Client{APIKey: key, HTTP: &http.Client{Timeout: 10 * time.Minute}}
	} else {
		log.Print("MODEL_API_KEY unset — POST /memory/consolidate will be unavailable")
	}

	// Integrations: config-file driven; every provider reports
	// its own state and none of them can take the server down.
	var hook http.Handler
	var actStore *actions.Store
	var execMap map[string]agent.Executor
	icfg, icfgPath, err := integrations.Load()
	if err != nil {
		log.Printf("integrations config: %v (continuing without)", err)
	} else {
		var brain *chat.Brain
		if metaClient != nil {
			brain = &chat.Brain{Client: metaClient, Store: store}
		}
		reg := integrations.Build(icfg, brain)
		if icfgPath != "" {
			log.Printf("integrations config: %s", icfgPath)
		}
		for _, st := range reg.Status {
			state := "off"
			if st.Ready {
				state = "ready"
			} else if st.Enabled {
				state = "BROKEN"
			}
			log.Printf("integration %-9s %-6s %s", st.Name, state, st.Detail)
		}
		// Pending actions share the memory database. The phone
		// approves through /actions (app token), WhatsApp and
		// voice through the same Go-checked confirmer — all
		// claim atomically, so an action executes exactly once.
		if opened, aerr := actions.Open(dbPath); aerr != nil {
			log.Printf("actions: store unavailable: %v", aerr)
		} else {
			actStore = opened
			defer actStore.Close()
			if reg.Calendar != nil {
				execMap = map[string]agent.Executor{
					"calendar_create": agent.CalendarCreateExecutor(reg.Calendar),
				}
			}
			if brain != nil {
				brain.Confirmer = &agent.Confirmer{Store: actStore, Exec: execMap}
			}
			if os.Getenv("FRITZ_APP_TOKEN") == "" {
				log.Print("FRITZ_APP_TOKEN unset — /actions phone approval routes are off")
			}
		}
		if reg.WhatsAppHook != nil {
			reg.WhatsAppHook.Logf = func(msg string) { log.Print(msg) }
			hook = reg.WhatsAppHook
			log.Print("WhatsApp webhook mounted at /webhooks/whatsapp (expose it publicly for Meta to reach it)")
		}
	}

	log.Printf("fritz-api listening on %s (memory: %s)", addr, dbPath)
	log.Fatal(http.ListenAndServe(addr, httpapi.NewRouter(httpapi.Deps{
		Store:        store,
		Meta:         metaClient,
		WhatsAppHook: hook,
		Actions:      actStore,
		ActionExec:   execMap,
		AppToken:     os.Getenv("FRITZ_APP_TOKEN"),
	})))
}
