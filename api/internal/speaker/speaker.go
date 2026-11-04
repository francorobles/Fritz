// Package speaker keeps Fritz's Bluetooth speaker (the Echo Dot)
// connected. The Echo drops the A2DP link on its own (when someone
// uses Alexa, another phone grabs it, or the radio link is lost), and
// nothing on the Pi brings it back: bluetoothd only retries for a
// short while, and the Echo refuses connections for a bit right after
// a drop. So the voice loop runs a Watchdog that checks the link every
// 30 s and reconnects. While the Echo is gone, PipeWire moves playback
// to the next available sink (the Pi's built-in audio), so Fritz keeps
// talking instead of going silent.
package speaker

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// Watchdog polls `bluetoothctl info` and runs `bluetoothctl connect`
// whenever the speaker is not connected.
type Watchdog struct {
	MAC      string        // speaker address, from FRITZ_SPEAKER_BT
	Interval time.Duration // time between checks; 0 means 30 s
	// Logf reports state changes. Nil means no logging.
	Logf func(format string, args ...any)

	// run executes bluetoothctl; tests replace it.
	run func(ctx context.Context, args ...string) (string, error)

	down       bool      // last check found the speaker disconnected
	lastNagged time.Time // last time a failed reconnect was logged
}

// nagEvery limits how often a speaker that stays away is logged, so a
// long outage doesn't drown the voice loop's output.
const nagEvery = 5 * time.Minute

// connectTimeout bounds one `bluetoothctl connect`; a page timeout to
// an unreachable Echo takes ~5–15 s.
const connectTimeout = 25 * time.Second

// Run checks the link right away and then every Interval until ctx
// ends.
func (w *Watchdog) Run(ctx context.Context) {
	interval := w.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		w.Check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Check makes one pass: it reports whether the speaker is connected
// after the pass, reconnecting first if it wasn't.
func (w *Watchdog) Check(ctx context.Context) bool {
	if w.connected(ctx) {
		if w.down {
			w.logf("speaker: %s connected again", w.MAC)
		}
		w.down = false
		return true
	}
	if !w.down {
		w.logf("speaker: %s disconnected — reconnecting (audio falls back to the Pi's built-in output meanwhile)", w.MAC)
		w.down = true
		w.lastNagged = time.Time{}
	}

	cctx, cancel := context.WithTimeout(ctx, connectTimeout)
	out, err := w.bluetoothctl(cctx, "connect", w.MAC)
	cancel()
	if err == nil && strings.Contains(out, "Connection successful") {
		w.logf("speaker: %s reconnected", w.MAC)
		w.down = false
		return true
	}
	if ctx.Err() == nil && time.Since(w.lastNagged) >= nagEvery {
		w.lastNagged = time.Now()
		reason := lastLine(out)
		if reason == "" && err != nil {
			reason = err.Error()
		}
		// A refused or unanswered connect means the Echo isn't
		// accepting this Pi right now; asking it out loud makes it
		// connect from its side.
		w.logf("speaker: reconnect failed (%s) — retrying every %s; if it persists, say \"Alexa, connect\" to the Echo", reason, w.intervalOrDefault())
	}
	return false
}

func (w *Watchdog) connected(ctx context.Context) bool {
	out, err := w.bluetoothctl(ctx, "info", w.MAC)
	return err == nil && strings.Contains(out, "Connected: yes")
}

func (w *Watchdog) bluetoothctl(ctx context.Context, args ...string) (string, error) {
	if w.run != nil {
		return w.run(ctx, args...)
	}
	out, err := exec.CommandContext(ctx, "bluetoothctl", args...).CombinedOutput()
	return string(out), err
}

func (w *Watchdog) intervalOrDefault() time.Duration {
	if w.Interval <= 0 {
		return 30 * time.Second
	}
	return w.Interval
}

func (w *Watchdog) logf(format string, args ...any) {
	if w.Logf != nil {
		w.Logf(format, args...)
	}
}

// lastLine returns the last non-empty line of bluetoothctl output,
// which is where it prints the failure reason.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
