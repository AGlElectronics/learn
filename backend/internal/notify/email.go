package notify

import (
	"context"
	"errors"
	"strings"

	"github.com/AGlElectronics/learn/backend/internal/mail"
)

// ChannelEmail is the Email channel's name and Recipient.Channels key.
const ChannelEmail = "email"

// ErrNoAddress means the recipient has no address on the channel.
var ErrNoAddress = errors.New("recipient has no address for channel")

// Email delivers notices through a mail.Sender.
type Email struct {
	sender mail.Sender
}

// NewEmail returns nil when sender is nil (email disabled), so the result
// can be passed straight to New.
func NewEmail(sender mail.Sender) *Email {
	if sender == nil || isNilSender(sender) {
		return nil
	}
	return &Email{sender: sender}
}

// Name implements Channel.
func (e *Email) Name() string { return ChannelEmail }

// Send implements Channel.
func (e *Email) Send(ctx context.Context, r Recipient, n Notice) error {
	to := strings.TrimSpace(r.Channels[ChannelEmail])
	if to == "" {
		to = strings.TrimSpace(r.Email)
	}
	if to == "" {
		return ErrNoAddress
	}
	err := e.sender.Send(ctx, mail.Message{To: to, Subject: n.Subject, Body: emailBody(r, n)})
	if errors.Is(err, mail.ErrNotConfigured) {
		return ErrChannelOff
	}
	return err
}

func emailBody(r Recipient, n Notice) string {
	var b strings.Builder
	if name := strings.TrimSpace(r.DisplayName); name != "" {
		b.WriteString("Hi " + name + ",\n\n")
	}
	b.WriteString(strings.TrimRight(n.Body, "\n"))
	if n.URL != "" {
		b.WriteString("\n\n" + n.URL)
	}
	b.WriteString("\n")
	return b.String()
}

func isNilSender(s mail.Sender) bool {
	switch v := s.(type) {
	case *mail.SMTP:
		return v == nil
	case *mail.Switch:
		return v == nil
	}
	return false
}
