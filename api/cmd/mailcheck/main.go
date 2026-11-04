// Command mailcheck is the integrations smoke test: it loads
// integrations.json, reports every provider's state, and does a
// live read against each configured inbox (newest few messages)
// and the calendar. Like voicetest for accounts.
//
//	set -a; . /etc/fritz/fritz.env; set +a
//	go run ./cmd/mailcheck
//
// Exit code is 0 only if every ENABLED provider is ready and
// answers. -whatsapp additionally sends the configured alert
// template to your first allowed number (proof the send path
// works; it really messages you).
//
// Alerts (internal/alerts):
//
//	-triage            ask Muse Spark which recent unread mail it would alert on (sends nothing)
//	-alert "text"      send one alert the way Fritz does: free text inside
//	                   WhatsApp's 24 h window, the alert template outside it
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/francorobles/fritz/api/internal/alerts"
	"github.com/francorobles/fritz/api/internal/integrations"
	"github.com/francorobles/fritz/api/internal/memory"
	"github.com/francorobles/fritz/api/internal/meta"
)

var failures int

func pass(format string, args ...any) { fmt.Printf("READY  "+format+"\n", args...) }
func fail(format string, args ...any) {
	fmt.Printf("FAIL   "+format+"\n", args...)
	failures++
}
func off(format string, args ...any) { fmt.Printf("OFF    "+format+"\n", args...) }

func main() {
	sendWhatsApp := flag.Bool("whatsapp", false, "also send the WhatsApp alert template to your first allowed number")
	triage := flag.Bool("triage", false, "ask Muse Spark which recent unread mail is important (dry run, sends nothing)")
	var rules ruleFlags
	flag.Var(&rules, "rule", `with -triage: an interruption rule to judge with (repeatable): -rule "hold project updates unless there's a deadline"`)
	spark := flag.Bool("spark", false, "check the Meta API key with one tiny Muse Spark call (preflight)")
	alertText := flag.String("alert", "", `send one WhatsApp alert like Fritz does: -alert "Test from Fritz"`)
	flag.Parse()

	cfg, path, err := integrations.Load()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if path == "" {
		fmt.Printf("no integrations.json found — everything is disabled.\n")
		fmt.Printf("Create %s (see the integrations package doc for the shape).\n", integrations.ConfigFile())
		os.Exit(1)
	}
	fmt.Printf("config: %s\n\n", path)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	reg := integrations.Build(cfg, nil)

	for _, st := range reg.Status {
		switch {
		case !st.Enabled:
			off("%-9s %s", st.Name, st.Detail)
		case !st.Ready:
			fail("%-9s %s", st.Name, st.Detail)
		default:
			pass("%-9s %s", st.Name, st.Detail)
		}
	}
	fmt.Println()

	for _, box := range reg.Mailboxes {
		sums, err := box.Search(ctx, "", 5)
		if err != nil {
			fail("%s search: %v", box.Name(), err)
			continue
		}
		fmt.Printf("[%s] %d newest:\n", box.Name(), len(sums))
		for _, s := range sums {
			unread := ""
			if s.Unread {
				unread = " (unread)"
			}
			fmt.Printf("  %s  %s — %s%s\n", s.Date.Format("Jan 2 15:04"), s.From, s.Subject, unread)
		}
	}

	if reg.Calendar != nil {
		events, err := reg.Calendar.ListEvents(ctx, time.Now(), time.Now().AddDate(0, 0, 7), 5)
		if err != nil {
			fail("calendar list: %v", err)
		} else {
			fmt.Printf("\n[calendar] %d events in the next 7 days:\n", len(events))
			for _, ev := range events {
				when := ev.Start.Format("Mon Jan 2 3:04 PM")
				if ev.AllDay {
					when = ev.Start.Format("Mon Jan 2") + " (all day)"
				}
				fmt.Printf("  %s  %s\n", when, ev.Summary)
			}
		}
	}

	if *sendWhatsApp {
		switch {
		case reg.WhatsApp == nil:
			fail("whatsapp: not ready (see status above)")
		case len(cfg.WhatsApp.Allow) == 0:
			fail("whatsapp: no allowed number to send the test to")
		default:
			to := cfg.WhatsApp.Allow[0]
			name := cfg.WhatsApp.AlertTemplateOrDefault()
			if err := reg.WhatsApp.SendTemplate(ctx, to, name, "en_US", nil); err != nil {
				fail("whatsapp send template %q: %v", name, err)
			} else {
				pass("whatsapp: sent template %q to %s — check your phone", name, to)
			}
		}
	}

	if *spark {
		checkSpark(ctx)
	}
	if *triage {
		runTriage(ctx, reg, rules)
	}
	if *alertText != "" {
		sendAlert(ctx, cfg, reg, *alertText)
	}

	if failures > 0 {
		fmt.Printf("\n%d check(s) FAILED\n", failures)
		os.Exit(1)
	}
	fmt.Println("\nMAIL CHECK PASSED — every enabled provider answered.")
}

