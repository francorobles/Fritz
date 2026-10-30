package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testWAV builds a 16-bit mono WAV whose samples are all fill bytes,
// so each sentence's audio is recognizable in the played stream.
func testWAV(fill byte, dataBytes int) []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+dataBytes))
	b.WriteString("WAVEfmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))     // PCM
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))     // mono
	_ = binary.Write(&b, binary.LittleEndian, uint32(16000)) // rate
	_ = binary.Write(&b, binary.LittleEndian, uint32(32000)) // bytes/s
	_ = binary.Write(&b, binary.LittleEndian, uint16(2))
	_ = binary.Write(&b, binary.LittleEndian, uint16(16))
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(dataBytes))
	b.Write(bytes.Repeat([]byte{fill}, dataBytes))
	return b.Bytes()
}

// fakeSpeech installs a fake piper that behaves like
// `piper --output_dir DIR` (one WAV per stdin line, path printed on
// stdout; line N gets wav<N>.wav) and a fake aplay that records
// everything it is sent. Returns the voice path and the played file.
func fakeSpeech(t *testing.T, wavs ...[]byte) (voicePath, played string) {
	t.Helper()
	dir := t.TempDir()
	for i, w := range wavs {
		if err := os.WriteFile(filepath.Join(dir, "wav"+string(rune('1'+i))+".wav"), w, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	piperSh := `#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--output_dir" ]; then out="$2"; fi
  shift
done
n=0
while IFS= read -r line; do
  n=$((n+1))
  cp "` + dir + `/wav$n.wav" "$out/$n.wav"
  echo "$out/$n.wav"
done
`
	played = filepath.Join(dir, "played")
	if err := os.WriteFile(filepath.Join(dir, "piper"), []byte(piperSh), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "aplay"), []byte("#!/bin/sh\ncat > "+played+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	voicePath = filepath.Join(dir, "voice.onnx")
	if err := os.WriteFile(voicePath+".json", []byte(`{"audio":{"sample_rate":16000}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	return voicePath, played
}

// waitPlayed polls the fake aplay's output until it holds want
// non-silent bytes (the pipe may still be draining after Wait).
func waitPlayed(t *testing.T, played string, want int) []byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(played)
		if len(data)-bytes.Count(data, []byte{0}) >= want || time.Now().After(deadline) {
			return data
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestEngineSpeaksInOrder checks the engine's core promise: one piper
// for many sentences, audio played in order (an ack queued first
// plays first), and Wait returning only after the audio has played.
func TestEngineSpeaksInOrder(t *testing.T) {
	const n = 3200 // 0.1 s at 16 kHz
	voicePath, played := fakeSpeech(t, testWAV(0x11, n), testWAV(0x22, n), testWAV(0x33, n))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	e, err := NewEngine(ctx, voicePath)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	// Line 1 is rendered, not played.
	ack, err := e.Render(ctx, "Mm-hm.")
	if err != nil || len(ack) != n || ack[0] != 0x11 {
		t.Fatalf("Render = %d bytes (err %v), want %d bytes of 0x11", len(ack), err, n)
	}

	var started []time.Time
	onAudio := func(at time.Time) { started = append(started, at) }
	begin := time.Now()
	e.PlayPCM(ack)
	if err := e.Say("Hi there.", onAudio); err != nil {
		t.Fatalf("Say: %v", err)
	}
	if err := e.Say("It is\n9 AM.", onAudio); err != nil { // newline must not split the job
		t.Fatalf("Say: %v", err)
	}
	if err := e.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if took := time.Since(begin); took < 250*time.Millisecond {
		t.Errorf("Wait returned after %s, before 0.3 s of audio could play", took)
	}
	if len(started) != 2 {
		t.Errorf("onAudio called %d times, want once per sentence", len(started))
	}

	data := waitPlayed(t, played, 3*n)
	i1, i2, i3 := bytes.IndexByte(data, 0x11), bytes.IndexByte(data, 0x22), bytes.IndexByte(data, 0x33)
	if i1 < 0 || i2 < 0 || i3 < 0 || !(i1 < i2 && i2 < i3) {
		t.Fatalf("played order: ack@%d sentence1@%d sentence2@%d, want ack, then sentences in order", i1, i2, i3)
	}
	for _, b := range []byte{0x11, 0x22, 0x33} {
		if got := bytes.Count(data, []byte{b}); got != n {
			t.Errorf("byte %#x played %d times, want %d (whole clip, once)", b, got, n)
		}
	}
}

func TestEngineNeedsVoice(t *testing.T) {
	if _, err := NewEngine(context.Background(), ""); err == nil {
		t.Error("NewEngine with no voice path must error")
	}
}

// TestWAVData covers piper's flush race: the path is printed before
// the file is complete, so a short data chunk must read as "not yet".
func TestWAVData(t *testing.T) {
	full := testWAV(0x44, 100)
	if pcm, ok := wavData(full); !ok || len(pcm) != 100 {
		t.Errorf("complete WAV: %d bytes ok=%v, want 100 true", len(pcm), ok)
	}
	if _, ok := wavData(full[:len(full)-10]); ok {
		t.Error("truncated WAV reported complete")
	}
	if _, ok := wavData([]byte("not a wav file at all")); ok {
		t.Error("garbage reported as WAV")
	}
}

func TestSplitterFirstClause(t *testing.T) {
	s := Splitter{FirstClause: true}
	got := append(s.Feed("Sure, the meeting is at 3, in room 2. See you then."), s.Flush())
	want := []string{"Sure,", "the meeting is at 3, in room 2.", "See you then."}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Feed = %q, want %q (only the first chunk may end at a comma)", got, want)
	}
	// Thousands separators have no space after the comma.
	s = Splitter{FirstClause: true}
	if got := s.Feed("About 1,000 people"); len(got) != 0 {
		t.Errorf("Feed = %q, want nothing (1,000 is not a clause break)", got)
	}
	// Off by default: commas never split.
	var plain Splitter
	if got := plain.Feed("Sure, it is 9. "); len(got) != 1 || got[0] != "Sure, it is 9." {
		t.Errorf("default Feed = %q, want one whole sentence", got)
	}
}
