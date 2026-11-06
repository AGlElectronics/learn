package learn

import (
	"reflect"
	"testing"
)

func TestNormalizeLang(t *testing.T) {
	for raw, want := range map[string]string{
		"nl": "nl", " NL-be ": "nl", "en_GB": "en", "fil": "fil", "": "", "*": "", "english": "", "n1": "",
	} {
		if got := NormalizeLang(raw); got != want {
			t.Errorf("NormalizeLang(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestParseAcceptLanguage(t *testing.T) {
	got := ParseAcceptLanguage("de;q=0.3, nl-NL, fr;q=0, en;q=0.8, *;q=0.1, nl;q=0.9")
	want := []string{"nl", "nl", "en", "de"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := ParseAcceptLanguage(""); len(got) != 0 {
		t.Fatalf("empty header: %v", got)
	}
}

func TestLangResolutionOrder(t *testing.T) {
	course := Course{DefaultLang: "en", Languages: []string{"en", "nl", "de"}}
	cases := []struct {
		name                         string
		override, pref, accept, want string
	}{
		{"nothing asked: default", "", "", "", "en"},
		{"accept-language", "", "", "nl-NL,en;q=0.5", "nl"},
		{"accept-language order by q", "", "", "en;q=0.2, de;q=0.9", "de"},
		{"preference beats accept-language", "", "de", "nl", "de"},
		{"override beats preference", "nl", "de", "en", "nl"},
		{"unsupported override falls through to the preference", "fr", "de", "nl", "de"},
		{"unsupported preference falls through to accept-language", "", "fr", "nl", "nl"},
		{"nothing supported: default", "fr", "es", "it, pt", "en"},
		{"invalid codes are ignored", "!!", "", "nl", "nl"},
	}
	for _, c := range cases {
		if got := course.LangFor(NewLangs(c.override, c.pref, c.accept)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	if got := (Course{DefaultLang: "nl", Languages: []string{"nl"}}).LangFor(nil); got != "nl" {
		t.Errorf("nil langs: got %q", got)
	}
}

func TestNewLangsDropsDuplicates(t *testing.T) {
	got := NewLangs("nl", "nl-BE", "nl, en, en-GB")
	if want := (Langs{"nl", "en"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTodoTextFallsBackToEnglish(t *testing.T) {
	if got := todoText(Langs{"de", "nl"}, "finish", "Brand"); got != "Rond Brand af" {
		t.Errorf("nl: %q", got)
	}
	if got := todoText(Langs{"de"}, "finish", "Fire"); got != "Finish Fire" {
		t.Errorf("fallback: %q", got)
	}
}
