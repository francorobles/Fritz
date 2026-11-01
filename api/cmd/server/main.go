package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/francorobles/fritz/api/internal/actions"
	"github.com/francorobles/fritz/api/internal/activity"
	"github.com/francorobles/fritz/api/internal/agent"
	"github.com/francorobles/fritz/api/internal/alerts"
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
	// The activity log shares the database too; /activity is the
	// dashboard's "what did Fritz do" view.
	actLog, logErr := activity.Open(dbPath)
	if logErr != nil {
		log.Printf("activity: log unavailable: %v", logErr)
		actLog = nil
	} else {
		defer actLog.Close()
	}
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
			execMap = agent.Executors(reg)
			if brain != nil {
				brain.Confirmer = &agent.Confirmer{Store: actStore, Exec: execMap, Activity: actLog, Source: "whatsapp"}
			}
			if os.Getenv("FRITZ_APP_TOKEN") == "" {
				log.Print("FRITZ_APP_TOKEN unset — /actions phone approval routes are off")
			}
		}
		// WhatsApp replies get the same tools as voice (minus the
		// camera): read and answer mail, check the calendar, turn
		// sentry mode off. Gated tools only file pending actions.
		if brain != nil {
			brain.Activity = actLog
			brain.Tools = append(brain.Tools, agent.MailTools(reg.Mailboxes)...)
			if actStore != nil {
				brain.Tools = append(brain.Tools, agent.EmailReplyTool(reg.Mailboxes, actStore)...)
			}
			if reg.Calendar != nil {
				brain.Tools = append(brain.Tools, agent.CalendarTools(reg.Calendar)...)
				if actStore != nil {
					brain.Tools = append(brain.Tools, agent.CalendarCreateTool(reg.Calendar, actStore)...)
				}
			}
			brain.Tools = append(brain.Tools, agent.SentryTool(store, nil)...)
			names := make([]string, len(brain.Tools))
			for i, t := range brain.Tools {
				names[i] = t.Name
			}
			log.Printf("whatsapp tools: %s", strings.Join(names, ", "))
		}
		if reg.WhatsAppHook != nil {
			reg.WhatsAppHook.Logf = func(msg string) { log.Print(msg) }
			// Every owner message opens WhatsApp's 24 h window for
			// free-form alerts and releases a held sentry photo.
			if notifier := alerts.NewNotifier(icfg.WhatsApp, reg.WhatsApp, store, dbPath); notifier != nil {
				notifier.Logf = func(msg string) { log.Print(msg) }
				reg.WhatsAppHook.OnInbound = func(ctx context.Context, _ integrations.InboundMessage) { notifier.Inbound(ctx) }
			}
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
		Activity:     actLog,
		ActionExec:   execMap,
		AppToken:     os.Getenv("FRITZ_APP_TOKEN"),
	})))
}
