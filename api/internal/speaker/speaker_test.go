package speaker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeBT scripts bluetoothctl: info reports connected, connect
// succeeds or fails, and every call is recorded.
type fakeBT struct {
	connected bool
	connectOK bool
	calls     []string
}

func (f *fakeBT) run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	switch args[0] {
	case "info":
		return fmt.Sprintf("Device %s\n\tConnected: %s\n", args[1], map[bool]string{true: "yes", false: "no"}[f.connected]), nil
	case "connect":
		if f.connectOK {
			f.connected = true
			return "Attempting to connect\nConnection successful\n", nil
		}
		return "Attempting to connect\nFailed to connect: org.bluez.Error.Failed br-connection-refused\n", errors.New("exit status 1")
	}
	return "", errors.New("unexpected command")
}

func newWatchdog(f *fakeBT, logs *[]string) *Watchdog {
	return &Watchdog{
		MAC:  "00:11:22:33:44:55",
		run:  f.run,
		Logf: func(format string, args ...any) { *logs = append(*logs, fmt.Sprintf(format, args...)) },
	}
}

func TestConnectedDoesNothing(t *testing.T) {
	f := &fakeBT{connected: true}
	var logs []string
	w := newWatchdog(f, &logs)
	if !w.Check(context.Background()) {
		t.Fatal("Check = false, want true")
	}
	if len(f.calls) != 1 || len(logs) != 0 {
		t.Fatalf("calls %v logs %v, want one info call and no logs", f.calls, logs)
	}
}

func TestReconnects(t *testing.T) {
	f := &fakeBT{connectOK: true}
	var logs []string
	w := newWatchdog(f, &logs)
	if !w.Check(context.Background()) {
		t.Fatal("Check = false, want true after reconnect")
	}
	if got := f.calls[len(f.calls)-1]; got != "connect 00:11:22:33:44:55" {
		t.Fatalf("last call %q, want connect", got)
	}
	if len(logs) != 2 || !strings.Contains(logs[1], "reconnected") {
		t.Fatalf("logs %v, want disconnected + reconnected", logs)
	}
}

func TestFailedReconnectLogsOnce(t *testing.T) {
	f := &fakeBT{}
	var logs []string
	w := newWatchdog(f, &logs)
	for range 3 {
		if w.Check(context.Background()) {
			t.Fatal("Check = true, want false")
		}
	}
	// One "disconnected" line and one failure line; the next two
	// failures fall inside nagEvery and stay quiet.
	if len(logs) != 2 || !strings.Contains(logs[1], "br-connection-refused") {
		t.Fatalf("logs %v, want 2 lines naming the failure", logs)
	}
	f.connectOK = true
	w.Check(context.Background())
	if !strings.Contains(logs[len(logs)-1], "reconnected") {
		t.Fatalf("logs %v, want reconnected at the end", logs)
	}
}
