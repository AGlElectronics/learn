package learn

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func rect(x, y, w, h float64, correct bool) QuestionRegion {
	return QuestionRegion{Shape: ShapeRect, Coords: map[string]float64{"x": x, "y": y, "w": w, "h": h}, Correct: correct}
}

func circle(cx, cy, rx, ry float64, correct bool) QuestionRegion {
	return QuestionRegion{Shape: ShapeCircle, Coords: map[string]float64{"cx": cx, "cy": cy, "rx": rx, "ry": ry}, Correct: correct}
}

func TestMarkHotspot(t *testing.T) {
	// Two extinguishers (correct) and a fire alarm (a distractor region).
	regions := []QuestionRegion{
		rect(0.1, 0.1, 0.2, 0.2, true),
		circle(0.7, 0.7, 0.1, 0.05, true),
		rect(0.5, 0.1, 0.1, 0.1, false),
	}
	cases := []struct {
		name string
		taps []Point
		want bool
	}{
		{"one tap in each correct region", []Point{{0.2, 0.2}, {0.7, 0.7}}, true},
		{"order does not matter", []Point{{0.75, 0.72}, {0.11, 0.29}}, true},
		{"edges count as inside", []Point{{0.1, 0.1}, {0.8, 0.7}}, true},
		{"no taps", nil, false},
		{"a correct region missed", []Point{{0.2, 0.2}}, false},
		{"two taps in one correct region", []Point{{0.15, 0.15}, {0.25, 0.25}, {0.7, 0.7}}, false},
		{"extra tap outside all regions", []Point{{0.2, 0.2}, {0.7, 0.7}, {0.95, 0.05}}, false},
		{"extra tap in a distractor region", []Point{{0.2, 0.2}, {0.7, 0.7}, {0.55, 0.15}}, false},
		{"inside the circle's bounding box but outside the ellipse", []Point{{0.2, 0.2}, {0.79, 0.749}}, false},
	}
	for _, c := range cases {
		if got := markHotspot(regions, c.taps); got != c.want {
			t.Errorf("%s: markHotspot = %v, want %v", c.name, got, c.want)
		}
	}
	if markHotspot([]QuestionRegion{rect(0, 0, 1, 1, false)}, []Point{{0.5, 0.5}}) {
		t.Error("a question without correct regions must never be right")
	}
	// Overlapping correct regions: one tap in the overlap counts for both.
	overlap := []QuestionRegion{rect(0, 0, 0.5, 0.5, true), rect(0.25, 0.25, 0.5, 0.5, true)}
	if !markHotspot(overlap, []Point{{0.3, 0.3}}) {
		t.Error("one tap in an overlap should satisfy both overlapping regions")
	}
}

func TestMaxTapsAndValidateTaps(t *testing.T) {
	regions := []QuestionRegion{rect(0, 0, 0.1, 0.1, true), rect(0.5, 0.5, 0.1, 0.1, true), rect(0.8, 0.8, 0.1, 0.1, false)}
	if n := maxTaps(nil, regions); n != 2 {
		t.Fatalf("default max taps = %d, want 2 (one per correct region)", n)
	}
	three := 3
	if n := maxTaps(&three, regions); n != 3 {
		t.Fatalf("explicit max taps = %d, want 3", n)
	}
	if err := validateTaps([]Point{{0.1, 0.1}, {0.2, 0.2}, {0.3, 0.3}}, 2); err == nil {
		t.Error("three taps with a limit of two should fail")
	}
	if err := validateTaps([]Point{{1.2, 0.1}}, 2); err == nil {
		t.Error("a tap outside the image should fail")
	}
	if err := validateTaps([]Point{{0, 1}}, 1); err != nil {
		t.Errorf("a tap on the edge is fine: %v", err)
	}
}

func TestNormalizeRegion(t *testing.T) {
	ok := []RegionInput{
		{Shape: ShapeRect, Coords: map[string]float64{"x": 0, "y": 0, "w": 1, "h": 1}},
		{Shape: ShapeCircle, Coords: map[string]float64{"cx": 0.5, "cy": 0.5, "r": 0.2}},
		{Shape: ShapeCircle, Coords: map[string]float64{"cx": 0.5, "cy": 0.5, "rx": 0.2, "ry": 0.1, "junk": 9}},
	}
	for _, in := range ok {
		out, err := normalizeRegion(in)
		if err != nil {
			t.Fatalf("normalizeRegion(%v): %v", in, err)
		}
		if _, junk := out.Coords["junk"]; junk || len(out.Coords) != 4 {
			t.Fatalf("normalizeRegion(%v) kept wrong keys: %v", in, out.Coords)
		}
	}
	bad := []RegionInput{
		{Shape: "polygon", Coords: map[string]float64{"x": 0}},
		{Shape: ShapeRect, Coords: map[string]float64{"x": 0.5, "y": 0.5, "w": 0.6, "h": 0.1}},
		{Shape: ShapeRect, Coords: map[string]float64{"x": 0.1, "y": 0.1, "w": 0, "h": 0.1}},
		{Shape: ShapeRect, Coords: map[string]float64{"x": 0.1, "y": 0.1}},
		{Shape: ShapeCircle, Coords: map[string]float64{"cx": 1.5, "cy": 0.5, "r": 0.1}},
		{Shape: ShapeCircle, Coords: map[string]float64{"cx": 0.5, "cy": 0.5}},
	}
	for _, in := range bad {
		if _, err := normalizeRegion(in); err == nil {
			t.Errorf("normalizeRegion(%v) should fail", in)
		}
	}
}

