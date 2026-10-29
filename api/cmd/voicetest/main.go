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
)

// transcribeURL is a var so tests can point it at a fake server.
var transcribeURL = "https://api.meta.ai/v1/asr/transcribe"

var failures int

func pass(format string, args ...any) { fmt.Printf("PASS  "+format+"\n", args...) }
func fail(format string, args ...any) {
	fmt.Printf("FAIL  "+format+"\n", args...)
	failures++
}

// wakeAliases are the accepted wake spellings (see the fritz-voice
// skill: collect real mis-hearings from logs and add them here).
var wakeAliases = []string{"fritz", "fritts", "frits", "fritzz"}

// wakeMatch reports whether a transcript contains the wake phrase.
// Matching is case-insensitive and ignores punctuation, so
// "Hi, Fritz!" and "hi fritz" both count.
func wakeMatch(transcript string) bool {
	cleaned := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ' ' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return ' '
	}, transcript)
	for _, word := range strings.Fields(cleaned) {
		for _, alias := range wakeAliases {
			if word == alias {
				return true
			}
		}
	}
	return false
}

// piperSampleRate reads audio.sample_rate from the voice's .onnx.json
// (the file next to the .onnx model); Piper's raw output is only
// intelligible when aplay uses exactly this rate.
func piperSampleRate(voicePath string) int {
	data, err := os.ReadFile(voicePath + ".json")
	if err != nil {
		return 22050
	}
	var cfg struct {
		Audio struct {
			SampleRate int `json:"sample_rate"`
		} `json:"audio"`
	}
	if json.Unmarshal(data, &cfg) == nil && cfg.Audio.SampleRate > 0 {
		return cfg.Audio.SampleRate
	}
	return 22050
}

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
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("transcribe response: %w", err)
	}
	return out.Transcript, nil
}

func stepSpeak(ctx context.Context, text string) error {
	voice := os.Getenv("FRITZ_PIPER_VOICE")
	if voice == "" {
		return fmt.Errorf("FRITZ_PIPER_VOICE is unset (path to the Piper .onnx voice)")
	}
	rate := piperSampleRate(voice)
	piper := exec.CommandContext(ctx, "piper", "--model", voice, "--output-raw")
	piper.Stdin = strings.NewReader(text)
	aplay := exec.CommandContext(ctx, "aplay", "-D", "pipewire",
		"-r", fmt.Sprint(rate), "-f", "S16_LE", "-c", "1", "-t", "raw", "-q")
	pipe, err := piper.StdoutPipe()
	if err != nil {
		return err
	}
	aplay.Stdin = pipe
	if err := piper.Start(); err != nil {
		return fmt.Errorf("piper: %w (is piper installed?)", err)
	}
	if err := aplay.Start(); err != nil {
		return fmt.Errorf("aplay: %w (is alsa-utils installed?)", err)
	}
	if err := piper.Wait(); err != nil {
		return fmt.Errorf("piper: %w", err)
	}
	if err := aplay.Wait(); err != nil {
		return fmt.Errorf("aplay: %w", err)
	}
	return nil
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
	defer os.Remove(wavPath)
	micName := os.Getenv("FRITZ_MIC")
	if micName == "" {
		micName = "pipewire"
	}
	if info, err := os.Stat(wavPath); err == nil && info.Size() > 44 {
		pass("microphone: recorded %d bytes of WAV (arecord -D %s)", info.Size(), micName)
	} else {
		fail("microphone: recording is empty (%v)", err)
	}

	transcript, err := stepTranscribe(ctx, wavPath, apiKey)
	if err != nil {
		fail("transcription: %v", err)
		fmt.Printf("\n%d step(s) FAILED\n", failures)
		os.Exit(1)
	}
	fmt.Printf("transcript: %q\n", transcript)

	if !wakeMatch(transcript) {
		if faceClient != nil {
			faceClient.Emotion("skeptical")
		}
		fail("wake phrase: %q does not contain \"Fritz\" — try again, closer to the mic", transcript)
		fmt.Printf("\n%d step(s) FAILED\n", failures)
		os.Exit(1)
	}
	pass("wake phrase: heard \"Fritz\"")

	if faceClient != nil {
		faceClient.Blink()
		faceClient.Emotion("happy")
		faceClient.Speaking(true)
	}
	if err := stepSpeak(ctx, *reply); err != nil {
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
