package importer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// summary renders a question compactly: kind|prompt|options (* = correct)|points|feedback.
func summary(q Question) string {
	opts := make([]string, len(q.Options))
	for i, o := range q.Options {
		if o.Correct {
			opts[i] = "*" + o.Label
		} else {
			opts[i] = o.Label
		}
	}
	return fmt.Sprintf("%s|%s|%s|%d|%s", q.Kind, q.Prompt, strings.Join(opts, ","), q.Points, q.Feedback)
}

func TestParseSamples(t *testing.T) {
	tests := []struct {
		file      string
		format    Format
		questions []string
		warnings  []string // "index:substring"
	}{
		{
			file:   "moodle.xml",
			format: FormatMoodleXML,
			questions: []string{
				"single|Which extinguisher is for electrical fires?|Water,*CO2,Foam|2|CO2 & powder are safe.",
				"multiple|Select all exits:\nLook at the map\ncarefully.|*Door A,*Door B,Lift|1|",
				"single|Lifts may be used during a fire alarm.|True,*False|1|",
			},
			warnings: []string{
				`4:"Describe": question type "essay" is not supported`,
				`5:"Broken": no correct answer`,
				`6:"Short": question type "shortanswer" is not supported`,
			},
		},
		{
			file:   "sample.gift",
			format: FormatGIFT,
			questions: []string{
				"single|Which extinguisher is for electrical fires?|Water,*CO2,Foam|1|CO2 leaves no residue.",
				"multiple|Select all exits|*Door A,*Door B,Lift|1|",
				"single|Lifts may be used during a fire alarm.|True,*False|1|",
				"single|The assembly point is outside.|*True,False|1|",
				"single|What does 1 + 1 = ? Use the {braces} ~ literally|*2,3,1=1|1|",
				"multiple|Pick two:|*Red,*Blue,Green|1|",
			},
			warnings: []string{
				"7:short-answer questions are not supported",
				`8:"Describe": essay questions are not supported`,
				"9:numerical questions are not supported",
				"10:matching questions are not supported",
				"11:no answer block",
			},
		},
		{
			file:   "sample.aiken.txt",
			format: FormatAiken,
			questions: []string{
				"single|Which extinguisher is for electrical fires?|Water,*CO2,Foam|1|",
				"multiple|Select the exits\non the ground floor.|*Door A,*Door B,Lift|1|",
			},
			warnings: []string{
				`3:ANSWER "D" does not match any option`,
				"4:unexpected line after options",
				"5:missing ANSWER line",
			},
		},
		{
			file:   "sample.csv",
			format: FormatCSV,
			questions: []string{
				"single|Which extinguisher is for electrical fires?|Water,*CO2,Foam|2|CO2 is safe.",
				"multiple|Select all exits|*Door A,*Door B,Lift|1|",
				"multiple|Inferred multiple|*A,B,*C|1|",
			},
			warnings: []string{
				"4:more than one correct answer",
				`5:invalid correct option "5"`,
				"6:question text is empty",
				`8:unknown kind "essay"`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tt.file))
			if err != nil {
				t.Fatal(err)
			}
			if got := DetectFormat(tt.file, data); got != tt.format {
				t.Errorf("DetectFormat = %q, want %q", got, tt.format)
			}
			qs, warns, err := Parse(tt.format, bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if len(qs) != len(tt.questions) {
				for _, q := range qs {
					t.Logf("got question: %q", summary(q))
				}
				t.Fatalf("got %d questions, want %d (warnings: %+v)", len(qs), len(tt.questions), warns)
			}
			for i, want := range tt.questions {
				if got := summary(qs[i]); got != want {
					t.Errorf("question %d:\n got %q\nwant %q", i, got, want)
				}
			}
			if len(warns) != len(tt.warnings) {
				t.Fatalf("got warnings %+v, want %v", warns, tt.warnings)
			}
			for i, want := range tt.warnings {
				idx, sub, _ := strings.Cut(want, ":")
				if got := fmt.Sprint(warns[i].Index); got != idx || !strings.Contains(warns[i].Message, sub) {
					t.Errorf("warning %d = %d:%q, want %s", i, warns[i].Index, warns[i].Message, want)
				}
			}
		})
	}
}

func TestMoodleImages(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "moodle.xml"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	qs, _, err := ParseMoodleXML(f)
	if err != nil {
		t.Fatal(err)
	}
	imgs := qs[0].Images
	if len(imgs) != 1 || imgs[0].Filename != "sign.png" || imgs[0].ContentType != "image/png" {
		t.Fatalf("images = %+v", imgs)
	}
	if !bytes.HasPrefix(imgs[0].Data, []byte("\x89PNG")) {
		t.Fatalf("image data not decoded: % x", imgs[0].Data[:8])
	}
	if len(qs[1].Images) != 0 {
		t.Fatal("unexpected images on second question")
	}
}

func TestMoodleInvalidXML(t *testing.T) {
	if _, _, err := ParseMoodleXML(strings.NewReader("<quiz><question")); err == nil {
		t.Fatal("want error")
	}
}

func TestHTMLToText(t *testing.T) {
	tests := []struct{ in, want string }{
		{"<p>One</p><p>Two</p>", "One\nTwo"},
		{"a<br>b<br/>c", "a\nb\nc"},
		{"<ul><li>x</li><li>y</li></ul>", "- x\n- y"},
		{"  spaced \n  out  &nbsp;text ", "spaced out text"},
		{"<script>alert(1)</script>Safe &lt;b&gt;", "Safe <b>"},
		{"<p>a</p><p></p><p></p><p>b</p>", "a\n\nb"},
	}
	for _, tt := range tests {
		if got := htmlToText(tt.in); got != tt.want {
			t.Errorf("htmlToText(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGIFTEscapes(t *testing.T) {
	tests := []struct{ in, want string }{
		{`a\~b\=c\#d\{e\}f\:g\\h`, `a~b=c#d{e}f:g\h`},
		{`line1\nline2`, "line1\nline2"},
		{`plain`, "plain"},
	}
	for _, tt := range tests {
		if got := giftUnescape(tt.in); got != tt.want {
			t.Errorf("giftUnescape(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCSVTemplateRoundTrip(t *testing.T) {
	qs, warns, err := ParseCSV(strings.NewReader(CSVTemplate()))
	if err != nil || len(warns) != 0 || len(qs) != 2 {
		t.Fatalf("template: %d questions, warnings %+v, err %v", len(qs), warns, err)
	}
	if qs[1].Kind != KindMultiple || qs[1].Points != 2 {
		t.Fatalf("second template row = %q", summary(qs[1]))
	}
}

func TestCSVSemicolonDelimiterAndBOM(t *testing.T) {
	src := "\xEF\xBB\xBFPrompt;Option1;Option2;Correct\r\nIs it?;Yes;No;1\r\n"
	qs, warns, err := ParseCSV(strings.NewReader(src))
	if err != nil || len(warns) != 0 || len(qs) != 1 {
		t.Fatalf("got %d questions, warnings %+v, err %v", len(qs), warns, err)
	}
	if got := summary(qs[0]); got != "single|Is it?|*Yes,No|1|" {
		t.Fatalf("got %q", got)
	}
}

func TestCSVMissingColumns(t *testing.T) {
	_, _, err := ParseCSV(strings.NewReader("prompt,kind\nq,single\n"))
	if err == nil || !strings.Contains(err.Error(), "correct") || !strings.Contains(err.Error(), "option1") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseUnknownFormat(t *testing.T) {
	if _, _, err := Parse("docx", strings.NewReader("")); err == nil {
		t.Fatal("want error")
	}
}
