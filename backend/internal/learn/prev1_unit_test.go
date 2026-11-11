package learn

import (
	"testing"
)

func TestNormalizeGroup(t *testing.T) {
	for in, want := range map[string]string{
		" /Learn/Cohort-A ": "learn/cohort-a",
		"staff":             "staff",
		"//double":          "/double",
		"":                  "",
		"/":                 "",
	} {
		if got := NormalizeGroup(in); got != want {
			t.Errorf("NormalizeGroup(%q) = %q, want %q", in, got, want)
		}
	}
	if got := normalizeGroups([]string{"/B", "b", " a ", ""}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("normalizeGroups = %v", got)
	}
}

func TestGroupLinkable(t *testing.T) {
	patterns := []string{"/learn/*", "/teams/red"}
	for path, want := range map[string]bool{
		"/learn/cohort-a": true,
		"/Learn/a/b":      true,
		"/learn":          false,
		"/learning/x":     false,
		"/teams/red":      true,
		"/teams/red/sub":  false,
		"/teams/blue":     false,
		"cohort-a":        false,
		"":                false,
	} {
		if got := GroupLinkable(path, patterns); got != want {
			t.Errorf("GroupLinkable(%q) = %v, want %v", path, got, want)
		}
	}
	if !GroupLinkable("/anything", []string{"*"}) || GroupLinkable("/x", nil) {
		t.Fatal("* allows everything, no patterns allow nothing")
	}
}

func TestMagicMatches(t *testing.T) {
	for _, tc := range []struct {
		ct   string
		head string
		want bool
	}{
		{"image/png", "\x89PNG\r\n\x1a\nrest", true},
		{"image/png", "GIF89a", false},
		{"image/jpeg", "\xff\xd8\xff\xe0", true},
		{"image/gif", "GIF87a....", true},
		{"image/webp", "RIFF\x00\x00\x00\x00WEBPVP8 ", true},
		{"image/webp", "RIFF\x00\x00\x00\x00WAVEfmt ", false},
		{"audio/mpeg", "ID3\x03", true},
		{"audio/mpeg", "\xff\xfb\x90", true},
		{"audio/mp4", "\x00\x00\x00\x20ftypM4A ", true},
		{"video/mp4", "\x00\x00\x00\x18ftypmp42", true},
		{"audio/ogg", "OggS\x00", true},
		{"video/webm", "\x1a\x45\xdf\xa3", true},
		{"video/webm", "<html>", false},
		{"text/html", "<html>", false},
		{"image/png", "", false},
	} {
		if got := magicMatches(tc.ct, []byte(tc.head)); got != tc.want {
			t.Errorf("magicMatches(%s, %q) = %v, want %v", tc.ct, tc.head, got, tc.want)
		}
	}
}

func TestDenRetention(t *testing.T) {
	if _, ok := DenRetention(map[string]any{}); ok {
		t.Fatal("no retention from Den")
	}
	r, ok := DenRetention(map[string]any{"retention": map[string]any{"inactive_user_months": float64(18)}})
	if !ok || r.InactiveUserMonths != 18 || r.AuditLogMonths != DefaultRetention.AuditLogMonths {
		t.Fatalf("partial Den retention = %+v %v", r, ok)
	}
	if _, ok := DenRetention(map[string]any{"retention": map[string]any{"audit_log_months": float64(-3)}}); ok {
		t.Fatal("invalid Den retention accepted")
	}
	if (RetentionSettings{InactiveUserMonths: 1201}).Validate() == nil || (RetentionSettings{}).Validate() != nil {
		t.Fatal("retention bounds")
	}
}

func TestDisplayPath(t *testing.T) {
	if displayPath("learn/a") != "/learn/a" || displayPath(" /x ") != "/x" {
		t.Fatal("displayPath")
	}
}
