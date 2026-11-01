// Command mailcheck is the integrations smoke test: it loads
// integrations.json, reports every provider's state, and does a
// live read against each configured inbox (newest few messages)
// and Jira (your open issues). Like voicetest for accounts.
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
	"bufio"
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
	jiraKey := flag.String("jira", "", "print one Jira issue in full (description + comments): -jira FRITZ-7")
	jiraCreate := flag.String("jira-create", "", `create a Jira issue: -jira-create "PROJ|Task|Summary|Description" — asks for a typed YES first`)
	jiraComment := flag.String("jira-comment", "", `comment on an issue: -jira-comment "FRITZ-7|Looks good" — asks for a typed YES first`)
	jiraTransition := flag.String("jira-transition", "", `move an issue: -jira-transition "FRITZ-7|Done" — asks for a typed YES first`)
	triage := flag.Bool("triage", false, "ask Muse Spark which recent unread mail is important (dry run, sends nothing)")
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

	if reg.Jira != nil {
		issues, err := reg.Jira.AssignedToMe(ctx, 5)
		if err != nil {
			fail("jira assigned: %v", err)
		} else {
			fmt.Printf("\n[jira] %d open issues assigned to you:\n", len(issues))
			for _, issue := range issues {
				fmt.Printf("  %s  [%s] %s\n", issue.Key, issue.Status, issue.Summary)
			}
		}
	}

	if *jiraKey != "" {
		if reg.Jira == nil {
			fail("jira: not ready — cannot read %s", *jiraKey)
		} else if detail, err := reg.Jira.GetIssue(ctx, *jiraKey); err != nil {
			fail("jira read %s: %v", *jiraKey, err)
		} else {
			printJiraDetail(detail)
		}
	}

	if *jiraCreate != "" {
		createJiraIssue(ctx, reg, *jiraCreate)
	}

	if *jiraComment != "" {
		commentJiraIssue(ctx, reg, *jiraComment)
	}

	if *jiraTransition != "" {
		transitionJiraIssue(ctx, reg, *jiraTransition)
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

	if *triage {
		runTriage(ctx, reg)
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

func printJiraDetail(d integrations.IssueDetail) {
	fmt.Printf("\n[%s] %s\n", d.Key, d.Summary)
	fmt.Printf("  status %s · priority %s · assignee %s · updated %s\n",
		d.Status, d.Priority, d.Assignee, d.Updated.Format("Jan 2 15:04"))
	if d.Description != "" {
		fmt.Printf("\n%s\n", d.Description)
	}
	for _, c := range d.Comments {
		fmt.Printf("\n— %s (%s):\n%s\n", c.Author, c.Created.Format("Jan 2 15:04"), c.Body)
	}
}

// confirmYes asks for an explicit typed YES — the same
// confirmation rule the agent enforces for actions (Go code
// checks the yes, never a model's say-so).
func confirmYes() bool {
	fmt.Print("Type YES to proceed (anything else aborts): ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if !strings.EqualFold(strings.TrimSpace(line), "yes") {
		fmt.Println("aborted — nothing changed")
		return false
	}
	return true
}

// createJiraIssue files an issue only after confirmation.
func createJiraIssue(ctx context.Context, reg *integrations.Registry, spec string) {
	parts := strings.SplitN(spec, "|", 4)
	if len(parts) < 3 {
		fail(`jira-create: want "PROJ|Type|Summary[|Description]"`)
		return
	}
	if reg.Jira == nil {
		fail("jira: not ready — cannot create issues")
		return
	}
	project := strings.TrimSpace(parts[0])
	issueType := strings.TrimSpace(parts[1])
	summary := strings.TrimSpace(parts[2])
	description := ""
	if len(parts) == 4 {
		description = strings.TrimSpace(parts[3])
	}
	fmt.Printf("\nAbout to create in Jira:\n  Project: %s\n  Type:    %s\n  Summary: %s\n", project, issueType, summary)
	if description != "" {
		fmt.Printf("  Description: %s\n", description)
	}
	if !confirmYes() {
		return
	}
	key, err := reg.Jira.CreateIssue(ctx, project, issueType, summary, description)
	if err != nil {
		fail("jira create: %v", err)
		return
	}
	pass("jira: created %s — %s", key, summary)
}

// commentJiraIssue comments only after confirmation.
func commentJiraIssue(ctx context.Context, reg *integrations.Registry, spec string) {
	parts := strings.SplitN(spec, "|", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		fail(`jira-comment: want "KEY|comment text"`)
		return
	}
	if reg.Jira == nil {
		fail("jira: not ready — cannot comment")
		return
	}
	key := strings.TrimSpace(parts[0])
	body := strings.TrimSpace(parts[1])
	fmt.Printf("\nAbout to comment on %s:\n  %s\n", key, body)
	if !confirmYes() {
		return
	}
	id, err := reg.Jira.AddComment(ctx, key, body)
	if err != nil {
		fail("jira comment: %v", err)
		return
	}
	pass("jira: commented on %s (comment %s)", key, id)
}

// transitionJiraIssue moves an issue only after confirmation.
// The preview resolves the named step first, so you confirm
// the exact transition that will fire.
func transitionJiraIssue(ctx context.Context, reg *integrations.Registry, spec string) {
	parts := strings.SplitN(spec, "|", 2)
	if len(parts) < 2 {
		fail(`jira-transition: want "KEY|Done"`)
		return
	}
	if reg.Jira == nil {
		fail("jira: not ready — cannot transition issues")
		return
	}
	key := strings.TrimSpace(parts[0])
	name := strings.TrimSpace(parts[1])
	transitions, err := reg.Jira.Transitions(ctx, key)
	if err != nil {
		fail("jira transitions %s: %v", key, err)
		return
	}
	t, ok := integrations.FindTransition(transitions, name)
	if !ok {
		available := make([]string, len(transitions))
		for i, tr := range transitions {
			available[i] = tr.Name + " → " + tr.To
		}
		fail("jira: %s cannot %q from here (available: %s)", key, name, strings.Join(available, ", "))
		return
	}
	fmt.Printf("\nAbout to move %s via %q → %s\n", key, t.Name, t.To)
	if !confirmYes() {
		return
	}
	if _, err := reg.Jira.Transition(ctx, key, name); err != nil {
		fail("jira transition: %v", err)
		return
	}
	pass("jira: %s moved → %s", key, t.To)
}

// neverSeen makes -triage judge every unread message without
// recording anything, so it never hides mail from the real watcher.
type neverSeen struct{}

func (neverSeen) Alerted(string) (bool, error)                     { return false, nil }
func (neverSeen) MarkAlerted(string, string, string) (bool, error) { return true, nil }

func runTriage(ctx context.Context, reg *integrations.Registry) {
	key := os.Getenv("MODEL_API_KEY")
	if key == "" {
		fail("triage: MODEL_API_KEY is not set")
		return
	}
	found := 0
	w := &alerts.MailWatcher{
		Boxes: reg.Mailboxes, Judge: &meta.Client{APIKey: key}, Seen: neverSeen{},
		Logf: func(msg string) {},
		OnImportant: func(_ context.Context, m alerts.ImportantMail) {
			found++
			fmt.Printf("  IMPORTANT  %s\n", alerts.EmailAlert(m))
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
