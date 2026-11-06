package importer

import (
	"io"
	"regexp"
	"strconv"
	"strings"
)

// ParseGIFT parses Moodle GIFT text. Supported: multiple choice (`=` right,
// `~` wrong), weighted answers (`~%50%`, imported as multiple choice with
// every positive-weight answer correct), true/false (`{T}`/`{F}`), titles
// (`::title::`), `//` comment lines, `[html]`-style format markers, per-answer
// `#feedback` (dropped) and `####general feedback`. Short answer, matching,
// numeric, essay and descriptions are skipped with a warning.
func ParseGIFT(r io.Reader) ([]Question, []Warning, error) {
	src, err := readAll(r)
	if err != nil {
		return nil, nil, err
	}
	var (
		out   []Question
		warns []Warning
	)
	for i, item := range splitGIFTItems(src) {
		q, reason := parseGIFTItem(item)
		if reason == "" {
			reason = validate(&q)
		}
		if reason != "" {
			warns = append(warns, Warning{Index: i + 1, Message: reason})
			continue
		}
		out = append(out, q)
	}
	return out, warns, nil
}

// splitGIFTItems drops comment lines and $CATEGORY directives and splits on
// blank lines outside an answer block.
func splitGIFTItems(src string) []string {
	var (
		items []string
		cur   []string
		depth int
	)
	flush := func() {
		if s := strings.TrimSpace(strings.Join(cur, "\n")); s != "" {
			items = append(items, s)
		}
		cur = nil
	}
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if depth == 0 && strings.HasPrefix(trimmed, "$CATEGORY:") {
			flush()
			continue
		}
		if trimmed == "" && depth == 0 {
			flush()
			continue
		}
		cur = append(cur, line)
		depth += braceDelta(line)
		if depth < 0 {
			depth = 0
		}
	}
	flush()
	return items
}

func braceDelta(s string) int {
	d := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '{':
			d++
		case '}':
			d--
		}
	}
	return d
}

var reFormatMarker = regexp.MustCompile(`^\[(html|moodle|plain|markdown)\]`)

func parseGIFTItem(item string) (Question, string) {
	q := Question{Points: 1}
	text := item
	title := ""
	if strings.HasPrefix(text, "::") {
		if end := indexUnescaped(text[2:], "::"); end >= 0 {
			title = giftUnescape(strings.TrimSpace(text[2 : 2+end]))
			text = text[2+end+2:]
		}
	}

	open := indexUnescaped(text, "{")
	if open < 0 {
		return q, withTitle(title, "no answer block (description items are not supported)")
	}
	closeRel := indexUnescaped(text[open+1:], "}")
	if closeRel < 0 {
		return q, withTitle(title, "unterminated answer block")
	}
	block := text[open+1 : open+1+closeRel]
	prompt := strings.TrimSpace(text[:open])
	if after := strings.TrimSpace(text[open+1+closeRel+1:]); after != "" {
		prompt += " _____ " + after
	}

	isHTML := false
	if m := reFormatMarker.FindStringSubmatch(prompt); m != nil {
		isHTML = m[1] == "html"
		prompt = strings.TrimSpace(prompt[len(m[0]):])
	}
	q.Prompt = giftText(prompt, isHTML)
	if q.Prompt == "" {
		q.Prompt = title
	}

	block = strings.TrimSpace(block)
	if gf := indexUnescaped(block, "####"); gf >= 0 {
		q.Feedback = giftText(block[gf+4:], isHTML)
		block = strings.TrimSpace(block[:gf])
	}
	if block == "" {
		return q, withTitle(title, "essay questions are not supported")
	}
	if block[0] == '#' {
		return q, withTitle(title, "numerical questions are not supported")
	}

	head := block
	if fb := indexUnescaped(head, "#"); fb >= 0 {
		head = head[:fb]
	}
	switch strings.ToUpper(strings.TrimSpace(head)) {
	case "T", "TRUE":
		q.Kind = KindSingle
		q.Options = []Option{{Label: "True", Correct: true}, {Label: "False"}}
		return q, ""
	case "F", "FALSE":
		q.Kind = KindSingle
		q.Options = []Option{{Label: "True"}, {Label: "False", Correct: true}}
		return q, ""
	}

	answers, ok := splitGIFTAnswers(block)
	if !ok {
		return q, withTitle(title, "answer block is malformed")
	}
	tildes, correct, weighted := 0, 0, false
	for _, a := range answers {
		if indexUnescaped(a.text, "->") >= 0 {
			return q, withTitle(title, "matching questions are not supported")
		}
		if a.marker == '~' {
			tildes++
			if a.weight > 0 {
				weighted = true
			}
		}
	}
	if tildes == 0 {
		return q, withTitle(title, "short-answer questions are not supported")
	}
	for _, a := range answers {
		c := a.marker == '=' || a.weight > 0
		if c {
			correct++
		}
		q.Options = append(q.Options, Option{Label: giftText(a.text, isHTML), Correct: c})
	}
	q.Kind = KindSingle
	if weighted || correct > 1 {
		q.Kind = KindMultiple
	}
	return q, ""
}

type giftAnswer struct {
	marker byte
	weight float64
	text   string
}

var reWeight = regexp.MustCompile(`^%(-?\d+(?:\.\d+)?)%`)

// splitGIFTAnswers splits "=a#fb ~%50%b ~c" at unescaped = and ~ markers,
// dropping per-answer feedback.
func splitGIFTAnswers(block string) ([]giftAnswer, bool) {
	var (
		out   []giftAnswer
		start = -1
	)
	push := func(end int) {
		if start < 0 {
			return
		}
		a := giftAnswer{marker: block[start]}
		body := block[start+1 : end]
		if fb := indexUnescaped(body, "#"); fb >= 0 {
			body = body[:fb]
		}
		body = strings.TrimSpace(body)
		if m := reWeight.FindStringSubmatch(body); m != nil {
			a.weight, _ = strconv.ParseFloat(m[1], 64)
			body = strings.TrimSpace(body[len(m[0]):])
		} else if a.marker == '=' {
			a.weight = 100
		}
		a.text = body
		out = append(out, a)
	}
	for i := 0; i < len(block); i++ {
		switch c := block[i]; c {
		case '\\':
			i++
		case '=', '~':
			if start < 0 && strings.TrimSpace(block[:i]) != "" {
				return nil, false
			}
			push(i)
			start = i
		}
	}
	push(len(block))
	return out, len(out) > 0
}

func giftText(s string, isHTML bool) string {
	s = giftUnescape(strings.TrimSpace(s))
	if isHTML || looksLikeHTML(s) {
		return htmlToText(s)
	}
	return normalizeText(s)
}

// giftUnescape resolves \~ \= \# \{ \} \: \\ and \n.
func giftUnescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch n := s[i+1]; n {
			case '~', '=', '#', '{', '}', ':', '\\':
				b.WriteByte(n)
				i++
				continue
			case 'n':
				b.WriteByte('\n')
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// indexUnescaped is strings.Index ignoring backslash-escaped characters.
func indexUnescaped(s, sub string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if strings.HasPrefix(s[i:], sub) {
			return i
		}
	}
	return -1
}

func withTitle(title, msg string) string {
	if title == "" {
		return msg
	}
	return strconv.Quote(title) + ": " + msg
}
