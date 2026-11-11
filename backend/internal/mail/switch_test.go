package mail

import (
	"context"
	"errors"
	"testing"
)

func TestSwitchPrefersDenWhenHostIsSet(t *testing.T) {
	env := NewSMTP(SMTPConfig{Host: "mailpit", Port: 1025, From: "env@example.org", Password: "env-secret"})
	s := NewSwitch(env, func(context.Context) (SMTPConfig, bool, error) {
		return SMTPConfig{
			Host: "smtp.example.org", Port: 587, Username: "conservices", Password: "den-secret",
			From: "learn@example.org", Security: "starttls",
		}, true, nil
	})
	got, source, err := s.current(context.Background())
	if err != nil || source != "den" || got == nil || got.cfg.Host != "smtp.example.org" || got.cfg.Password != "den-secret" {
		t.Fatalf("server = %+v source %q err %v", got, source, err)
	}
	if src, warning := s.Source(context.Background()); src != "den" || warning != "" {
		t.Fatalf("source = %q %q", src, warning)
	}
}

func TestSwitchUsesEnvOnlyWhenDenSetsNoHost(t *testing.T) {
	env := NewSMTP(SMTPConfig{Host: "mailpit", Port: 1025, From: "env@example.org", Password: "env-secret"})
	s := NewSwitch(env, func(context.Context) (SMTPConfig, bool, error) {
		return SMTPConfig{}, false, nil
	})
	got, source, err := s.current(context.Background())
	if err != nil || source != "env" || got != env {
		t.Fatalf("server = %p source %q err %v, want the env server", got, source, err)
	}

	none := NewSwitch(nil, func(context.Context) (SMTPConfig, bool, error) {
		return SMTPConfig{}, false, nil
	})
	if got, source, err := none.current(context.Background()); err != nil || source != "none" || got != nil {
		t.Fatalf("no server: %+v %q %v", got, source, err)
	}
	if err := none.Send(context.Background(), Message{To: "a@b.c"}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("send with no server: %v", err)
	}
}

func TestSwitchDoesNotFallBackWhenDenPasswordFails(t *testing.T) {
	env := NewSMTP(SMTPConfig{Host: "mailpit", Port: 1025, From: "env@example.org", Password: "env-secret"})
	want := errors.New("could not reach Den")
	s := NewSwitch(env, func(context.Context) (SMTPConfig, bool, error) {
		return SMTPConfig{}, false, want
	})
	got, source, err := s.current(context.Background())
	if !errors.Is(err, want) || source != "den" || got != nil {
		t.Fatalf("server = %+v source %q err %v", got, source, err)
	}
	if err := s.Send(context.Background(), Message{To: "a@b.c"}); !errors.Is(err, want) {
		t.Fatalf("send fell through: %v", err)
	}
	if src, warning := s.Source(context.Background()); src != "den" || warning != want.Error() {
		t.Fatalf("source = %q %q", src, warning)
	}
}

func TestSwitchNil(t *testing.T) {
	var s *Switch
	if src, warning := s.Source(context.Background()); src != "none" || warning != "" {
		t.Fatalf("nil switch: %q %q", src, warning)
	}
}
