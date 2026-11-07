package learn

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCheckinCodeWindows(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	slot := uuid.New()
	base := time.Unix(1_800_000_000, 0) // a multiple of 30 s: start of a window
	code := checkinCodeFor(secret, slot, checkinWindow(base))

	if len(code) != 8 {
		t.Fatalf("code %q: want 8 characters", code)
	}
	cases := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"same window", base.Add(29 * time.Second), true},
		{"previous window still accepted", base.Add(30 * time.Second), true},
		{"previous window, last second", base.Add(59 * time.Second), true},
		{"two windows later", base.Add(60 * time.Second), false},
		{"future code not accepted early", base.Add(-1 * time.Second), false},
	}
	for _, c := range cases {
		if got := validCheckinCode(secret, slot, code, c.at); got != c.want {
			t.Errorf("%s: valid = %v, want %v", c.name, got, c.want)
		}
	}
	if !validCheckinCode(secret, slot, "  "+strings.ToLower(code)+" ", base) {
		t.Error("code should be case- and space-insensitive")
	}
	if validCheckinCode(secret, uuid.New(), code, base) {
		t.Error("code must be bound to its slot")
	}
	if validCheckinCode([]byte("other secret"), slot, code, base) {
		t.Error("code must be bound to the slot secret")
	}
	if validCheckinCode(secret, slot, "", base) {
		t.Error("empty code accepted")
	}
}

func TestCheckinOpen(t *testing.T) {
	start := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	cases := []struct {
		at   time.Time
		want bool
	}{
		{start.Add(-31 * time.Minute), false},
		{start.Add(-30 * time.Minute), true},
		{start, true},
		{end, true},
		{end.Add(time.Second), false},
	}
	for _, c := range cases {
		if got := checkinOpen(start, end, c.at); got != c.want {
			t.Errorf("checkinOpen at %s = %v, want %v", c.at.Format(time.Kitchen), got, c.want)
		}
	}
}
