package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/francorobles/fritz/api/internal/integrations"
	"github.com/francorobles/fritz/api/internal/meta"
)

// fakeSpark replays scripted rounds and records what it was sent.
type fakeSpark struct {
	rounds []meta.StreamResult
	sent   [][]meta.Message
}

func (f *fakeSpark) ChatStreamTools(_ context.Context, messages []meta.Message, _ []meta.Tool, emit func(string)) (meta.StreamResult, error) {
	f.sent = append(f.sent, append([]meta.Message(nil), messages...))
	if len(f.sent) > len(f.rounds) {
		return meta.StreamResult{}, errors.New("fakeSpark: no more rounds")
	}
	res := f.rounds[len(f.sent)-1]
	if res.Text != "" {
		emit(res.Text)
	}
	return res, nil
}

func call(id, name, args string) meta.ToolCall {
	return meta.ToolCall{ID: id, Type: "function", Function: meta.FunctionCall{Name: name, Arguments: args}}
}

// fakeBox is an in-memory Mailbox.
type fakeBox struct {
	name    string
	mail    []integrations.Message
	queries []string
	err     error
}

func (b *fakeBox) Name() string { return b.name }

func (b *fakeBox) Search(_ context.Context, query string, max int) ([]integrations.Summary, error) {
	b.queries = append(b.queries, query)
	if b.err != nil {
		return nil, b.err
	}
	var out []integrations.Summary
	for _, m := range b.mail {
		if len(out) < max {
			out = append(out, m.Summary)
		}
	}
	return out, nil
}

func (b *fakeBox) Read(_ context.Context, id string) (integrations.Message, error) {
	for _, m := range b.mail {
		if m.ID == id {
			return m, nil
		}
	}
	return integrations.Message{}, errors.New("not found")
}

func msg(account, id, from, subject string, at time.Time, body string) integrations.Message {
	return integrations.Message{
		Summary: integrations.Summary{Account: account, ID: id, From: from, Subject: subject, Date: at, Unread: true},
		Body:    body,
	}
}

// TestRunSearchThenAnswer is the core loop: Spark says it will
// check, calls email_search, gets the inbox back as a tool message
// tied to its call, and answers. The spoken text is one reply with
// the rounds separated.
func TestRunSearchThenAnswer(t *testing.T) {
	now := time.Now()
	box := &fakeBox{name: "gmail", mail: []integrations.Message{
		msg("gmail", "m1", "Sarah <sarah@example.com>", "Thursday?", now, "Does Thursday work?"),
	}}
	spark := &fakeSpark{rounds: []meta.StreamResult{
		{Text: "Let me check.", ToolCalls: []meta.ToolCall{call("c1", "email_search", `{"query":"is:unread"}`)}},
		{Text: "One new email, from Sarah."},
	}}
	var used []string
	a := &Agent{Chat: spark, Tools: MailTools([]integrations.Mailbox{box}), OnTool: func(name, _ string) { used = append(used, name) }}

	var spoken strings.Builder
	text, err := a.Run(context.Background(), []meta.Message{{Role: "user", Content: "any new email?"}},
		func(d string) { spoken.WriteString(d) })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if text != "Let me check. One new email, from Sarah." || spoken.String() != text {
		t.Errorf("text = %q, spoken %q", text, spoken.String())
	}
	if len(used) != 1 || used[0] != "email_search" || len(box.queries) != 1 || box.queries[0] != "is:unread" {
		t.Errorf("tools used %v with queries %v", used, box.queries)
	}
	second := spark.sent[1]
	if len(second) != 3 || second[1].Role != "assistant" || len(second[1].ToolCalls) != 1 {
		t.Fatalf("round 2 history = %+v, want user, assistant(tool_calls), tool", second)
	}
	tool := second[2]
	if tool.Role != "tool" || tool.ToolCallID != "c1" || !strings.Contains(tool.Content, "Thursday?") {
		t.Errorf("tool message = %+v", tool)
	}
}

