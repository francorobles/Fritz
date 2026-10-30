package voice

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Engine is Fritz's always-on speech output: ONE piper process with
// the voice model loaded once at startup, and ONE aplay stream to the
// speaker that never closes. Starting piper and aplay per reply (what
// Speaker does) costs the model load plus waking the Bluetooth sink
// on every answer — about a second on the Pi before the first word.
//
// Piper runs with --output_dir: for every stdin line it writes one
// WAV file and prints its path, which gives clean per-sentence
// boundaries (raw output is one undelimited stream). The engine
// reads each WAV and streams it into aplay paced at real time, and
// writes silence in between, so the sink stays awake and the engine
// always knows when the last word has actually played.
type Engine struct {
	rate int
	dir  string

	piper   *exec.Cmd
	piperIn io.WriteCloser
	aplay   *exec.Cmd
	aplayIn io.WriteCloser

	mu       sync.Mutex
	jobs     []engineJob // lines sent to piper, oldest first (piper answers in order)
	queue    []playItem  // audio waiting to be played
	pending  int         // queued items (Say/PlayPCM) not yet fully written to aplay
	playEnd  time.Time   // when the last real audio written finishes playing
	progress time.Time   // last successful write to aplay (stall detection)
	err      error       // first fatal pipeline error
}

type engineJob struct {
	render  chan []byte // non-nil: return the audio instead of playing it
	onAudio OnAudio     // play jobs: called when its audio starts streaming
}

type playItem struct {
	pcm     []byte
	onStart OnAudio
}

// OnAudio is told when a sentence starts playing, and how much
// silence came before it since the previous audio ended. Within a
// reply that gap is the stutter a listener hears: piper (or the
// model) not keeping up with playback.
type OnAudio func(at time.Time, gap time.Duration)

// Playback pacing. The writer keeps engineLead of audio ahead of the
// wall clock in chunks of engineTick; aplay's own buffer is kept
// small (engineBufferUS) so queued speech isn't stuck behind half a
// second of buffered silence.
const (
	engineTick     = 20 * time.Millisecond
	engineLead     = 80 * time.Millisecond
	engineBufferUS = 150000
)

// NewEngine starts piper and aplay and keeps them running until ctx
// ends. voicePath comes from FRITZ_PIPER_VOICE at the call site.
func NewEngine(ctx context.Context, voicePath string) (*Engine, error) {
	if voicePath == "" {
		return nil, fmt.Errorf("FRITZ_PIPER_VOICE is unset (path to the Piper .onnx voice)")
	}
	// WAVs are tiny and short-lived; keep them in RAM (the runtime dir
	// is tmpfs) rather than wearing the SD card.
	base := os.Getenv("XDG_RUNTIME_DIR")
	dir, err := os.MkdirTemp(base, "fritz-piper-")
	if err != nil {
		return nil, fmt.Errorf("piper: temp dir: %w", err)
	}
	e := &Engine{rate: PiperSampleRate(voicePath), dir: dir, progress: time.Now()}

	args := append([]string{"--model", voicePath, "--output_dir", dir}, PiperArgs()...)
	e.piper = exec.CommandContext(ctx, "piper", args...)
	if e.piperIn, err = e.piper.StdinPipe(); err != nil {
		return nil, err
	}
	piperOut, err := e.piper.StdoutPipe()
	if err != nil {
		return nil, err
	}
	e.aplay = exec.CommandContext(ctx, "aplay", "-D", "pipewire",
		"-r", fmt.Sprint(e.rate), "-f", "S16_LE", "-c", "1", "-t", "raw", "-q",
		"-B", fmt.Sprint(engineBufferUS))
	if e.aplayIn, err = e.aplay.StdinPipe(); err != nil {
		return nil, err
	}
	if err := e.piper.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("piper: %w (is piper installed?)", err)
	}
	if err := e.aplay.Start(); err != nil {
		_ = e.piper.Process.Kill()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("aplay: %w (is alsa-utils installed?)", err)
	}

	go e.readWAVs(piperOut)
	go e.play(ctx)
	go func() {
		<-ctx.Done()
		_ = e.piperIn.Close()
		_ = e.piper.Wait()
		_ = e.aplay.Wait()
		_ = os.RemoveAll(dir)
	}()
	return e, nil
}

