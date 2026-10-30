// Command voicetest is a manual end-to-end test for Fritz's desk
// hardware on the Pi: C922 camera → C922 microphone → Muse Voice
// Transcribe → wake phrase → Piper → Echo Dot speaker, with the face
// reacting over UDP. Say "Hi Fritz" when prompted; Fritz answers with
// a canned line ("Hi, I heard you...") and turns happy. This tests the
// whole chain except the agent — no Muse Spark reply is involved.
//
// Run it on the Pi (from api/):
//
//	MODEL_API_KEY=... FRITZ_PIPER_VOICE=/opt/fritz/piper/en_US-lessac-medium.onnx \
//	  go run ./cmd/voicetest
//
// Exit code is 0 only if every step passes. Steps that cannot run
// (missing key, device, or binary) fail with the exact fix to apply.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/francorobles/fritz/api/internal/face"
	"github.com/francorobles/fritz/api/internal/voice"
)

// transcribeURL is a var so tests can point it at a fake server.
var transcribeURL = "https://api.meta.ai/v1/asr/transcribe"

var failures int

func pass(format string, args ...any) { fmt.Printf("PASS  "+format+"\n", args...) }
func fail(format string, args ...any) {
	fmt.Printf("FAIL  "+format+"\n", args...)
	failures++
}

// Wake-phrase matching lives in internal/voice (voice.WakeMatch) so
// this test and the realtime loop can never drift apart.

// Piper speech lives in internal/voice (voice.Speak) so this test
// and the realtime loop share one implementation.

func cameraDevice() string {
	if dev := os.Getenv("FRITZ_CAMERA_DEVICE"); dev != "" {
		return dev
	}
	for _, pattern := range []string{
		"/dev/v4l/by-id/*C922*-video-index0",
		"/dev/v4l/by-id/*-video-index0",
	} {
		if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
			return matches[0]
		}
	}
	return ""
}

func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func stepCamera(ctx context.Context) {
	dev := cameraDevice()
	if dev == "" {
		fail("camera: no device (set FRITZ_CAMERA_DEVICE; ls /dev/v4l/by-id/ | grep -i c922)")
		return
	}
	tmp, err := os.CreateTemp("", "fritz-voicetest-*.jpg")
	if err != nil {
		fail("camera: %v", err)
		return
	}
	defer os.Remove(tmp.Name())
	tmp.Close()
	err = run(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "v4l2", "-input_format", "mjpeg", "-video_size", "1280x720",
		"-i", dev, "-frames:v", "1", "-y", tmp.Name())
	if err != nil {
		fail("camera: %v", err)
		return
	}
	if info, err := os.Stat(tmp.Name()); err == nil && info.Size() > 0 {
		pass("camera: one frame from %s (%d bytes)", dev, info.Size())
	} else {
		fail("camera: ffmpeg produced no frame from %s", dev)
	}
}

func stepRecord(ctx context.Context, seconds int) (string, error) {
	mic := os.Getenv("FRITZ_MIC")
	if mic == "" {
		mic = "pipewire"
	}
	tmp, err := os.CreateTemp("", "fritz-voicetest-*.wav")
	if err != nil {
		return "", err
	}
	tmp.Close() // arecord writes by path; the caller removes the file

	fmt.Printf("\n>>> When you see GO, say: \"Hi Fritz\"\n")
	for i := 3; i > 0; i-- {
		fmt.Printf(">>> %d...\n", i)
		time.Sleep(time.Second)
	}
	fmt.Println(">>> GO — recording now")
	err = run(ctx, "arecord", "-D", mic, "-f", "S16_LE", "-r", "16000",
		"-c", "1", "-t", "wav", "-d", fmt.Sprint(seconds), "-q", tmp.Name())
	fmt.Println(">>> done recording")
	if err != nil {
		return "", err
	}
	return tmp.Name(), nil
}

// wavPeak returns the largest |sample| in a 16-bit PCM WAV file, so a
// silent recording can be told apart from a transcription problem
// before anything is sent to the API.
func wavPeak(wavPath string) (int, error) {
	data, err := os.ReadFile(wavPath)
	if err != nil {
		return 0, err
	}
	i := bytes.Index(data, []byte("data"))
	if i < 0 || i+8 > len(data) {
		return 0, fmt.Errorf("no data chunk in %s", wavPath)
	}
	raw := data[i+8:]
	peak := 0
	for j := 0; j+1 < len(raw); j += 2 {
		s := int(int16(uint16(raw[j]) | uint16(raw[j+1])<<8))
		if s < 0 {
			s = -s
		}
		if s > peak {
			peak = s
		}
	}
	return peak, nil
}

