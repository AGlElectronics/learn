package importer

import (
	"html"
	"regexp"
	"strings"
)

var (
	reScriptStyle = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	reBreak       = regexp.MustCompile(`(?i)<br\s*/?>`)
	reBlockEnd    = regexp.MustCompile(`(?i)</(p|div|h[1-6]|tr|blockquote|pre|ul|ol|table)\s*>`)
	reListItem    = regexp.MustCompile(`(?i)<li(\s[^>]*)?>`)
	reTag         = regexp.MustCompile(`(?s)<[^>]*>`)
	reSpaces      = regexp.MustCompile(`[ \t\f\v]+`)
	reManyBlank   = regexp.MustCompile(`\n{3,}`)
)

// htmlToText converts an HTML fragment to plain text, turning block
// elements and <br> into line breaks and dropping all other markup.
// Source newlines are insignificant in HTML and collapse to spaces.
func htmlToText(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = reScriptStyle.ReplaceAllString(s, "")
	s = reBreak.ReplaceAllString(s, "\n")
	s = reBlockEnd.ReplaceAllString(s, "\n")
	s = reListItem.ReplaceAllString(s, "\n- ")
	s = reTag.ReplaceAllString(s, "")
	return normalizeText(html.UnescapeString(s))
}

// plainText tidies text that is not HTML: entities are decoded, stray tags
// removed, line breaks kept.
func plainText(s string) string {
	if strings.ContainsAny(s, "<") {
		s = reBreak.ReplaceAllString(s, "\n")
		s = reTag.ReplaceAllString(s, "")
	}
	return normalizeText(html.UnescapeString(s))
}

// normalizeText collapses runs of spaces, trims each line and limits blank
// lines to one.
func normalizeText(s string) string {
	s = strings.ReplaceAll(s, "\u00a0", " ")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(reSpaces.ReplaceAllString(l, " "))
	}
	s = strings.Join(lines, "\n")
	s = reManyBlank.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// looksLikeHTML reports whether s contains markup worth converting.
func looksLikeHTML(s string) bool {
	return strings.Contains(s, "<") && strings.Contains(s, ">")
}