// Say queues one sentence: piper synthesizes it and it plays after
// everything queued before it. onAudio (may be nil) is called when
// its audio starts streaming to the speaker.
func (e *Engine) Say(sentence string, onAudio OnAudio) error {
	sentence = oneLine(sentence)
	if sentence == "" {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	e.jobs = append(e.jobs, engineJob{onAudio: onAudio})
	e.pending++
	if _, err := io.WriteString(e.piperIn, sentence+"\n"); err != nil {
		e.failLocked(fmt.Errorf("piper: %w", err))
		return e.err
	}
	return nil
}

// Render synthesizes text to raw PCM without playing it — used at
// startup to pre-render short acknowledgements.
func (e *Engine) Render(ctx context.Context, text string) ([]byte, error) {
	text = oneLine(text)
	if text == "" {
		return nil, nil
	}
	out := make(chan []byte, 1)
	e.mu.Lock()
	if e.err != nil {
		e.mu.Unlock()
		return nil, e.err
	}
	e.jobs = append(e.jobs, engineJob{render: out})
	if _, err := io.WriteString(e.piperIn, text+"\n"); err != nil {
		e.failLocked(fmt.Errorf("piper: %w", err))
		e.mu.Unlock()
		return nil, e.err
	}
	e.mu.Unlock()
	select {
	case pcm := <-out:
		if pcm == nil {
			return nil, fmt.Errorf("piper: could not render %q", text)
		}
		return pcm, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// PlayPCM queues pre-rendered audio (from Render) to play next.
func (e *Engine) PlayPCM(pcm []byte) {
	if len(pcm) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return
	}
	e.queue = append(e.queue, playItem{pcm: pcm})
	e.pending++
}

// Wait blocks until everything queued so far has finished playing.
// It gives up when the speaker stops taking audio for 3 s, so a
// stuck output never leaves Fritz muted forever.
func (e *Engine) Wait(ctx context.Context) error {
	poll := time.NewTicker(engineTick)
	defer poll.Stop()
	for {
		e.mu.Lock()
		err, pending, end, progress := e.err, e.pending, e.playEnd, e.progress
		e.mu.Unlock()
		switch {
		case err != nil:
			return err
		case pending == 0 && !time.Now().Before(end):
			return nil
		case time.Since(progress) > 3*time.Second:
			return errors.New("speaker stalled: aplay stopped taking audio for 3 s")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-poll.C:
		}
	}
}

// Err reports the pipeline's fatal error, if any. A broken engine
// stays broken; callers fall back to per-reply speech.
func (e *Engine) Err() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.err
}

func (e *Engine) failLocked(err error) {
	if e.err == nil {
		e.err = err
	}
}

// readWAVs pairs each path piper prints with the oldest outstanding
// job (piper handles lines strictly in order).
func (e *Engine) readWAVs(stdout io.Reader) {
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		path := strings.TrimSpace(sc.Text())
		if path == "" {
			continue
		}
		e.mu.Lock()
		if len(e.jobs) == 0 {
			e.mu.Unlock()
			_ = os.Remove(path)
			continue
		}
		job := e.jobs[0]
		e.jobs = e.jobs[1:]
		e.mu.Unlock()

		pcm, err := readPiperWAV(path)
		_ = os.Remove(path)
		if job.render != nil {
			job.render <- pcm // nil on error
			continue
		}
		e.mu.Lock()
		if err != nil || len(pcm) == 0 {
			e.pending-- // nothing to play for this sentence
		} else {
			e.queue = append(e.queue, playItem{pcm: pcm, onStart: job.onAudio})
		}
		e.mu.Unlock()
	}
	e.mu.Lock()
	e.failLocked(errors.New("piper exited"))
	for _, job := range e.jobs {
		if job.render != nil {
			job.render <- nil
		}
	}
	e.jobs = nil
	e.mu.Unlock()
}

