package face

import (
	"math"
	"net"
	"testing"
	"time"
)

// fakeFace is a UDP listener standing in for the C++ face.
type fakeFace struct {
	conn *net.UDPConn
}

func newFakeFace(t *testing.T) (*fakeFace, string) {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return &fakeFace{conn: conn}, conn.LocalAddr().String()
}

func (f *fakeFace) next(t *testing.T) string {
	t.Helper()
	if err := f.conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 512)
	n, _, err := f.conn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("reading datagram: %v", err)
	}
	return string(buf[:n])
}

func (f *fakeFace) expectNone(t *testing.T) {
	t.Helper()
	if err := f.conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 512)
	if n, _, err := f.conn.ReadFromUDP(buf); err == nil {
		t.Fatalf("expected no datagram, got %q", buf[:n])
	}
}

func newClient(t *testing.T, addr string) *Client {
	t.Helper()
	client, err := New(addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func TestClientSendsProtocolCommands(t *testing.T) {
	face, addr := newFakeFace(t)
	client := newClient(t, addr)

	client.Emotion("happy")
	if got := face.next(t); got != "emotion happy" {
		t.Errorf("Emotion: got %q", got)
	}
	client.Standby()
	if got := face.next(t); got != "standby" {
		t.Errorf("Standby: got %q", got)
	}
	client.Gaze(-0.5, 0.25)
	if got := face.next(t); got != "gaze -0.5 0.25" {
		t.Errorf("Gaze: got %q", got)
	}
	client.GazeOff()
	if got := face.next(t); got != "gaze off" {
		t.Errorf("GazeOff: got %q", got)
	}
	client.Speaking(true)
	if got := face.next(t); got != "speaking on" {
		t.Errorf("Speaking(true): got %q", got)
	}
	client.Speaking(false)
	if got := face.next(t); got != "speaking off" {
		t.Errorf("Speaking(false): got %q", got)
	}
	client.Blink()
	if got := face.next(t); got != "blink" {
		t.Errorf("Blink: got %q", got)
	}
}

func TestClientDropsInvalidInput(t *testing.T) {
	face, addr := newFakeFace(t)
	client := newClient(t, addr)

	client.Emotion("ecstatic")
	client.Gaze(0.5, math.NaN())
	face.expectNone(t)
}
