package api

import (
	"regexp"
	"strings"
	"unicode"
)

// Email bodies arrive as one plain-text blob: the normalizer prepends
// "Subject: ...", and replies carry the whole prior thread (attribution
// lines, forwarded-header blocks, "> " quotes). parseEmailBody splits that
// blob for display only — nothing stored changes.

var (
	dividerRE = regexp.MustCompile(`(?i)^(-{2,}\s*(original|forwarded)\s+(message|email)\s*-*|begin forwarded message:?|_{8,})$`)
	// Header tokens anchor to line start or preceding whitespace so prose
	// ("reply-to:", "mailto:") doesn't count as a quoted-header field.
	hdrToRE       = regexp.MustCompile(`(?im)(^|\s)to:`)
	hdrWhenRE     = regexp.MustCompile(`(?im)(^|\s)(date|sent):`)
	hdrSubjectRE  = regexp.MustCompile(`(?im)(^|\s)subject:`)
	replyPrefixRE = regexp.MustCompile(`^((re|fwd?):\s*)+`)
)

// parseEmailBody splits a normalized email body into the subject line the
// connector prepended, the new (visible) text, and the quoted history.
// Either text part may be empty; quoted is "" when no quote marker is found.
func parseEmailBody(body string) (subject, visible, quoted string) {
	rest := strings.ReplaceAll(body, "\r\n", "\n")
	rest = strings.ReplaceAll(rest, "\r", "\n")
	if after, ok := strings.CutPrefix(rest, "Subject: "); ok {
		line, tail, _ := strings.Cut(after, "\n")
		subject = strings.TrimSpace(line)
		rest = tail
	}
	lines := strings.Split(rest, "\n")
	cut := quoteStart(lines)
	if cut < 0 {
		return subject, tidy(lines), ""
	}
	return subject, tidy(lines[:cut]), tidy(lines[cut:])
}

// quoteStart returns the index of the first line that begins the quoted /
// forwarded part of an email, or -1.
func quoteStart(lines []string) int {
	// Index of the last non-blank line that is not ">"-quoted: a ">" line
	// after it starts the trailing quote block. Inline replies (text after
	// quoted lines) are left alone.
	lastText := -1
	for i, raw := range lines {
		l := strings.TrimSpace(raw)
		if l != "" && !strings.HasPrefix(l, ">") {
			lastText = i
		}
	}
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if dividerRE.MatchString(line) {
			return i
		}
		// Attribution ("On <date>, <sender> wrote:"), possibly wrapped
		// across up to three lines. Requiring a digit (the date) keeps
		// ordinary sentences like "On reflection, ... wrote:" visible.
		if strings.HasPrefix(line, "On ") {
			for j := i; j < len(lines) && j < i+3; j++ {
				if !strings.HasSuffix(strings.TrimSpace(lines[j]), "wrote:") {
					continue
				}
				if strings.ContainsAny(strings.Join(lines[i:j+1], " "), "0123456789") {
					return i
				}
				break
			}
		}
		// Embedded header block of a quoted message: a From: line with
		// To:/Date:(or Sent:)/Subject: on the same or next few lines.
		if len(line) > 5 && strings.EqualFold(line[:5], "from:") {
			window := strings.Join(lines[i:min(i+7, len(lines))], "\n")
			if hdrToRE.MatchString(window) && hdrWhenRE.MatchString(window) && hdrSubjectRE.MatchString(window) {
				return i
			}
		}
		if strings.HasPrefix(line, ">") && i > lastText {
			return i
		}
	}
	return -1
}

// tidy trims trailing whitespace (including non-breaking spaces) from every
// line and collapses blank-line runs so pre-wrap rendering stays compact.
func tidy(lines []string) string {
	var b strings.Builder
	blanks := 0
	for _, l := range lines {
		l = strings.TrimRightFunc(l, unicode.IsSpace)
		if l == "" {
			blanks++
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
			if blanks > 0 {
				b.WriteString("\n")
			}
		}
		blanks = 0
		b.WriteString(l)
	}
	return b.String()
}

// normalizeSubject lowercases and strips reply/forward prefixes so a thread's
// unchanged subject isn't repeated on every message.
func normalizeSubject(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.TrimSpace(replyPrefixRE.ReplaceAllString(s, ""))
}