// play streams queued audio to aplay at real time, filling the gaps
// with silence. Audio at stream position p plays at about start+p
// (plus a constant output delay), which is how playEnd is known.
func (e *Engine) play(ctx context.Context) {
	ticker := time.NewTicker(engineTick)
	defer ticker.Stop()
	chunkBytes := e.bytesFor(engineTick)
	silence := make([]byte, chunkBytes)
	start := time.Now()
	var pos time.Duration
	for {
		select {
		case <-ctx.Done():
			_ = e.aplayIn.Close()
			return
		case <-ticker.C:
		}
		for target := time.Since(start) + engineLead; pos < target; {
			chunk, onStart, finished := e.next(chunkBytes)
			isAudio := chunk != nil
			if !isAudio {
				chunk = silence
			}
			if onStart != nil {
				e.mu.Lock()
				prevEnd := e.playEnd
				e.mu.Unlock()
				var gap time.Duration
				if !prevEnd.IsZero() {
					gap = start.Add(pos).Sub(prevEnd)
				}
				onStart(time.Now(), gap)
			}
			if _, err := e.aplayIn.Write(chunk); err != nil {
				e.mu.Lock()
				e.failLocked(fmt.Errorf("aplay: %w", err))
				e.mu.Unlock()
				return
			}
			pos += e.durationOf(len(chunk))
			e.mu.Lock()
			e.progress = time.Now()
			if isAudio {
				e.playEnd = start.Add(pos)
			}
			if finished {
				e.pending--
			}
			e.mu.Unlock()
		}
	}
}

// next pops up to n bytes of queued audio. chunk is nil when nothing
// is queued; onStart is set on an item's first chunk; finished is
// true on its last.
func (e *Engine) next(n int) (chunk []byte, onStart OnAudio, finished bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.queue) == 0 {
		return nil, nil, false
	}
	item := &e.queue[0]
	onStart, item.onStart = item.onStart, nil
	if len(item.pcm) <= n {
		chunk = item.pcm
		e.queue = e.queue[1:]
		return chunk, onStart, true
	}
	chunk, item.pcm = item.pcm[:n], item.pcm[n:]
	return chunk, onStart, false
}

func (e *Engine) bytesFor(d time.Duration) int {
	n := int(int64(e.rate) * int64(d) / int64(time.Second) * 2)
	return n &^ 1 // whole 16-bit samples
}

func (e *Engine) durationOf(bytes int) time.Duration {
	return time.Duration(int64(bytes/2) * int64(time.Second) / int64(e.rate))
}

// readPiperWAV returns the PCM data of a WAV piper just wrote. Piper
// prints the path before its file stream is flushed, so wait (up to
// 2 s) until the file holds the whole data chunk its header declares.
func readPiperWAV(path string) ([]byte, error) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			if pcm, ok := wavData(data); ok {
				return pcm, nil
			}
		}
		if time.Now().After(deadline) {
			if err == nil {
				err = errors.New("incomplete WAV")
			}
			return nil, fmt.Errorf("piper: read %s: %w", path, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// wavData finds the "data" chunk and reports whether it is complete.
func wavData(b []byte) ([]byte, bool) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, false
	}
	for off := 12; off+8 <= len(b); {
		id := string(b[off : off+4])
		size := int(binary.LittleEndian.Uint32(b[off+4 : off+8]))
		body := off + 8
		if id == "data" {
			if body+size > len(b) {
				return nil, false
			}
			return b[body : body+size : body+size], true
		}
		off = body + size + size%2
	}
	return nil, false
}

// oneLine keeps text on a single line: piper treats each stdin line
// as one utterance, and the engine counts on one path per line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
