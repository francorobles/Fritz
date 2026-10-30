package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// RealtimeURL is the Muse Voice Transcribe realtime endpoint. It is a
// var so tests can point it at a fake server. Note: the HTTP
// Authorization header is ignored on this endpoint; the key goes in
// the first text frame instead.
var RealtimeURL = "wss://api.meta.ai/v1/asr/realtime"

const (
	// chunkBytes is 100 ms of 16 kHz mono s16le audio.
	chunkBytes = 3200
	// Sessions last at most 60 minutes; callers should reconnect
	// before that (Run returns, the caller starts a new session).
	sessionLimit = 55 * time.Minute
)

// Client streams audio to Muse Voice Transcribe and reports events.
// While Muted is set, mic audio is replaced with silence (the API
// requires continuous frames on a live source) — that is how Fritz
// avoids hearing itself while it speaks.
type Client struct {
	APIKey string
	Muted  atomic.Bool
}

// handshake is the first text frame, sent within 10 s of connecting.
type handshake struct {
	Authorization struct {
		AccessToken string `json:"accessToken"`
	} `json:"authorization"`
	AudioEncoding string `json:"audioEncoding"`
	Model         string `json:"model"`
	Mode          string `json:"mode"`
	PartialMode   string `json:"partialMode"`
}

// Run connects, handshakes, then pumps audio (paced at real time) and
// delivers events to onEvent until the audio ends, ctx is cancelled,
// the session approaches its limit, or the server errors. Audio is
// read in 100 ms chunks; short final chunks are sent as-is. Events of
// unknown type are delivered too — callers ignore what they don't use.
func (c *Client) Run(ctx context.Context, audio io.Reader, onEvent func(Event)) error {
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, _, err := dialer.DialContext(ctx, RealtimeURL, nil)
	if err != nil {
		return fmt.Errorf("voice: dial realtime ASR: %w", err)
	}
	defer conn.Close()

	var hs handshake
	hs.Authorization.AccessToken = "Bearer " + c.APIKey
	hs.AudioEncoding = "PCM_16KHZ"
	hs.Model = "muse-voice-transcribe-1.0"
	hs.Mode = "ENDPOINTING"
	hs.PartialMode = "CUMULATIVE"
	hsJSON, _ := json.Marshal(hs)
	if err := conn.WriteMessage(websocket.TextMessage, hsJSON); err != nil {
		return fmt.Errorf("voice: send handshake: %w", err)
	}

	// Wait for the sessionId acknowledgement before sending audio.
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("voice: waiting for session ack: %w", err)
		}
		event, err := ParseEvent(data)
		if err != nil {
			continue
		}
		if event.IsSessionAck() {
			onEvent(event) // let callers see the session is established
			break
		}
		if event.Type == "error" {
			return fmt.Errorf("voice: ASR error during handshake: %s", event.Message)
		}
	}
	_ = conn.SetReadDeadline(time.Time{})

	ctx, cancel := context.WithTimeout(ctx, sessionLimit)
	defer cancel()

	// Reader goroutine: deliver events, then cancel everything.
	readErr := make(chan error, 1)
	go func() {
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				readErr <- err
				cancel()
				return
			}
			event, err := ParseEvent(data)
			if err != nil {
				continue // ignore malformed frames rather than die
			}
			if event.Type == "error" {
				readErr <- fmt.Errorf("voice: ASR error: %s", event.Message)
				cancel()
				return
			}
			onEvent(event)
		}
	}()

	// Pump: read exactly one 100 ms chunk and send it immediately.
	// The mic itself paces this at real time (a full chunk takes
	// 100 ms to record), so no ticker is needed — adding one on top
	// of fragmented pipe reads made the stream fall behind the clock
	// and replies feel slower and slower. ReadFull blocks until the
	// chunk is whole (or the source ends), which is the pacing.
	buf := make([]byte, chunkBytes)
	silence := make([]byte, chunkBytes)
pump:
	for {
		select {
		case <-ctx.Done():
			break pump
		case err := <-readErr:
			return err
		default:
		}
		n, readErr2 := io.ReadFull(audio, buf)
		if n > 0 {
			chunk := buf[:n]
			if c.Muted.Load() {
				chunk = silence[:n]
			}
			if err := conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
				return fmt.Errorf("voice: send audio: %w", err)
			}
		}
		if readErr2 == io.EOF || readErr2 == io.ErrUnexpectedEOF {
			break pump
		}
		if readErr2 != nil {
			return fmt.Errorf("voice: read audio: %w", readErr2)
		}
	}

	// Graceful end so the server can flush the final turn.
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"endStream"}`))
	select {
	case err := <-readErr:
		if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
			return nil
		}
		// The server closing after endStream is the normal path; the
		// reader reports it as an unexpected EOF/close. Treat any
		// reader end after a clean audio EOF as success.
		return nil
	case <-time.After(3 * time.Second):
		return nil
	}
}
