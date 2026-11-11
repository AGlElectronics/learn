package mail

import (
	"context"
)

// DenMail returns the mail server Den's module settings name (smtp.*).
// ok is false when Den sets no host (the caller keeps SMTP_*). A non-nil
// error means Den did set a server and it cannot be used; do not fall
// back to SMTP_*.
type DenMail func(ctx context.Context) (cfg SMTPConfig, ok bool, err error)

// Switch sends through Den's mail server when Den sets smtp.host, else
// through the SMTP_* server, else not at all (ErrNotConfigured). It is
// decided on every message, so pairing with Den or changing Den's settings
// needs no restart.
type Switch struct {
	env *SMTP
	den DenMail
}

// NewSwitch returns a Switch over the SMTP_* server (nil: none) and Den's
// settings (nil: never Den).
func NewSwitch(env *SMTP, den DenMail) *Switch {
	return &Switch{env: env, den: den}
}

// current is the server for the next message and where it came from.
// source is den, env or none. When Den sets a server and the password
// cannot be fetched, err is set and env is not used.
func (s *Switch) current(ctx context.Context) (*SMTP, string, error) {
	if s == nil {
		return nil, "none", nil
	}
	if s.den != nil {
		cfg, ok, err := s.den(ctx)
		if err != nil {
			return nil, "den", err
		}
		if ok {
			return NewSMTP(cfg), "den", nil
		}
	}
	if s.env != nil {
		return s.env, "env", nil
	}
	return nil, "none", nil
}

// Source says which server the next message uses: den, env or none.
// warning is set when Den's server cannot be used (it is not replaced
// by SMTP_*).
func (s *Switch) Source(ctx context.Context) (source, warning string) {
	_, source, err := s.current(ctx)
	if err != nil {
		return source, err.Error()
	}
	return source, ""
}

// Send implements Sender.
func (s *Switch) Send(ctx context.Context, m Message) error {
	smtp, _, err := s.current(ctx)
	if err != nil {
		return err
	}
	if smtp == nil {
		return ErrNotConfigured
	}
	return smtp.Send(ctx, m)
}