// neverSeen makes -triage judge every unread message without
// recording anything, so it never hides mail from the real watcher.
type neverSeen struct{}

func (neverSeen) Alerted(string) (bool, error)                     { return false, nil }
func (neverSeen) MarkAlerted(string, string, string) (bool, error) { return true, nil }

type ruleFlags []string

func (r *ruleFlags) String() string     { return strings.Join(*r, "; ") }
func (r *ruleFlags) Set(v string) error { *r = append(*r, v); return nil }

func runTriage(ctx context.Context, reg *integrations.Registry, rules []string) {
	key := os.Getenv("MODEL_API_KEY")
	if key == "" {
		fail("triage: MODEL_API_KEY is not set")
		return
	}
	found := 0
	w := &alerts.MailWatcher{
		Boxes: reg.Mailboxes, Judge: &meta.Client{APIKey: key}, Seen: neverSeen{},
		Logf:  func(msg string) {},
		Rules: func() []string { return rules },
		OnImportant: func(_ context.Context, m alerts.ImportantMail) {
			found++
			// What Fritz would do with it: hold (rule), interrupt
			// even during focus (urgent), or tell you normally.
			action := "tell you"
			switch {
			case m.Hold:
				action = "HOLD (your rule)"
			case m.Urgent:
				action = "URGENT (breaks focus)"
			}
			fmt.Printf("  %-22s %s\n", action, alerts.EmailAlert(m))
		},
	}
	fmt.Println("\ntriage: asking Muse Spark about recent unread mail (nothing is sent)")
	if err := w.Check(ctx); err != nil {
		fail("triage: %v", err)
		return
	}
	if found == 0 {
		fmt.Println("  nothing it would interrupt you for")
	}
	pass("triage: done")
}

func sendAlert(ctx context.Context, cfg integrations.Config, reg *integrations.Registry, text string) {
	dbPath := os.Getenv("FRITZ_DB")
	if dbPath == "" {
		dbPath = "fritz.db"
	}
	store, err := memory.Open(dbPath)
	if err != nil {
		fail("alert: memory store: %v", err)
		return
	}
	defer store.Close()
	n := alerts.NewNotifier(cfg.WhatsApp, reg.WhatsApp, store, dbPath)
	if n == nil {
		fail("alert: WhatsApp not ready or no allowed number (see status above)")
		return
	}
	how := fmt.Sprintf("template %q (no message from you in the last 24 h)", n.Template)
	if n.WindowOpen() {
		how = "free text (you messaged Fritz in the last 24 h)"
	}
	if err := n.Text(ctx, text); err != nil {
		fail("alert: %v", err)
		return
	}
	pass("alert: sent as %s — check your phone", how)
}

// checkSpark proves the Meta API key works (an expired or rotated
// key is the most common "Fritz went quiet") without printing it.
func checkSpark(ctx context.Context) {
	key := os.Getenv("MODEL_API_KEY")
	if key == "" {
		fail("spark: MODEL_API_KEY is not set")
		return
	}
	start := time.Now()
	reply, err := (&meta.Client{APIKey: key}).Chat(ctx, []meta.Message{{Role: "user", Content: "Reply with the single word OK."}})
	if err != nil {
		fail("spark: %v", err)
		return
	}
	pass("spark     Muse Spark answered %q in %s", strings.TrimSpace(reply), time.Since(start).Round(100*time.Millisecond))
}
