package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/francorobles/fritz/api/internal/integrations"
)

// replyBox is a fakeBox that can also send.
type replyBox struct {
	fakeBox
	sent []string
}

func (b *replyBox) PrepareReply(_ context.Context, id string) (integrations.Reply, error) {
	return integrations.Reply{To: "Sarah <sarah@example.com>", Name: "Sarah", Subject: "Re: Thursday?", ThreadID: "t-" + id}, nil
}

func (b *replyBox) SendReply(_ context.Context, r integrations.Reply, body string) error {
	b.sent = append(b.sent, r.ThreadID+"|"+body)
	return nil
}

// TestEmailReplyNeedsSpokenYes is the confirmation rule end to
// end: the tool only files a pending action naming the recipient;
// nothing is sent until a Go-checked yes, and then exactly once.
func TestEmailReplyNeedsSpokenYes(t *testing.T) {
	box := &replyBox{fakeBox: fakeBox{name: "gmail"}}
	store := newActionStore(t)
	tools := EmailReplyTool([]integrations.Mailbox{box}, store)
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want email_reply", len(tools))
	}
	out, err := tools[0].Run(context.Background(), json.RawMessage(`{"account":"gmail","id":"m1","body":"Thursday works!"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, _ := json.Marshal(out)
	if !strings.Contains(string(data), "needs_confirmation") || !strings.Contains(string(data), `reply to Sarah: \"Thursday works!\"`) {
		t.Errorf("tool result = %s", data)
	}
	if q := tools[0].Confirm(out); q != "Should I reply to Sarah: Thursday works? Just say yes and I'll send it." {
		t.Errorf("spoken question = %q", q)
	}
	if len(box.sent) != 0 {
		t.Fatal("email_reply sent before confirmation")
	}

	c := &Confirmer{Store: store, Exec: map[string]Executor{"email_reply": EmailReplyExecutor([]integrations.Mailbox{box})}}
	handled, reply := c.HandleTurn(context.Background(), "send it")
	if !handled || reply != "Sent. I replied to Sarah." {
		t.Errorf("HandleTurn = %v %q", handled, reply)
	}
	if len(box.sent) != 1 || box.sent[0] != "t-m1|Thursday works!" {
		t.Errorf("sent = %q, want one reply in thread t-m1", box.sent)
	}
	if handled, _ := c.HandleTurn(context.Background(), "yes"); handled || len(box.sent) != 1 {
		t.Error("a second yes must not send again")
	}
}

func TestEmailReplyRejectsBadInput(t *testing.T) {
	store := newActionStore(t)
	readOnly := &fakeBox{name: "workmail"}
	if tools := EmailReplyTool([]integrations.Mailbox{readOnly}, store); tools != nil {
		t.Error("no mailbox can send: email_reply must not be offered")
	}
	box := &replyBox{fakeBox: fakeBox{name: "gmail"}}
	tool := EmailReplyTool([]integrations.Mailbox{box, readOnly}, store)[0]
	for _, args := range []string{
		`{"account":"gmail","id":"m1","body":"   "}`,
		`{"account":"workmail","id":"m1","body":"hi"}`,
		`{"account":"gmail","id":"m1","body":"` + strings.Repeat("x", maxReplyRunes+1) + `"}`,
	} {
		if _, err := tool.Run(context.Background(), json.RawMessage(args)); err == nil {
			t.Errorf("Run(%.60s…) succeeded, want an error", args)
		}
	}
	if pending, _ := store.PendingList(10); len(pending) != 0 {
		t.Errorf("bad input filed %d pending actions", len(pending))
	}
}
