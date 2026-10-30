// Package voice is Fritz's hearing: microphone capture, realtime
// transcription with Muse Voice Transcribe, the wake-phrase filter,
// and (later) Piper speech. See the fritz-voice skill.
package voice

import "strings"

// wakeAliases are the accepted wake spellings. Collect real
// mis-hearings from logs and add them here.
var wakeAliases = map[string]bool{
	"fritz": true, "fritts": true, "frits": true, "fritzz": true,
}

// cleanWords lowercases a transcript and splits it into words,
// dropping all punctuation ("Hi, Fritz!" -> ["hi", "fritz"]).
func cleanWords(transcript string) []string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == ' ':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return ' '
		}
	}, transcript)
	return strings.Fields(cleaned)
}

// WakeMatch reports whether a transcript contains the wake phrase as a
// whole word ("fritzgerald" does not count).
func WakeMatch(transcript string) bool {
	for _, word := range cleanWords(transcript) {
		if wakeAliases[word] {
			return true
		}
	}
	return false
}

// StripWake removes the first wake word from a transcript and returns
// the remaining request text, cleaned ("Hi Fritz, what time is it?" ->
// "hi what time is it"). Callers check WakeMatch first.
func StripWake(transcript string) string {
	words := cleanWords(transcript)
	for i, word := range words {
		if wakeAliases[word] {
			words = append(words[:i], words[i+1:]...)
			break
		}
	}
	return strings.Join(words, " ")
}