func stepTranscribe(ctx context.Context, wavPath, apiKey string) (string, error) {
	audio, err := os.Open(wavPath)
	if err != nil {
		return "", err
	}
	defer audio.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	reqJSON := `{"mode":"ENDPOINTING","model":"muse-voice-transcribe-1.0","audioEncoding":"WAV"}`
	reqHeader := make(textproto.MIMEHeader)
	reqHeader.Set("Content-Disposition", `form-data; name="request"`)
	reqHeader.Set("Content-Type", "application/json")
	reqPart, err := mw.CreatePart(reqHeader)
	if err != nil {
		return "", err
	}
	if _, err := io.WriteString(reqPart, reqJSON); err != nil {
		return "", err
	}
	filePart, err := mw.CreateFormFile("audio", filepath.Base(wavPath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(filePart, audio); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, transcribeURL, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("transcribe HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var out struct {
		Transcript string `json:"transcript"`
		Turns      []struct {
			Transcript string `json:"transcript"`
		} `json:"turns"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("transcribe response: %w", err)
	}
	transcript := strings.TrimSpace(out.Transcript)
	if transcript == "" && len(out.Turns) > 0 {
		// Some responses carry the text only in per-turn transcripts.
		parts := make([]string, 0, len(out.Turns))
		for _, turn := range out.Turns {
			if t := strings.TrimSpace(turn.Transcript); t != "" {
				parts = append(parts, t)
			}
		}
		transcript = strings.Join(parts, " ")
	}
	if transcript == "" {
		raw := strings.TrimSpace(string(respBody))
		if len(raw) > 600 {
			raw = raw[:600] + "..."
		}
		fmt.Printf("note: transcription returned no text; raw response: %s\n", raw)
	}
	return transcript, nil
}

func main() {
	seconds := flag.Int("listen", 5, "seconds to record")
	reply := flag.String("reply", "Hi, I heard you. Fritz is awake and listening.", "canned reply Fritz speaks when the wake phrase matches")
	skipCamera := flag.Bool("skip-camera", false, "skip the camera frame check")
	flag.Parse()

	ctx := context.Background()
	fmt.Println("Fritz voice test — camera, microphone, speaker, face")
	fmt.Println("Say \"Hi Fritz\" when prompted.")

	faceAddr := face.DefaultAddr
	faceClient, faceErr := face.New(faceAddr)
	if faceErr == nil {
		defer faceClient.Close()
		faceClient.Blink()
		faceClient.Emotion("focused")
	} else {
		fmt.Printf("note: face client unavailable (%v); continuing\n", faceErr)
	}

	if !*skipCamera {
		stepCamera(ctx)
	} else {
		fmt.Println("SKIP  camera (--skip-camera)")
	}

	apiKey := os.Getenv("MODEL_API_KEY")
	if apiKey == "" {
		fail("MODEL_API_KEY is unset — transcription cannot run (export MODEL_API_KEY first)")
		fmt.Printf("\n%d step(s) FAILED\n", failures)
		os.Exit(1)
	}

	wavPath, err := stepRecord(ctx, *seconds)
	if err != nil {
		fail("microphone: %v", err)
		fmt.Printf("\n%d step(s) FAILED\n", failures)
		os.Exit(1)
	}
	keepWav := false
	defer func() {
		if !keepWav {
			os.Remove(wavPath)
		}
	}()
	micName := os.Getenv("FRITZ_MIC")
	if micName == "" {
		micName = "pipewire"
	}
	if info, err := os.Stat(wavPath); err == nil && info.Size() > 44 {
		pass("microphone: recorded %d bytes of WAV (arecord -D %s)", info.Size(), micName)
	} else {
		fail("microphone: recording is empty (%v)", err)
	}

	// A full-size recording can still be silent (wrong source, muted
	// mic). Check the peak level before blaming transcription.
	if peak, err := wavPeak(wavPath); err != nil {
		fail("microphone: %v", err)
	} else if peak <= 200 {
		keepWav = true
		fail("microphone: recording is SILENT (peak amplitude %d/32767) — "+
			"wrong PipeWire source or muted mic. Kept the recording at %s; "+
			"play it back with: pw-play %s ; check the source with: wpctl status "+
			"and wpctl set-volume @DEFAULT_AUDIO_SOURCE@ 1.0", peak, wavPath, wavPath)
		fmt.Printf("\n%d step(s) FAILED\n", failures)
		os.Exit(1)
	} else {
		pass("microphone: audio is non-silent (peak amplitude %d/32767)", peak)
	}

	transcript, err := stepTranscribe(ctx, wavPath, apiKey)
	if err != nil {
		fail("transcription: %v", err)
		fmt.Printf("\n%d step(s) FAILED\n", failures)
		os.Exit(1)
	}
	fmt.Printf("transcript: %q\n", transcript)

	if !voice.WakeMatch(transcript) {
		if faceClient != nil {
			faceClient.Emotion("skeptical")
		}
		keepWav = true
		fail("wake phrase: %q does not contain \"Fritz\" — recording kept at %s (listen with: pw-play %s)", transcript, wavPath, wavPath)
		fmt.Printf("\n%d step(s) FAILED\n", failures)
		os.Exit(1)
	}
	pass("wake phrase: heard \"Fritz\"")

	if faceClient != nil {
		faceClient.Blink()
		faceClient.Emotion("happy")
		faceClient.Speaking(true)
	}
	if err := voice.Speak(ctx, os.Getenv("FRITZ_PIPER_VOICE"), *reply); err != nil {
		if faceClient != nil {
			faceClient.Speaking(false)
		}
		fail("speaker: %v", err)
	} else {
		if faceClient != nil {
			faceClient.Speaking(false)
		}
		pass("speaker: spoke %q via Piper → aplay -D pipewire", *reply)
	}

	if failures > 0 {
		fmt.Printf("\n%d step(s) FAILED\n", failures)
		os.Exit(1)
	}
	fmt.Println("\nVOICE TEST PASSED — Fritz heard you and responded.")
}
