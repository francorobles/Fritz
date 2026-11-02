package alerts

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/francorobles/fritz/api/internal/integrations"
	"github.com/francorobles/fritz/api/internal/memory"
	"github.com/francorobles/fritz/api/internal/meta"
)

func openStore(t *testing.T) *memory.Store {
	t.Helper()
	st, err := memory.Open(filepath.Join(t.TempDir(), "fritz.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

type fakeWA struct {
	mu        sync.Mutex
	texts     []string
	templates [][]string
	images    []string // captions
	uploads   [][]byte
}

func (f *fakeWA) SendText(_ context.Context, _, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.texts = append(f.texts, body)
	return nil
}
func (f *fakeWA) SendTemplate(_ context.Context, _, _, _ string, params []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.templates = append(f.templates, params)
	return nil
}
func (f *fakeWA) UploadMedia(_ context.Context, jpeg []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads = append(f.uploads, jpeg)
	return "media-1", nil
}
func (f *fakeWA) SendImage(_ context.Context, _, _, caption string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.images = append(f.images, caption)
	return nil
}

func TestNotifierUsesTemplateUntilTheOwnerWrites(t *testing.T) {
	st := openStore(t)
	wa := &fakeWA{}
	n := &Notifier{WA: wa, To: "63917", Template: "fritz_alert", State: st, PhotoDir: t.TempDir()}
	ctx := context.Background()

	// Never written to: only a template can reach them.
	if err := n.Text(ctx, "Important email from Dana:\n\"Contract\""); err != nil {
		t.Fatal(err)
	}
	if len(wa.templates) != 1 || len(wa.texts) != 0 {
		t.Fatalf("closed window: templates=%v texts=%v", wa.templates, wa.texts)
	}
	if got := wa.templates[0]; len(got) != 1 || strings.ContainsAny(got[0], "\n\t") {
		t.Errorf("template param must be one line: %q", got)
	}
	// The owner replies: the window opens, alerts become plain text.
	n.Inbound(ctx)
	if err := n.Text(ctx, "Another one"); err != nil {
		t.Fatal(err)
	}
	if len(wa.texts) != 1 || wa.texts[0] != "Another one" {
		t.Errorf("open window: texts=%v", wa.texts)
	}
	// Alerts are remembered as Fritz's turns, for "what did it say?".
	turns, _ := st.RecentTurns(10)
	if len(turns) != 2 || turns[0].Role != "assistant" {
		t.Errorf("turns = %+v", turns)
	}
	// A day later the window has closed again.
	n.Now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	if n.WindowOpen() {
		t.Error("window still open after 25 h")
	}
}

func TestNotifierHoldsPhotoUntilReply(t *testing.T) {
	st := openStore(t)
	wa := &fakeWA{}
	n := &Notifier{WA: wa, To: "63917", Template: "fritz_alert", State: st, PhotoDir: t.TempDir()}
	ctx := context.Background()

	if err := n.Photo(ctx, []byte("JPEG"), "Someone just came to your desk."); err != nil {
		t.Fatal(err)
	}
	if len(wa.uploads) != 0 || len(wa.templates) != 1 || !strings.Contains(wa.templates[0][0], "send the photo") {
		t.Fatalf("closed window must send text and hold the photo: uploads=%d templates=%v", len(wa.uploads), wa.templates)
	}
	n.Inbound(ctx) // the owner replies
	if len(wa.uploads) != 1 || string(wa.uploads[0]) != "JPEG" || len(wa.images) != 1 {
		t.Fatalf("held photo not delivered on reply: uploads=%d images=%v", len(wa.uploads), wa.images)
	}
	n.Inbound(ctx) // and only once
	if len(wa.images) != 1 {
		t.Errorf("held photo sent twice")
	}
	// Window open: the photo goes straight out.
	if err := n.Photo(ctx, []byte("JPEG2"), "Again."); err != nil {
		t.Fatal(err)
	}
	if len(wa.images) != 2 || wa.images[1] != "Again." {
		t.Errorf("images = %v", wa.images)
	}
}

func TestTemplateParams(t *testing.T) {
	if p := templateParams("hello_world", "x"); p != nil {
		t.Errorf("hello_world takes no variables, got %v", p)
	}
	p := templateParams("fritz_alert", "a\n\nb\t   c"+strings.Repeat("x", 2000))
	if len(p) != 1 || strings.ContainsAny(p[0], "\n\t") || strings.Contains(p[0], "  ") || len([]rune(p[0])) > 900 {
		t.Errorf("param not sanitized: %q", p[0][:40])
	}
}

type fakeBox struct {
	name string
	list []integrations.Summary
}

func (b *fakeBox) Name() string { return b.name }
func (b *fakeBox) Search(context.Context, string, int) ([]integrations.Summary, error) {
	return b.list, nil
}
func (b *fakeBox) Read(context.Context, string) (integrations.Message, error) {
	return integrations.Message{}, errors.New("unused")
}

type fakeJudge struct {
	calls  int
	reply  string
	err    error
	prompt string
}

func (j *fakeJudge) Chat(_ context.Context, msgs []meta.Message) (string, error) {
	j.calls++
	j.prompt = msgs[len(msgs)-1].Content
	return j.reply, j.err
}

func TestMailWatcherJudgesNewMailOnce(t *testing.T) {
	st := openStore(t)
	now := time.Now()
	box := &fakeBox{name: "gmail", list: []integrations.Summary{
		{Account: "gmail", ID: "a", From: `"Starlink" <billing@starlink.com>`, Subject: "Payment failed", Date: now, Unread: true},
		{Account: "gmail", ID: "b", From: "news@shop.com", Subject: "Sale!", Date: now, Unread: true},
		{Account: "gmail", ID: "old", From: "x@y.z", Subject: "Backlog", Date: now.Add(-3 * time.Hour), Unread: true},
		{Account: "gmail", ID: "read", From: "x@y.z", Subject: "Already read", Date: now, Unread: false},
	}}
	judge := &fakeJudge{reply: "```json\n{\"important\":[{\"id\":\"a\",\"why\":\"your Starlink payment failed\"},{\"id\":\"zzz\",\"why\":\"invented\"}]}\n```"}
	var got []ImportantMail
	w := &MailWatcher{Boxes: []integrations.Mailbox{box}, Judge: judge, Seen: st, since: now.Add(-10 * time.Minute),
		OnImportant: func(_ context.Context, m ImportantMail) { got = append(got, m) }}
	if err := w.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Mail.ID != "a" || got[0].Sender() != "Starlink" {
		t.Fatalf("important = %+v", got)
	}
	if strings.Contains(judge.prompt, "Backlog") || strings.Contains(judge.prompt, "Already read") {
		t.Errorf("old or read mail was sent to the judge: %s", judge.prompt)
	}
	// Second poll: same mail, already judged — no model call, no alert.
	if err := w.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if judge.calls != 1 || len(got) != 1 {
		t.Errorf("re-judged or re-alerted: calls=%d alerts=%d", judge.calls, len(got))
	}
	want := `Important email from Starlink: "Payment failed" — your Starlink payment failed. Reply here if you want me to read it or answer it.`
	if EmailAlert(got[0]) != want {
		t.Errorf("EmailAlert = %q", EmailAlert(got[0]))
	}
}

func TestMailWatcherRetriesWhenJudgeFails(t *testing.T) {
	st := openStore(t)
	box := &fakeBox{name: "gmail", list: []integrations.Summary{{Account: "gmail", ID: "a", Subject: "Hi", Date: time.Now(), Unread: true}}}
	judge := &fakeJudge{err: errors.New("429")}
	w := &MailWatcher{Boxes: []integrations.Mailbox{box}, Judge: judge, Seen: st}
	if err := w.Check(context.Background()); err == nil {
		t.Fatal("judge error not reported")
	}
	judge.err, judge.reply = nil, `{"important":[]}`
	if err := w.Check(context.Background()); err != nil || judge.calls != 2 {
		t.Errorf("mail not retried after a failed judge: calls=%d err=%v", judge.calls, err)
	}
}

func TestDisplayName(t *testing.T) {
	cases := map[string]string{
		`"Dana Lee" <dana@example.com>`: "Dana Lee",
		"Dana Lee <dana@example.com>":   "Dana Lee",
		"dana@example.com":              "dana",
		"<dana@example.com>":            "dana",
		"Starlink":                      "Starlink",
	}
	for in, want := range cases {
		if got := DisplayName(in); got != want {
			t.Errorf("DisplayName(%q) = %q, want %q", in, got, want)
		}
	}
}

type fakePhotos struct {
	mu   sync.Mutex
	sent []string
	done chan struct{}
}

func (f *fakePhotos) Photo(_ context.Context, _ []byte, caption string) error {
	f.mu.Lock()
	f.sent = append(f.sent, caption)
	f.mu.Unlock()
	f.done <- struct{}{}
	return nil
}

type oneFrame struct{}

func (oneFrame) Latest() ([]byte, bool) { return []byte("JPEG"), true }

func TestSentry(t *testing.T) {
	st := openStore(t)
	photos := &fakePhotos{done: make(chan struct{}, 4)}
	clock := time.Now()
	s := &Sentry{Flags: st, Notify: photos, Frames: oneFrame{}, Settle: time.Millisecond,
		Now: func() time.Time { return clock }}
	ctx := context.Background()

	if s.OnPresence(ctx, true) {
		t.Fatal("alerted while disarmed")
	}
	if err := SetSentry(st, true); err != nil {
		t.Fatal(err)
	}
	// The owner is still at the desk right after arming.
	s.OnPresence(ctx, false)
	clock = clock.Add(10 * time.Second)
	if s.OnPresence(ctx, true) {
		t.Fatal("alerted inside the grace period")
	}
	// Leaves; someone arrives a minute later.
	s.OnPresence(ctx, false)
	clock = clock.Add(time.Minute)
	if !s.OnPresence(ctx, true) {
		t.Fatal("no alert for an arrival while armed")
	}
	<-photos.done
	if !strings.Contains(photos.sent[0], "Someone just came to your desk") {
		t.Errorf("caption = %q", photos.sent[0])
	}
	// Flicker: gone for 2 s is not a new arrival.
	s.OnPresence(ctx, false)
	clock = clock.Add(2 * time.Second)
	if s.OnPresence(ctx, true) {
		t.Error("tracking flicker triggered an alert")
	}
	// A real new arrival inside the cooldown is still quiet.
	s.OnPresence(ctx, false)
	clock = clock.Add(30 * time.Second)
	if s.OnPresence(ctx, true) {
		t.Error("alerted inside the cooldown")
	}
	// Disarmed: nothing.
	_ = SetSentry(st, false)
	s.OnPresence(ctx, false)
	clock = clock.Add(5 * time.Minute)
	if s.OnPresence(ctx, true) {
		t.Error("alerted after sentry was turned off")
	}
}

func TestFocusHoldsWhatCanWait(t *testing.T) {
	st := openStore(t)
	clock := time.Now()
	f := &Focus{State: st, Now: func() time.Time { return clock }}
	normal := ImportantMail{Mail: integrations.Summary{From: "Dana <d@x.com>", Subject: "Q4 update"}, Why: "project update"}
	urgent := ImportantMail{Mail: integrations.Summary{From: "Bank", Subject: "Payment failed"}, Urgent: true}
	ruled := ImportantMail{Mail: integrations.Summary{From: "Jira", Subject: "Sprint notes"}, Hold: true}

	// Not focusing: only the user's rule holds anything.
	if hold, _ := f.ShouldHold(normal); hold {
		t.Error("held without focus or rule")
	}
	if hold, why := f.ShouldHold(ruled); !hold || !strings.Contains(why, "rule") {
		t.Errorf("rule not applied: %v %q", hold, why)
	}
	// Focusing: what can wait is held; urgent breaks through.
	if _, err := f.Start(30 * time.Minute); err != nil {
		t.Fatal(err)
	}
	if hold, why := f.ShouldHold(normal); !hold || why != "focus mode" {
		t.Errorf("focus did not hold: %v %q", hold, why)
	}
	if hold, _ := f.ShouldHold(urgent); hold {
		t.Error("urgent mail held during focus")
	}
	_ = f.Hold(HeldMail(normal))
	if got := f.Held(); len(got) != 1 || !strings.Contains(got[0].Text, `Email from Dana: "Q4 update" (project update)`) {
		t.Fatalf("held = %+v", got)
	}
	// The timer runs out: Expired until wrapped up, then the
	// digest is taken once.
	clock = clock.Add(31 * time.Minute)
	if _, on := f.Active(); on || !f.Expired() {
		t.Fatal("focus should have expired")
	}
	_ = f.Stop()
	if f.Expired() {
		t.Error("still expired after Stop")
	}
	if d := Digest(f.TakeHeld()); !strings.Contains(d, "Q4 update") {
		t.Errorf("digest = %q", d)
	}
	if len(f.TakeHeld()) != 0 {
		t.Error("digest repeated")
	}
}

func TestRulesReachTheJudge(t *testing.T) {
	st := openStore(t)
	box := &fakeBox{name: "gmail", list: []integrations.Summary{{Account: "gmail", ID: "a", Subject: "Sprint notes", Date: time.Now(), Unread: true}}}
	judge := &fakeJudge{reply: `{"important":[{"id":"a","why":"project update","urgent":false,"hold":true}]}`}
	var got []ImportantMail
	w := &MailWatcher{Boxes: []integrations.Mailbox{box}, Judge: judge, Seen: st,
		Rules: func() []string {
			return Rules([]string{"User likes tea", RulePrefix + "hold project updates unless there's a deadline"})
		},
		OnImportant: func(_ context.Context, m ImportantMail) { got = append(got, m) }}
	var system string
	w.Judge = judgeFunc(func(msgs []meta.Message) (string, error) {
		system = msgs[0].Content
		return judge.reply, nil
	})
	if err := w.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(system, "- hold project updates unless there's a deadline") || strings.Contains(system, "likes tea") {
		t.Errorf("rules section wrong: %s", system[strings.Index(system, "interruption rules (follow"):])
	}
	if len(got) != 1 || !got[0].Hold || got[0].Urgent {
		t.Errorf("flags not parsed: %+v", got)
	}
}

type judgeFunc func([]meta.Message) (string, error)

func (f judgeFunc) Chat(_ context.Context, msgs []meta.Message) (string, error) { return f(msgs) }

func TestProgressRoundTrip(t *testing.T) {
	st := openStore(t)
	if _, ok := LoadProgress(st); ok {
		t.Fatal("progress before any was saved")
	}
	_ = SaveProgress(st, Progress{Task: "pricing section", Next: "discount table", At: time.Now()})
	p, ok := LoadProgress(st)
	if !ok || p.Task != "pricing section" || p.Next != "discount table" {
		t.Errorf("progress = %+v, %v", p, ok)
	}
}
