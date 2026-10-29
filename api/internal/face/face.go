// Package face is the Go client for the Fritz face protocol: one ASCII
// command per UDP datagram to the C++ face in iot/ (127.0.0.1:7070 by
// default). It is fire-and-forget — the face only obeys commands and
// never replies — so send failures are logged once and never returned;
// a dead face must not break the agent.
package face

import (
	"fmt"
	"log"
	"math"
	"net"
	"sync"
)

// DefaultAddr is where the face listens when nothing overrides it.
const DefaultAddr = "127.0.0.1:7070"

// Client sends face commands over a single UDP connection. Safe for
// concurrent use; datagrams may interleave but never corrupt.
type Client struct {
	conn     *net.UDPConn
	warnOnce sync.Once
}

// New dials the face at addr (e.g. "127.0.0.1:7070"). UDP dialing does
// no I/O, so this succeeds even when the face is not running yet.
func New(addr string) (*Client, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("face: bad address %q: %w", addr, err)
	}
	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return nil, fmt.Errorf("face: dial %q: %w", addr, err)
	}
	return &Client{conn: conn}, nil
}

// Close releases the UDP connection.
func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) send(command string) {
	if _, err := c.conn.Write([]byte(command)); err != nil {
		c.warnOnce.Do(func() {
			log.Printf("face: send failed (further errors suppressed): %v", err)
		})
	}
}

// validEmotions mirrors the nine names the C++ face accepts.
var validEmotions = map[string]bool{
	"normal": true, "thinking": true, "surprised": true, "angry": true,
	"skeptical": true, "focused": true, "scared": true, "happy": true,
	"tired": true,
}

// Emotion pins an expression by name and leaves standby.
func (c *Client) Emotion(name string) {
	if !validEmotions[name] {
		c.warnOnce.Do(func() {
			log.Printf("face: unknown emotion %q dropped", name)
		})
		return
	}
	c.send("emotion " + name)
}

// Standby returns the face to its idle Normal/Thinking cycle.
func (c *Client) Standby() { c.send("standby") }

// Gaze looks toward (x, y), each in [-1, 1] (+x = screen right,
// +y = down). Out-of-range values are clamped by the face; NaN and
// infinities are dropped here. The override expires 1.5 s after the
// last Gaze call, so callers refresh it while tracking.
func (c *Client) Gaze(x, y float64) {
	if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
		return
	}
	c.send(fmt.Sprintf("gaze %g %g", x, y))
}

// GazeOff drops the gaze override immediately.
func (c *Client) GazeOff() { c.send("gaze off") }

// Speaking toggles the subtle pulse shown while TTS audio plays.
func (c *Client) Speaking(on bool) {
	if on {
		c.send("speaking on")
	} else {
		c.send("speaking off")
	}
}

// Blink triggers one blink now (e.g. acknowledging the wake phrase).
func (c *Client) Blink() { c.send("blink") }
