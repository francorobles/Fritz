package voice

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	urlPattern    = regexp.MustCompile(`https?://\S+|www\.\S+`)
	emailPattern  = regexp.MustCompile(`<?[\w.+-]+@[\w-]+\.[\w.-]+>?`)
	bulletPattern = regexp.MustCompile(`(?m)^\s*(?:[-*•]|\d+[.)])\s+`)
	spacePattern  = regexp.MustCompile(`\s+`)
)

// Speakable turns model text into what should be said aloud. The
// prompt already asks for plain speech, but a stray "**", emoji, URL
// or address gets read out symbol by symbol ("asterisk asterisk"),
// which is what makes a voice sound like a machine reading a screen.
func Speakable(text string) string {
	text = urlPattern.ReplaceAllString(text, "a link")
	text = emailPattern.ReplaceAllString(text, "")
	text = bulletPattern.ReplaceAllString(text, "")
	text = strings.NewReplacer(
		"**", "", "__", "", "`", "", "#", "", "*", "",
		" & ", " and ", "→", " to ", "—", ", ", "–", ", ",
	).Replace(text)
	text = strings.Map(func(r rune) rune {
		// Emoji and pictographs: piper either skips them or reads
		// their names.
		if r >= 0x1F000 || (r >= 0x2600 && r <= 0x27BF) || r == 0xFE0F || r == 0x200D ||
			(unicode.Is(unicode.So, r) && r != '°') {
			return -1
		}
		return r
	}, text)
	text = spacePattern.ReplaceAllString(text, " ")
	text = strings.ReplaceAll(text, " ,", ",")
	return strings.TrimSpace(text)
}
