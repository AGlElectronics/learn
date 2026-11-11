package learn

import (
	"encoding/json"
	"testing"

	"github.com/AGlElectronics/learn/backend/internal/denlink"
	"github.com/AGlElectronics/learn/backend/internal/groupmatch"
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
		if got := GroupLinkable(groupmatch.Config{}, path, patterns); got != want {
			t.Errorf("GroupLinkable(%q) = %v, want %v", path, got, want)
		}
	}
	if !GroupLinkable(groupmatch.Config{}, "/anything", []string{"*"}) || GroupLinkable(groupmatch.Config{}, "/x", nil) {
		t.Fatal("* allows everything, no patterns allow nothing")
	}

	// OIDC_GROUP_PREFIX is stripped from both sides; last mode still
	// compares whole paths for linkability.
	gm := groupmatch.Config{Prefix: "/con2026", Mode: groupmatch.ModeLast}
	for path, want := range map[string]bool{
		"/con2026/learn/cohort-a": true,
		"/learn/cohort-a":         true,
		"/con2026/teams/red":      true,
		"/con2026":                false,
		"/con2026/other/cohort-a": false,
	} {
		if got := GroupLinkable(gm, path, patterns); got != want {
			t.Errorf("prefixed GroupLinkable(%q) = %v, want %v", path, got, want)
		}
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

func denSettings(t *testing.T, data string) func() *denlink.ModuleSettings {
	t.Helper()
	var ms denlink.ModuleSettings
	if err := json.Unmarshal([]byte(data), &ms); err != nil {
		t.Fatal(err)
	}
	return func() *denlink.ModuleSettings { return &ms }
}

func TestDenManaged(t *testing.T) {
	s := &Service{}
	if len(s.DenManaged()) != 0 {
		t.Fatal("standalone: nothing managed in Den")
	}
	s.SetModuleSettings(func() *denlink.ModuleSettings { return nil })
	if len(s.DenManaged()) != 0 {
		t.Fatal("unpaired: nothing managed in Den")
	}
	s.SetModuleSettings(denSettings(t, `{"data":{"retention":{"inactive_users_days":365},"learn":{"audit_log_days":-3,"telegram_enabled":false}}}`))
	m := s.DenManaged()
	if !m[DenKeyInactiveUsersDays] || m[DenKeyAuditLogDays] || !m[DenKeyTelegramEnabled] {
		t.Fatalf("managed = %v (an invalid value counts as not set)", m)
	}
	if on, ok := s.DenTelegramEnabled(); !ok || on {
		t.Fatalf("Den telegram = %v %v", on, ok)
	}
	if (RetentionSettings{InactiveUsersDays: 36501}).Validate() == nil || (RetentionSettings{}).Validate() != nil {
		t.Fatal("retention bounds")
	}
}

func TestDisplayPath(t *testing.T) {
	if displayPath("learn/a") != "/learn/a" || displayPath(" /x ") != "/x" {
		t.Fatal("displayPath")
	}
}
