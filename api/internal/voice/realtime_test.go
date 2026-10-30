package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestClientRunAgainstFakeServer(t *testing.T) {
	var gotHandshake []byte
	audioBytes := 0
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Handshake, then ack.
		_, gotHandshake, _ = conn.ReadMessage()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"sessionId":"s1"}`))
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if kind == websocket.BinaryMessage {
				audioBytes += len(data)
				continue
			}
			if strings.Contains(string(data), "endStream") {
				_ = conn.WriteMessage(websocket.TextMessage,
					[]byte(`{"type":"speechComplete","turnId":"t1","transcript":"Hi Fritz"}`))
				return
			}
		}
	}))
	defer server.Close()

	old := RealtimeURL
	RealtimeURL = "ws" + strings.TrimPrefix(server.URL, "http")
	defer func() { RealtimeURL = old }()

	var events []Event
	client := &Client{APIKey: "test-key"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	audio := bytes.NewReader(make([]byte, chunkBytes*2))
	if err := client.Run(ctx, audio, func(e Event) { events = append(events, e) }); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var hs struct {
		Authorization struct {
			AccessToken string `json:"accessToken"`
		} `json:"authorization"`
		AudioEncoding string `json:"audioEncoding"`
		Model         string `json:"model"`
		Mode          string `json:"mode"`
	}
	if err := json.Unmarshal(gotHandshake, &hs); err != nil {
		t.Fatalf("handshake is not JSON: %v (%s)", err, gotHandshake)
	}
	if !strings.HasPrefix(hs.Authorization.AccessToken, "Bearer ") ||
		hs.AudioEncoding != "PCM_16KHZ" ||
		hs.Model != "muse-voice-transcribe-1.0" || hs.Mode != "ENDPOINTING" {
		t.Errorf("handshake fields = %+v", hs)
	}
	if audioBytes != chunkBytes*2 {
		t.Errorf("server received %d audio bytes, want %d", audioBytes, chunkBytes*2)
	}
	found := false
	for _, e := range events {
		if e.Type == "speechComplete" && e.Transcript == "Hi Fritz" {
			found = true
		}
	}
	if !found {
		t.Errorf("events = %+v, want a speechComplete with the transcript", events)
	}
}
