package importer

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var reOptionCol = regexp.MustCompile(`^option\s*(\d+)$`)

// CSVTemplate returns a header plus example rows for the CSV format.
func CSVTemplate() string {
	return `prompt,kind,option1,option2,option3,option4,correct,feedback,points
"Which fire extinguisher class is for electrical fires?",single,Class A,Class C,Class D,Water,2,"Class C covers energised electrical equipment.",1
"Which of these are emergency exits? (select all)",multiple,Door 1,Door 2,Window,Lift,"1;2",,2
`
}

// ParseCSV parses a spreadsheet export with a header row. Required
// columns: prompt, option1..optionN, correct (1-based option indexes
// separated by comma or semicolon). Optional: kind (single|multiple;
// inferred from the number of correct answers when empty), feedback,
// points. Column order is free; a semicolon delimiter is detected
// automatically.
func ParseCSV(r io.Reader) ([]Question, []Warning, error) {
	src, err := readAll(r)
	if err != nil {
		return nil, nil, err
	}
	cr := csv.NewReader(strings.NewReader(src))
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	firstLine, _, _ := strings.Cut(src, "\n")
	if strings.Count(firstLine, ";") > strings.Count(firstLine, ",") {
		cr.Comma = ';'
	}

	header, err := cr.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("read CSV header: %w", err)
	}
	cols := map[string]int{}
	type optCol struct{ n, idx int }
	var optCols []optCol
	for i, h := range header {
		h = strings.ToLower(strings.TrimSpace(h))
		if m := reOptionCol.FindStringSubmatch(h); m != nil {
			n, _ := strconv.Atoi(m[1])
			optCols = append(optCols, optCol{n, i})
			continue
		}
		cols[h] = i
	}
	sort.Slice(optCols, func(a, b int) bool { return optCols[a].n < optCols[b].n })
	var missing []string
	for _, req := range []string{"prompt", "correct"} {
		if _, ok := cols[req]; !ok {
			missing = append(missing, req)
		}
	}
	if len(optCols) == 0 {
		missing = append(missing, "option1..optionN")
	}
	if len(missing) > 0 {
		return nil, nil, fmt.Errorf("CSV header is missing column(s): %s", strings.Join(missing, ", "))
	}

	var (
		out   []Question
		warns []Warning
	)
	cell := func(rec []string, name string) string {
		if i, ok := cols[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		// Index by source line (minus the header) so warnings match the
		// spreadsheet row even though encoding/csv skips blank lines.
		var row int
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				row = pe.StartLine - 1
			}
			warns = append(warns, Warning{Index: row, Message: "unreadable row: " + err.Error()})
			continue
		}
		line, _ := cr.FieldPos(0)
		row = line - 1
		if isBlankRecord(rec) {
			continue
		}
		q := Question{
			Prompt:   plainText(cell(rec, "prompt")),
			Feedback: plainText(cell(rec, "feedback")),
			Points:   1,
		}
		if p := cell(rec, "points"); p != "" {
			n, err := strconv.Atoi(p)
			if err != nil || n < 1 {
				warns = append(warns, Warning{Index: row, Message: fmt.Sprintf("invalid points %q", p)})
				continue
			}
			q.Points = n
		}
		// Options keep their column position so "correct" indexes refer to
		// optionN even when earlier option cells are empty.
		labels := make([]string, len(optCols))
		for i, oc := range optCols {
			if oc.idx < len(rec) {
				labels[i] = strings.TrimSpace(rec[oc.idx])
			}
		}
		correctSet, reason := parseCorrect(cell(rec, "correct"), labels)
		if reason == "" {
			for i, l := range labels {
				q.Options = append(q.Options, Option{Label: l, Correct: correctSet[i]})
			}
			switch kind := strings.ToLower(cell(rec, "kind")); kind {
			case KindSingle, KindMultiple:
				q.Kind = kind
			case "":
				q.Kind = KindSingle
				if len(correctSet) > 1 {
					q.Kind = KindMultiple
				}
			default:
				reason = fmt.Sprintf("unknown kind %q (use single or multiple)", kind)
			}
		}
		if reason == "" {
			reason = validate(&q)
		}
		if reason != "" {
			warns = append(warns, Warning{Index: row, Message: reason})
			continue
		}
		out = append(out, q)
	}
	return out, warns, nil
}

func parseCorrect(s string, labels []string) (map[int]bool, string) {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
	if len(parts) == 0 {
		return nil, "correct column is empty"
	}
	set := map[int]bool{}
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > len(labels) {
			return nil, fmt.Sprintf("invalid correct option %q", p)
		}
		if labels[n-1] == "" {
			return nil, fmt.Sprintf("correct option %d is empty", n)
		}
		set[n-1] = true
	}
	return set, ""
}

func isBlankRecord(rec []string) bool {
	for _, c := range rec {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}
