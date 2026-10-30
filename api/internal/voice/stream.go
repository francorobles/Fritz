package voice

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Speaker speaks sentences as they arrive instead of waiting for a
// whole reply: start it before the first streamed sentence (Piper's
// model load then overlaps the reply), feed each finished sentence
// to Say, and Close when the reply ends. One piper + aplay pipeline
// per reply; use from a single goroutine.
type Speaker struct {
	piper *exec.Cmd
	aplay *exec.Cmd
	stdin io.WriteCloser
}

// StartSpeaker launches the synthesis pipeline for one reply.
// voicePath comes from FRITZ_PIPER_VOICE at the call site.
func StartSpeaker(ctx context.Context, voicePath string) (*Speaker, error) {
	if voicePath == "" {
		return nil, fmt.Errorf("FRITZ_PIPER_VOICE is unset (path to the Piper .onnx voice)")
	}
	rate := PiperSampleRate(voicePath)
	piper := exec.CommandContext(ctx, "piper", "--model", voicePath, "--output-raw")
	stdin, err := piper.StdinPipe()
	if err != nil {
		return nil, err
	}
	aplay := exec.CommandContext(ctx, "aplay", "-D", "pipewire",
		"-r", fmt.Sprint(rate), "-f", "S16_LE", "-c", "1", "-t", "raw", "-q")
	pipe, err := piper.StdoutPipe()
	if err != nil {
		return nil, err
	}
	aplay.Stdin = pipe
	if err := piper.Start(); err != nil {
		return nil, fmt.Errorf("piper: %w (is piper installed?)", err)
	}
	if err := aplay.Start(); err != nil {
		return nil, fmt.Errorf("aplay: %w (is alsa-utils installed?)", err)
	}
	return &Speaker{piper: piper, aplay: aplay, stdin: stdin}, nil
}

// Say feeds one sentence for synthesis. Empty text is a no-op. A
// failed Say means the pipeline is broken; Close it and carry on
// without speech.
func (s *Speaker) Say(sentence string) error {
	if strings.TrimSpace(sentence) == "" {
		return nil
	}
	if _, err := io.WriteString(s.stdin, sentence+"\n"); err != nil {
		return fmt.Errorf("piper: %w", err)
	}
	return nil
}

// Close ends the pipeline and waits for playback to finish. Call it
// once, even when no sentence was ever said.
func (s *Speaker) Close() error {
	_ = s.stdin.Close() // EOF lets piper finish, then aplay drains
	if err := s.piper.Wait(); err != nil {
		_ = s.aplay.Wait() // reap aplay either way
		return fmt.Errorf("piper: %w", err)
	}
	if err := s.aplay.Wait(); err != nil {
		return fmt.Errorf("aplay: %w", err)
	}
	return nil
}

// Splitter cuts streamed reply text into speakable sentences.
// Deltas arrive mid-word, so Feed buffers until a sentence clearly
// ends — a period, exclamation, or question mark followed by
// whitespace (the space also keeps decimals like 3.50 unsplit) — and
// returns the newly completed sentences. Flush returns whatever is
// left when the stream ends.
type Splitter struct {
	pending string
}

// Feed appends a text chunk and returns the sentences it completed.
func (s *Splitter) Feed(chunk string) []string {
	s.pending += chunk
	var out []string
	for {
		end := -1
		for i := 0; i+1 < len(s.pending); i++ {
			if c := s.pending[i]; (c == '.' || c == '!' || c == '?') && isSpaceByte(s.pending[i+1]) {
				end = i + 1
				break
			}
		}
		if end < 0 {
			return out
		}
		if sentence := strings.TrimSpace(s.pending[:end]); sentence != "" {
			out = append(out, sentence)
		}
		s.pending = s.pending[end:]
	}
}

// Flush returns the remaining text, if any.
func (s *Splitter) Flush() string {
	text := strings.TrimSpace(s.pending)
	s.pending = ""
	return text
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