func TestValidateHotspotQuestion(t *testing.T) {
	img := &MediaRef{Kind: "image"}
	audio := &MediaRef{Kind: "audio"}
	one := 1
	cases := []struct {
		name string
		q    Question
		ok   bool
	}{
		{"valid", Question{Kind: "hotspot", Media: img, Regions: []QuestionRegion{rect(0, 0, 0.1, 0.1, true)}}, true},
		{"no image", Question{Kind: "hotspot", Regions: []QuestionRegion{rect(0, 0, 0.1, 0.1, true)}}, false},
		{"audio instead of image", Question{Kind: "hotspot", Media: audio, Regions: []QuestionRegion{rect(0, 0, 0.1, 0.1, true)}}, false},
		{"no correct region", Question{Kind: "hotspot", Media: img, Regions: []QuestionRegion{rect(0, 0, 0.1, 0.1, false)}}, false},
		{"fewer taps than correct regions", Question{Kind: "hotspot", Media: img, HotspotClicks: &one,
			Regions: []QuestionRegion{rect(0, 0, 0.1, 0.1, true), rect(0.5, 0.5, 0.1, 0.1, true)}}, false},
	}
	for _, c := range cases {
		if err := validateQuestion(c.q); (err == nil) != c.ok {
			t.Errorf("%s: validateQuestion err = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestOptionLabelOptionalWithPicture(t *testing.T) {
	pic := uuid.New()
	opts := []QuestionOption{{Label: "", MediaID: &pic, Correct: true}, {Label: "Red", Correct: false}}
	if err := validateOptions("single", opts); err != nil {
		t.Fatalf("an option with a picture needs no label: %v", err)
	}
	opts[0].MediaID = nil
	if err := validateOptions("single", opts); err == nil {
		t.Fatal("an option with neither label nor picture must fail")
	}
}

func TestEmbedURL(t *testing.T) {
	good := map[string]string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ":        "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ?t=10":                  "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ",
		"https://m.youtube.com/shorts/dQw4w9WgXcQ":           "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ",
		"https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ": "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ",
		"https://vimeo.com/76979871":                         "https://player.vimeo.com/video/76979871?dnt=1",
		"https://player.vimeo.com/video/76979871":            "https://player.vimeo.com/video/76979871?dnt=1",
	}
	for in, want := range good {
		got, _, err := EmbedURL(in)
		if err != nil || got != want {
			t.Errorf("EmbedURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"https://evil.example/watch?v=dQw4w9WgXcQ",
		"https://youtube.com.evil.example/watch?v=dQw4w9WgXcQ",
		"javascript:alert(1)",
		"https://www.youtube.com/watch",
		"https://vimeo.com/channels/staffpicks",
		"ftp://youtu.be/dQw4w9WgXcQ",
	} {
		if _, _, err := EmbedURL(in); err == nil {
			t.Errorf("EmbedURL(%q) should be refused", in)
		}
	}
}

func TestMediaKindFor(t *testing.T) {
	cases := map[string]string{
		"image/png": "image", "IMAGE/JPEG": "image", "audio/mpeg": "audio", "audio/ogg; codecs=opus": "audio",
		"video/webm": "video", "image/svg+xml": "", "application/pdf": "", "": "",
	}
	for in, want := range cases {
		if got := MediaKindFor(in); got != want {
			t.Errorf("MediaKindFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsOverdue(t *testing.T) {
	now := time.Now()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	if !isOverdue(&past, Progress{Total: 2, Completed: 1}, now) {
		t.Error("past due with work left is overdue")
	}
	if isOverdue(&past, Progress{Total: 2, Completed: 2}, now) {
		t.Error("finished courses are never overdue")
	}
	if isOverdue(&future, Progress{Total: 2}, now) || isOverdue(nil, Progress{Total: 2}, now) {
		t.Error("future or no due date is not overdue")
	}
}
