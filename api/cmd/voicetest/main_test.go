package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStepTranscribeAgainstFakeServer(t *testing.T) {
	var gotAuth, gotRequest, gotAudio string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		gotRequest = r.FormValue("request")
		file, _, err := r.FormFile("audio")
		if err != nil {
			t.Errorf("FormFile(audio): %v", err)
			return
		}
		defer file.Close()
		audio, _ := io.ReadAll(file)
		gotAudio = string(audio)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"transcript":"Hi Fritz"}`)
	}))
	defer server.Close()

	old := transcribeURL
	transcribeURL = server.URL
	defer func() { transcribeURL = old }()

	wav := filepath.Join(t.TempDir(), "t.wav")
	if err := os.WriteFile(wav, []byte("fake-wav-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	transcript, err := stepTranscribe(context.Background(), wav, "test-key")
	if err != nil {
		t.Fatalf("stepTranscribe: %v", err)
	}
	if transcript != "Hi Fritz" {
		t.Errorf("transcript = %q, want %q", transcript, "Hi Fritz")
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want Bearer test-key", gotAuth)
	}
	if !strings.Contains(gotRequest, `"muse-voice-transcribe-1.0"`) ||
		!strings.Contains(gotRequest, `"WAV"`) {
		t.Errorf("request part = %q, want model + WAV encoding", gotRequest)
	}
	if gotAudio != "fake-wav-bytes" {
		t.Errorf("audio part = %q, want the wav bytes", gotAudio)
	}
}

func TestWavPeak(t *testing.T) {
	makeWav := func(samples ...int16) string {
		path := filepath.Join(t.TempDir(), "t.wav")
		data := []byte("RIFF\x00\x00\x00\x00WAVEfmt data")
		for _, s := range samples {
			u := uint16(s)
			data = append(data, byte(u), byte(u>>8))
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if peak, err := wavPeak(makeWav(0, 100, -5000, 300)); err != nil || peak != 5000 {
		t.Errorf("wavPeak = %d, %v; want 5000", peak, err)
	}
	if peak, err := wavPeak(makeWav(0, 0, 0)); err != nil || peak != 0 {
		t.Errorf("wavPeak silent = %d, %v; want 0", peak, err)
	}
}

func TestStepTranscribeTurnsFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"transcript":"","turns":[{"transcript":"Hi"},{"transcript":"Fritz"}]}`)
	}))
	defer server.Close()
	old := transcribeURL
	transcribeURL = server.URL
	defer func() { transcribeURL = old }()

	wav := filepath.Join(t.TempDir(), "t.wav")
	if err := os.WriteFile(wav, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := stepTranscribe(context.Background(), wav, "k")
	if err != nil || got != "Hi Fritz" {
		t.Errorf("stepTranscribe turns fallback = %q, %v; want \"Hi Fritz\"", got, err)
	}
}