// TestRunReportsToolErrorsToModel: a bad call is answered, not
// fatal — the model hears what went wrong and can say so.
func TestRunReportsToolErrorsToModel(t *testing.T) {
	spark := &fakeSpark{rounds: []meta.StreamResult{
		{ToolCalls: []meta.ToolCall{
			call("c1", "email_read", `{"account":"gmail"`),        // broken JSON
			call("c2", "email_read", `{"account":"gmail","x":1}`), // invented field
			call("c3", "launch_rockets", `{}`),                    // no such tool
			call("c4", "email_read", `{"account":"yahoo","id":"1"}`),
		}},
		{Text: "Sorry, I couldn't open that email."},
	}}
	a := &Agent{Chat: spark, Tools: MailTools([]integrations.Mailbox{&fakeBox{name: "gmail"}})}
	if _, err := a.Run(context.Background(), nil, func(string) {}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	results := spark.sent[1][1:]
	if len(results) != 4 {
		t.Fatalf("got %d tool results, want 4", len(results))
	}
	for i, want := range []string{"not valid JSON", "unknown field", "no tool named", `no connected account`} {
		if !strings.Contains(results[i].Content, `"error"`) || !strings.Contains(results[i].Content, want) {
			t.Errorf("result %d = %s, want an error mentioning %q", i, results[i].Content, want)
		}
	}
}

// TestRunStopsRunawayTools: a model that never stops calling tools
// is told to answer, then cut off.
func TestRunStopsRunawayTools(t *testing.T) {
	loop := meta.StreamResult{ToolCalls: []meta.ToolCall{call("c", "email_search", `{}`)}}
	spark := &fakeSpark{rounds: []meta.StreamResult{loop, loop, loop, loop}}
	a := &Agent{Chat: spark, MaxRounds: 2, Tools: MailTools([]integrations.Mailbox{&fakeBox{name: "gmail"}})}
	_, err := a.Run(context.Background(), nil, func(string) {})
	if err == nil {
		t.Fatal("want an error after the round limit")
	}
	if len(spark.sent) != 3 {
		t.Errorf("Spark called %d times, want 3 (2 tool rounds + the final ask)", len(spark.sent))
	}
	last := spark.sent[2]
	if !strings.Contains(last[len(last)-1].Content, "tool limit reached") {
		t.Errorf("final round was not told to answer: %s", last[len(last)-1].Content)
	}
}

func TestMailSearchMergesNewestFirst(t *testing.T) {
	now := time.Now()
	gmail := &fakeBox{name: "gmail", mail: []integrations.Message{
		msg("gmail", "g1", "A", "old", now.Add(-2*time.Hour), ""),
	}}
	work := &fakeBox{name: "workmail", mail: []integrations.Message{
		msg("workmail", "w1", "B", "new", now, ""),
	}}
	broken := &fakeBox{name: "outlook", err: errors.New("token expired")}
	out, err := searchMail(context.Background(), []integrations.Mailbox{gmail, work, broken}, "", 0)
	if err != nil {
		t.Fatalf("searchMail: %v", err)
	}
	data, _ := json.Marshal(out)
	s := string(data)
	if strings.Index(s, `"new"`) > strings.Index(s, `"old"`) || !strings.Contains(s, "token expired") {
		t.Errorf("result = %s, want newest first and the broken account reported", s)
	}
	if _, err := searchMail(context.Background(), []integrations.Mailbox{broken}, "", 5); err == nil {
		t.Error("all accounts failing must be an error")
	}
}

func TestMailReadTruncatesBody(t *testing.T) {
	long := strings.Repeat("é", maxBodyRunes+50)
	box := &fakeBox{name: "gmail", mail: []integrations.Message{msg("gmail", "m1", "A", "s", time.Now(), long)}}
	out, err := readMail(context.Background(), []integrations.Mailbox{box}, "gmail", "m1")
	if err != nil {
		t.Fatalf("readMail: %v", err)
	}
	data, _ := json.Marshal(out)
	var got struct {
		ID        string `json:"id"`
		Body      string `json:"body"`
		Truncated bool   `json:"truncated"`
	}
	_ = json.Unmarshal(data, &got)
	if got.ID != "m1" || len([]rune(got.Body)) != maxBodyRunes || !got.Truncated {
		t.Errorf("read = id %q, %d runes, truncated %v", got.ID, len([]rune(got.Body)), got.Truncated)
	}
}

func TestNoMailboxesNoTools(t *testing.T) {
	if tools := MailTools(nil); tools != nil {
		t.Errorf("MailTools(nil) = %d tools, want none", len(tools))
	}
}
