package mail

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNewSMTPDisabledWithoutHost(t *testing.T) {
	if s := NewSMTP(SMTPConfig{Host: "  "}); s != nil {
		t.Fatalf("want nil sender, got %+v", s)
	}
	var s *SMTP
	if err := s.Send(context.Background(), Message{To: "a@b.c"}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("nil sender err = %v", err)
	}
}

func TestRender(t *testing.T) {
	s := NewSMTP(SMTPConfig{Host: "mailpit", Port: 1025, From: "no-reply@conservices.local", FromName: "Con Services"})
	out := string(s.render(Message{To: "x@y.z", Subject: "Hi", Body: "line1\nline2"}))
	for _, want := range []string{"From: Con Services <no-reply@conservices.local>\r\n", "To: x@y.z\r\n", "Subject: Hi\r\n", "line1\r\nline2"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered message missing %q:\n%s", want, out)
		}
	}
}
