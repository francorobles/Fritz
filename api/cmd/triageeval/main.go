// Command triageeval measures how well Fritz decides what may
// interrupt you. It runs a hand-labelled set of emails
// (interruptions.json: a focus session plus the rule "hold project
// updates unless there's a deadline") through the real Muse Spark
// email check — the same MailWatcher and prompt voicecheck uses —
// and scores each decision: interrupt now, hold for the digest, or
// ignore. Nothing is read from or sent to a real mailbox.
//
//	MODEL_API_KEY=... go run ./cmd/triageeval            # one run
//	MODEL_API_KEY=... go run ./cmd/triageeval -runs 3    # consistency
//
// Each run costs a few small Spark calls.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/francorobles/fritz/api/internal/alerts"
	"github.com/francorobles/fritz/api/internal/integrations"
	"github.com/francorobles/fritz/api/internal/meta"
)

//go:embed interruptions.json
var defaultSet []byte

type testSet struct {
	Scenario     string   `json:"scenario"`
	FocusMinutes int      `json:"focus_minutes"`
	Rules        []string `json:"rules"`
	About        []string `json:"about"`
	Emails       []struct {
		ID      string `json:"id"`
		From    string `json:"from"`
		Subject string `json:"subject"`
		Snippet string `json:"snippet"`
		Expect  string `json:"expect"` // interrupt | hold | ignore
	} `json:"emails"`
}

// batch is a mailbox holding one poll's worth of new mail. Real polls
// see a few emails at a time, so the set is judged in small batches.
type batch struct{ mail []integrations.Summary }

func (b *batch) Name() string { return "eval" }
func (b *batch) Search(context.Context, string, int) ([]integrations.Summary, error) {
	return b.mail, nil
}
func (b *batch) Read(context.Context, string) (integrations.Message, error) {
	return integrations.Message{}, errors.New("not used")
}

// neverSeen judges everything every run.
type neverSeen struct{}

func (neverSeen) Alerted(string) (bool, error)                     { return false, nil }
func (neverSeen) MarkAlerted(string, string, string) (bool, error) { return true, nil }

// decide is what voicecheck does with a judged email during focus
// (alerts.Focus.ShouldHold): rule-held or not urgent → hold.
func decide(m alerts.ImportantMail) string {
	if m.Hold || !m.Urgent {
		return "hold"
	}
	return "interrupt"
}

func main() {
	path := flag.String("set", "", "test set JSON (default: the embedded interruptions.json)")
	runs := flag.Int("runs", 1, "how many times to run the whole set")
	size := flag.Int("batch", 6, "emails per simulated poll")
	flag.Parse()

	data := defaultSet
	if *path != "" {
		var err error
		if data, err = os.ReadFile(*path); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}
	var set testSet
	if err := json.Unmarshal(data, &set); err != nil {
		fmt.Println("test set:", err)
		os.Exit(1)
	}
	key := os.Getenv("MODEL_API_KEY")
	if key == "" {
		fmt.Println("MODEL_API_KEY is not set")
		os.Exit(1)
	}
	judge := &meta.Client{APIKey: key}

	fmt.Printf("%s\nRules: %s\n%d emails, %d run(s)\n\n", set.Scenario, strings.Join(set.Rules, "; "), len(set.Emails), *runs)
	labels := []string{"interrupt", "hold", "ignore"}
	confusion := map[[2]string]int{} // [expected, got]
	correct, total := 0, 0
	for run := 1; run <= *runs; run++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		focusEnd := time.Now().Add(time.Duration(set.FocusMinutes) * time.Minute)
		got := map[string]string{}
		box := &batch{}
		w := &alerts.MailWatcher{
			Boxes: []integrations.Mailbox{box}, Judge: judge, Seen: neverSeen{},
			About:     func() string { return "- " + strings.Join(set.About, "\n- ") },
			Rules:     func() []string { return set.Rules },
			FocusEnds: func() time.Time { return focusEnd },
			OnImportant: func(_ context.Context, m alerts.ImportantMail) {
				got[m.Mail.ID] = decide(m)
			},
		}
		// Interleave so every batch mixes kinds of mail, as a real
		// inbox does.
		for start := 0; start < *size; start++ {
			box.mail = nil
			for i := start; i < len(set.Emails); i += *size {
				e := set.Emails[i]
				box.mail = append(box.mail, integrations.Summary{Account: "eval", ID: e.ID, From: e.From, Subject: e.Subject,
					Snippet: e.Snippet, Date: time.Now().Add(-time.Duration(2+i) * time.Minute), Unread: true})
			}
			if len(box.mail) == 0 {
				continue
			}
			if err := w.Check(ctx); err != nil {
				fmt.Println("judge error:", err)
				os.Exit(1)
			}
		}
		cancel()

		fmt.Printf("Run %d\n", run)
		for _, e := range set.Emails {
			g := got[e.ID]
			if g == "" {
				g = "ignore"
			}
			confusion[[2]string{e.Expect, g}]++
			total++
			mark := "ok  "
			if g == e.Expect {
				correct++
			} else {
				mark = "MISS"
			}
			fmt.Printf("  %s  expected %-9s got %-9s %s\n", mark, e.Expect, g, e.Subject)
		}
		fmt.Println()
	}

	fmt.Printf("Score: %d/%d correct (%.0f%%)\n\n", correct, total, 100*float64(correct)/float64(total))
	fmt.Printf("%-18s", "expected \\ got")
	for _, l := range labels {
		fmt.Printf("%10s", l)
	}
	fmt.Println()
	for _, want := range labels {
		fmt.Printf("%-18s", want)
		for _, g := range labels {
			fmt.Printf("%10d", confusion[[2]string{want, g}])
		}
		fmt.Println()
	}
	missed := confusion[[2]string{"interrupt", "hold"}] + confusion[[2]string{"interrupt", "ignore"}]
	falseAlarms := confusion[[2]string{"hold", "interrupt"}] + confusion[[2]string{"ignore", "interrupt"}]
	fmt.Printf("\nMissed interruptions (urgent mail kept back): %d\n", missed)
	fmt.Printf("False interruptions (broke focus for nothing urgent): %d\n", falseAlarms)
}
