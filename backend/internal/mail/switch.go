package mail

import (
	"context"
	"errors"
)

// DenSMTP returns the mail server Den's module settings name (smtp.*), and
// whether Den needs a password for it. ok is false when Den sets none.
type DenSMTP func() (cfg SMTPConfig, passwordSet, ok bool)

// Switch sends through Den's mail server when Den's module settings set
// one Learn can use, else through the SMTP_* server, else not at all
// (ErrNotConfigured). It is decided on every message, so pairing with Den
// or changing Den's settings needs no restart.
type Switch struct {
	env *SMTP
	den DenSMTP
}

// NewSwitch returns a Switch over the SMTP_* server (nil: none) and Den's
// settings (nil: never Den).
func NewSwitch(env *SMTP, den DenSMTP) *Switch {
	return &Switch{env: env, den: den}
}

// ErrDenPassword explains why Den's mail server is not used.
var ErrDenPassword = errors.New("Den's mail server needs a password (smtp.password_set), which this Learn version cannot fetch from Den yet; Learn sends with SMTP_* instead")

// current is the server for the next message and where it came from.
func (s *Switch) current() (*SMTP, string, error) {
	if s == nil {
		return nil, "none", nil
	}
	var warn error
	if s.den != nil {
		if cfg, passwordSet, ok := s.den(); ok {
			if !passwordSet {
				return NewSMTP(cfg), "den", nil
			}
			warn = ErrDenPassword
		}
	}
	if s.env != nil {
		return s.env, "env", warn
	}
	return nil, "none", warn
}

// Source says which server the next message uses: den, env or none, and
// why Den's is not used when Den sets one.
func (s *Switch) Source() (source, warning string) {
	_, source, warn := s.current()
	if warn != nil {
		warning = warn.Error()
	}
	return source, warning
}

// Send implements Sender.
func (s *Switch) Send(ctx context.Context, m Message) error {
	smtp, _, _ := s.current()
	if smtp == nil {
		return ErrNotConfigured
	}
	return smtp.Send(ctx, m)
}
