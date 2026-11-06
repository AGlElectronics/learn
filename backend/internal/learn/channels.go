package learn

import (
	"context"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/notify"
)

// telegramChatIDRE: Telegram chat ids are integers (groups are negative).
var telegramChatIDRE = regexp.MustCompile(`^-?[0-9]{1,20}$`)

// UserChannels returns a person's addresses on non-email channels
// (channel -> address), as the reminder notifier reads them.
func (s *Service) UserChannels(ctx context.Context, userID uuid.UUID) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT channel, address FROM user_channels WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var ch, addr string
		if err := rows.Scan(&ch, &addr); err != nil {
			return nil, err
		}
		out[ch] = addr
	}
	return out, rows.Err()
}

// SetTelegramChatID stores (or with "" removes) the person's Telegram
// chat id.
func (s *Service) SetTelegramChatID(ctx context.Context, userID uuid.UUID, chatID string) error {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		_, err := s.pool.Exec(ctx, `DELETE FROM user_channels WHERE user_id = $1 AND channel = $2`, userID, notify.ChannelTelegram)
		return err
	}
	if !telegramChatIDRE.MatchString(chatID) {
		return httperror.Validation("telegram chat id must be a number (e.g. 123456789)", nil)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO user_channels (user_id, channel, address) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, channel) DO UPDATE SET address = EXCLUDED.address, updated_at = now()`,
		userID, notify.ChannelTelegram, chatID)
	return err
}
