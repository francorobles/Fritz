package integrations

import (
	"html"
	"regexp"
	"strings"
)

var (
	tagRe       = regexp.MustCompile(`(?s)<[^>]*>`)
	spaceRe     = regexp.MustCompile(`[ \t]+`)
	blankLineRe = regexp.MustCompile(`\n{3,}`)
	onWroteRe   = regexp.MustCompile(`^On .+ wrote:$`)
)

// htmlToText flattens an HTML mail body to readable plain text:
// block tags become newlines, other tags vanish, entities decode.
func htmlToText(s string) string {
	s = regexp.MustCompile(`(?i)<(br|/p|/div|/li|/tr)[^>]*>`).ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = spaceRe.ReplaceAllString(strings.TrimSpace(line), " ")
	}
	s = blankLineRe.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")
	return strings.TrimSpace(s)
}

// stripQuotedReply drops the quoted tail that starts with an
// "On <date>, <person> wrote:" line, per the fritz-integrations
// skill — the model should reason about the new text, not the
// whole thread history.
func stripQuotedReply(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if onWroteRe.MatchString(strings.TrimSpace(line)) {
			return strings.TrimRight(strings.Join(lines[:i], "\n"), " \t\n>") + "\n"
		}
	}
	return text
}

// truncate shortens s to at most n bytes (on a rune boundary),
// for snippets and log lines.
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	for n > 0 && (s[n]&0xC0) == 0x80 { // back off to a rune start
		n--
	}
	return s[:n] + "…"
}
