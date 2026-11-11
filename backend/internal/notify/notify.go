// Package notify fans Learn notices (assignments, results, reminders) out to
// the delivery channels a deployment has enabled: email and Telegram. A
// failing channel is logged and skipped so one broken integration never
// blocks the others; only real deliveries are counted.
package notify

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Notice is a single message to deliver.
type Notice struct {
	Subject string
	Body    string
	// URL is an optional deep link into the Learn SPA.
	URL string
	// Kind classifies the notice (e.g. "quiz_assigned", "quiz_graded").
	Kind string
}

// Recipient is who receives a notice. Channels maps a channel name to the
// recipient's address on that channel (e.g. "telegram" -> chat id); the
// email channel falls back to Email.
type Recipient struct {
	UserID      uuid.UUID
	Email       string
	DisplayName string
	Channels    map[string]string
}

// Channel delivers notices over one medium.
type Channel interface {
	Name() string
	Send(ctx context.Context, r Recipient, n Notice) error
}

// Notifier fans a notice out to every configured channel.
type Notifier struct {
	channels []Channel
	logger   zerolog.Logger
}

// New returns a Notifier over the given channels; nil channels are dropped
// so callers can pass optional ones (e.g. NewEmail(nil, ...)) unconditionally.
func New(logger zerolog.Logger, channels ...Channel) *Notifier {
	n := &Notifier{logger: logger.With().Str("component", "notify").Logger()}
	for _, c := range channels {
		if c == nil || isNilChannel(c) {
			continue
		}
		n.channels = append(n.channels, c)
	}
	return n
}

// Channels returns the names of the enabled channels.
func (n *Notifier) Channels() []string {
	if n == nil {
		return nil
	}
	out := make([]string, 0, len(n.channels))
	for _, c := range n.channels {
		out = append(out, c.Name())
	}
	return out
}

// Notify sends the notice to every recipient on every channel. Errors are
// logged and do not stop delivery; it returns the number of successful
// channel deliveries.
func (n *Notifier) Notify(ctx context.Context, notice Notice, recipients ...Recipient) int {
	if n == nil {
		return 0
	}
	sent := 0
	for _, r := range recipients {
		for _, c := range n.channels {
			if ctx.Err() != nil {
				return sent
			}
			if err := c.Send(ctx, r, notice); err != nil {
				if errors.Is(err, ErrNoAddress) || errors.Is(err, ErrChannelOff) {
					continue // nothing to deliver on this channel; not a failure, not delivered
				}
				n.logger.Warn().Err(err).
					Str("channel", c.Name()).
					Str("user_id", r.UserID.String()).
					Str("kind", notice.Kind).
					Msg("notification delivery failed")
				continue
			}
			sent++
		}
	}
	return sent
}

func isNilChannel(c Channel) bool {
	switch v := c.(type) {
	case *Email:
		return v == nil
	case *Telegram:
		return v == nil
	}
	return false
}
