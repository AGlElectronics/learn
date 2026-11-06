package importer

import (
	"fmt"
	"io"
	"regexp"
	"strings"
)

var (
	reAikenOption = regexp.MustCompile(`^([A-Za-z])[.)]\s+(.*)$`)
	reAikenAnswer = regexp.MustCompile(`^ANSWER:\s*(.*)$`)
)

// ParseAiken parses the Aiken format: question text (may span lines),
// options "A) ..." or "A. ...", then "ANSWER: B". As an extension,
// "ANSWER: A, C" imports a multiple-choice question.
func ParseAiken(r io.Reader) ([]Question, []Warning, error) {
	src, err := readAll(r)
	if err != nil {
		return nil, nil, err
	}
	var (
		out     []Question
		warns   []Warning
		index   int
		prompt  []string
		letters []string
		q       Question
		broken  string
	)
	reset := func() {
		prompt, letters, q, broken = nil, nil, Question{Points: 1}, ""
	}
	reset()
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if m := reAikenAnswer.FindStringSubmatch(line); m != nil {
			index++
			q.Prompt = normalizeText(strings.Join(prompt, "\n"))
			reason := broken
			if reason == "" {
				reason = applyAikenAnswer(&q, letters, m[1])
			}
			if reason == "" {
				reason = validate(&q)
			}
			if reason != "" {
				warns = append(warns, Warning{Index: index, Message: reason})
			} else {
				out = append(out, q)
			}
			reset()
			continue
		}
		if m := reAikenOption.FindStringSubmatch(line); m != nil && len(prompt) > 0 {
			letters = append(letters, strings.ToUpper(m[1]))
			q.Options = append(q.Options, Option{Label: normalizeText(m[2])})
			continue
		}
		if len(q.Options) > 0 {
			if broken == "" {
				broken = fmt.Sprintf("unexpected line after options: %q", line)
			}
			continue
		}
		prompt = append(prompt, line)
	}
	if len(prompt) > 0 || len(q.Options) > 0 {
		warns = append(warns, Warning{Index: index + 1, Message: "missing ANSWER line"})
	}
	return out, warns, nil
}

func applyAikenAnswer(q *Question, letters []string, answer string) string {
	parts := strings.FieldsFunc(strings.ToUpper(answer), func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
	if len(parts) == 0 {
		return "ANSWER line is empty"
	}
	for _, p := range parts {
		found := false
		for i, l := range letters {
			if l == p {
				q.Options[i].Correct = true
				found = true
			}
		}
		if !found {
			return fmt.Sprintf("ANSWER %q does not match any option", p)
		}
	}
	q.Kind = KindSingle
	if len(parts) > 1 {
		q.Kind = KindMultiple
	}
	return ""
}
