// Package importer parses question banks exported from other tools (Moodle
// XML, GIFT, Aiken, CSV) into Learn's choice-question shape. Parsers are
// pure: no database, no storage. Items Learn cannot represent are skipped
// and reported as Warnings rather than failing the whole import; an error
// is returned only when the input as a whole is unreadable.
package importer

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"path"
	"strings"
)

// Question kinds.
const (
	KindSingle   = "single"
	KindMultiple = "multiple"
)

// Question is one imported choice question.
type Question struct {
	Kind     string // KindSingle or KindMultiple
	Prompt   string // plain text, line breaks preserved
	Feedback string // general feedback shown after answering
	Points   int    // >= 1
	Options  []Option
	Images   []Image // images embedded in the prompt (Moodle XML only)
}

// Option is one answer choice.
type Option struct {
	Label   string
	Correct bool
}

// Image is an embedded file attached to the question prompt.
type Image struct {
	Filename    string
	ContentType string
	Data        []byte
}

// Warning reports a skipped or partially imported item. Index is the
// 1-based position of the item in the source (for CSV: the source line
// minus the header line, so it matches the spreadsheet data row).
type Warning struct {
	Index   int
	Message string
}

// Format identifies a supported input format.
type Format string

const (
	FormatMoodleXML Format = "moodle"
	FormatGIFT      Format = "gift"
	FormatAiken     Format = "aiken"
	FormatCSV       Format = "csv"
)

// Parse dispatches to the parser for format.
func Parse(format Format, r io.Reader) ([]Question, []Warning, error) {
	switch format {
	case FormatMoodleXML:
		return ParseMoodleXML(r)
	case FormatGIFT:
		return ParseGIFT(r)
	case FormatAiken:
		return ParseAiken(r)
	case FormatCSV:
		return ParseCSV(r)
	}
	return nil, nil, fmt.Errorf("unsupported import format %q", format)
}

// DetectFormat guesses the format from the filename extension, falling
// back to sniffing the content. It returns "" when unsure.
func DetectFormat(filename string, data []byte) Format {
	switch strings.ToLower(path.Ext(filename)) {
	case ".xml":
		return FormatMoodleXML
	case ".gift":
		return FormatGIFT
	case ".csv":
		return FormatCSV
	}
	head := bytes.TrimSpace(bytes.TrimPrefix(data, utf8BOM))
	switch {
	case bytes.HasPrefix(head, []byte("<?xml")) || bytes.HasPrefix(head, []byte("<quiz")):
		return FormatMoodleXML
	case bytes.Contains(head, []byte("ANSWER:")):
		return FormatAiken
	case bytes.Contains(head, []byte("{")) && bytes.Contains(head, []byte("}")):
		return FormatGIFT
	}
	return ""
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func readAll(r io.Reader) (string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	b = bytes.TrimPrefix(b, utf8BOM)
	return strings.ReplaceAll(strings.ReplaceAll(string(b), "\r\n", "\n"), "\r", "\n"), nil
}

// validate checks the invariants every imported question must satisfy and
// returns a reason when it must be skipped.
func validate(q *Question) string {
	q.Prompt = strings.TrimSpace(q.Prompt)
	q.Feedback = strings.TrimSpace(q.Feedback)
	if q.Points < 1 {
		q.Points = 1
	}
	if q.Prompt == "" {
		return "question text is empty"
	}
	opts := q.Options[:0]
	for _, o := range q.Options {
		o.Label = strings.TrimSpace(o.Label)
		if o.Label != "" {
			opts = append(opts, o)
		}
	}
	q.Options = opts
	if len(q.Options) < 2 {
		return "needs at least two answer options"
	}
	correct := 0
	for _, o := range q.Options {
		if o.Correct {
			correct++
		}
	}
	if correct == 0 {
		return "no correct answer"
	}
	if q.Kind == KindSingle && correct > 1 {
		return "single-choice question has more than one correct answer"
	}
	return ""
}

func contentTypeFor(filename string) string {
	if ct := mime.TypeByExtension(strings.ToLower(path.Ext(filename))); ct != "" {
		if i := strings.IndexByte(ct, ';'); i >= 0 {
			ct = ct[:i]
		}
		return ct
	}
	return "application/octet-stream"
}
