package notify

import (
	"context"
	"strings"

	"github.com/rs/zerolog"
)

// ChannelTelegram is the Telegram channel's name and Recipient.Channels key
// (value: chat id).
const ChannelTelegram = "telegram"

// Telegram is a placeholder channel: it is enabled by TELEGRAM_BOT_TOKEN
// but does not deliver anything yet.
type Telegram struct {
	token  string
	logger zerolog.Logger
}

// NewTelegram returns nil when botToken is empty (channel disabled).
func NewTelegram(botToken string, logger zerolog.Logger) *Telegram {
	if strings.TrimSpace(botToken) == "" {
		return nil
	}
	return &Telegram{token: botToken, logger: logger.With().Str("component", "notify_telegram").Logger()}
}

// Enabled reports whether a bot token is configured.
func (t *Telegram) Enabled() bool { return t != nil && t.token != "" }

// Name implements Channel.
func (t *Telegram) Name() string { return ChannelTelegram }

// Send implements Channel. Not implemented yet: logs and succeeds.
func (t *Telegram) Send(_ context.Context, r Recipient, n Notice) error {
	t.logger.Debug().Str("user_id", r.UserID.String()).Str("kind", n.Kind).Msg("telegram channel not implemented")
	return nil
}
