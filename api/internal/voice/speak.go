package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// PiperSampleRate reads audio.sample_rate from the voice's .onnx.json
// (the file next to the .onnx model); Piper's raw output is only
// intelligible when aplay uses exactly this rate. Falls back to 22050
// (the common Piper voice rate) when the config is missing.
func PiperSampleRate(voicePath string) int {
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

// Speak synthesizes text with Piper and plays it through PipeWire
// (the Echo Dot on the Pi). The whole reply goes through ONE piper
// + aplay pipeline: sentences are fed as stdin lines (Piper
// synthesizes line by line and streams raw audio, so playback still
// starts as soon as the first sentence is ready), and the model is
// loaded and the speaker opened only once. Restarting both
// processes per sentence left seconds of dead air after every
// period on the Pi. Callers keep the mic muted while Speak runs
// (see Client.Muted) and for ~700 ms after, for Bluetooth delay.
// voicePath comes from FRITZ_PIPER_VOICE at the call site.
func Speak(ctx context.Context, voicePath, text string) error {
	if voicePath == "" {
		return fmt.Errorf("FRITZ_PIPER_VOICE is unset (path to the Piper .onnx voice)")
	}
	sentences := splitSentences(text)
	if len(sentences) == 0 {
		return nil
	}
	rate := PiperSampleRate(voicePath)
	piper := exec.CommandContext(ctx, "piper", "--model", voicePath, "--output-raw")
	piper.Stdin = strings.NewReader(strings.Join(sentences, "\n") + "\n")
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

// splitSentences breaks a reply into sentence-sized chunks for
// incremental speech. The whole reply is returned as one chunk when
// it has no sentence punctuation.
func splitSentences(text string) []string {
	var out []string
	start := 0
	for i, r := range text {
		if r == '.' || r == '!' || r == '?' {
			s := strings.TrimSpace(text[start : i+1])
			if s != "" {
				out = append(out, s)
			}
			start = i + 1
		}
	}
	if tail := strings.TrimSpace(text[start:]); tail != "" {
		out = append(out, tail)
	}
	if len(out) == 0 && strings.TrimSpace(text) != "" {
		out = append(out, strings.TrimSpace(text))
	}
	return out
}
