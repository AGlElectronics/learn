package importer

import (
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

type mQuiz struct {
	Questions []mQuestion `xml:"question"`
}

type mQuestion struct {
	Type            string    `xml:"type,attr"`
	Name            mText     `xml:"name"`
	QuestionText    mText     `xml:"questiontext"`
	GeneralFeedback mText     `xml:"generalfeedback"`
	DefaultGrade    string    `xml:"defaultgrade"`
	Single          string    `xml:"single"`
	Answers         []mAnswer `xml:"answer"`
}

type mText struct {
	Format string  `xml:"format,attr"`
	Text   string  `xml:"text"`
	Files  []mFile `xml:"file"`
}

type mFile struct {
	Name     string `xml:"name,attr"`
	Encoding string `xml:"encoding,attr"`
	Data     string `xml:",chardata"`
}

type mAnswer struct {
	Fraction string `xml:"fraction,attr"`
	Format   string `xml:"format,attr"`
	Text     string `xml:"text"`
}

// ParseMoodleXML parses a Moodle XML question export. Supported types:
// multichoice (single and multiple), multichoiceset and truefalse.
// Category pseudo-questions are ignored; every other type is skipped with
// a warning. Base64 <file> elements under <questiontext> become Images.
func ParseMoodleXML(r io.Reader) ([]Question, []Warning, error) {
	var quiz mQuiz
	if err := xml.NewDecoder(r).Decode(&quiz); err != nil {
		return nil, nil, fmt.Errorf("parse Moodle XML: %w", err)
	}
	var (
		out   []Question
		warns []Warning
		index int
	)
	for _, mq := range quiz.Questions {
		if mq.Type == "category" {
			continue
		}
		index++
		label := strings.TrimSpace(mq.Name.Text)
		warn := func(msg string) {
			if label != "" {
				msg = fmt.Sprintf("%q: %s", label, msg)
			}
			warns = append(warns, Warning{Index: index, Message: msg})
		}

		q := Question{
			Prompt:   moodleText(mq.QuestionText.Format, mq.QuestionText.Text),
			Feedback: moodleText(mq.GeneralFeedback.Format, mq.GeneralFeedback.Text),
			Points:   moodleGrade(mq.DefaultGrade),
		}
		switch mq.Type {
		case "multichoice", "multichoiceset":
			single := mq.Type == "multichoice" && isTrue(mq.Single)
			q.Kind = KindMultiple
			if single {
				q.Kind = KindSingle
			}
			max := math.Inf(-1)
			fractions := make([]float64, len(mq.Answers))
			for i, a := range mq.Answers {
				fractions[i] = parseFraction(a.Fraction)
				max = math.Max(max, fractions[i])
			}
			for i, a := range mq.Answers {
				correct := fractions[i] > 0
				if single {
					correct = max > 0 && fractions[i] == max
				}
				q.Options = append(q.Options, Option{Label: moodleText(a.Format, a.Text), Correct: correct})
			}
		case "truefalse":
			q.Kind = KindSingle
			for _, a := range mq.Answers {
				lbl := strings.TrimSpace(moodleText(a.Format, a.Text))
				switch strings.ToLower(lbl) {
				case "true":
					lbl = "True"
				case "false":
					lbl = "False"
				}
				q.Options = append(q.Options, Option{Label: lbl, Correct: parseFraction(a.Fraction) > 0})
			}
		default:
			warn(fmt.Sprintf("question type %q is not supported", mq.Type))
			continue
		}

		for _, f := range mq.QuestionText.Files {
			if !strings.EqualFold(f.Encoding, "base64") {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(stripSpace(f.Data))
			if err != nil {
				warn(fmt.Sprintf("image %q could not be decoded", f.Name))
				continue
			}
			q.Images = append(q.Images, Image{Filename: f.Name, ContentType: contentTypeFor(f.Name), Data: data})
		}

		if reason := validate(&q); reason != "" {
			warn(reason)
			continue
		}
		out = append(out, q)
	}
	return out, warns, nil
}

func moodleText(format, s string) string {
	if format == "html" || looksLikeHTML(s) {
		return htmlToText(s)
	}
	return plainText(s)
}

func moodleGrade(s string) int {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f < 1 {
		return 1
	}
	return int(math.Round(f))
}

func parseFraction(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

func isTrue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes":
		return true
	}
	return false
}

func stripSpace(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\n', '\r', '\t':
			return -1
		}
		return r
	}, s)
}
