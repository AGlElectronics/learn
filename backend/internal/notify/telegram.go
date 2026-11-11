package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// ChannelTelegram is the Telegram channel's name and Recipient.Channels key
// (value: chat id).
const ChannelTelegram = "telegram"

// DefaultTelegramAPIURL is the Bot API origin.
const DefaultTelegramAPIURL = "https://api.telegram.org"

// ErrChannelOff means delivery on the channel is switched off right now
// (the module setting); the notice is not counted as delivered.
var ErrChannelOff = errors.New("channel is switched off")

// TelegramConfig configures the Telegram channel.
type TelegramConfig struct {
	// BotToken is TELEGRAM_BOT_TOKEN; empty disables the channel.
	BotToken string
	// APIURL is the Bot API origin (TELEGRAM_API_URL); empty = the public one.
	APIURL string
	// Enabled reports the module setting telegram_enabled; nil = always on.
	Enabled    func(ctx context.Context) bool
	HTTPClient *http.Client
	Logger     zerolog.Logger
}

// Telegram delivers notices with the Bot API's sendMessage to the chat id
// a person saved under "My notifications".
type Telegram struct {
	token   string
	apiURL  string
	enabled func(ctx context.Context) bool
	http    *http.Client
	logger  zerolog.Logger
}

// NewTelegram returns nil when the bot token is empty (channel disabled).
func NewTelegram(cfg TelegramConfig) *Telegram {
	if strings.TrimSpace(cfg.BotToken) == "" {
		return nil
	}
	api := strings.TrimRight(strings.TrimSpace(cfg.APIURL), "/")
	if api == "" {
		api = DefaultTelegramAPIURL
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Telegram{
		token: strings.TrimSpace(cfg.BotToken), apiURL: api, enabled: cfg.Enabled, http: client,
		logger: cfg.Logger.With().Str("component", "notify_telegram").Logger(),
	}
}

// Enabled reports whether a bot token is configured.
func (t *Telegram) Enabled() bool { return t != nil && t.token != "" }

// Name implements Channel.
func (t *Telegram) Name() string { return ChannelTelegram }

// telegramMaxText is the Bot API's message length limit.
const telegramMaxText = 4096

// Send implements Channel. A recipient without a chat id gets
// ErrNoAddress; with the module setting off it returns ErrChannelOff.
func (t *Telegram) Send(ctx context.Context, r Recipient, n Notice) error {
	chat := strings.TrimSpace(r.Channels[ChannelTelegram])
	if chat == "" {
		return ErrNoAddress
	}
	if t.enabled != nil && !t.enabled(ctx) {
		return ErrChannelOff
	}
	text := strings.TrimSpace(n.Subject)
	if body := strings.TrimSpace(n.Body); body != "" {
		text += "\n\n" + body
	}
	if n.URL != "" {
		text += "\n\n" + n.URL
	}
	if len([]rune(text)) > telegramMaxText {
		text = string([]rune(text)[:telegramMaxText-1]) + "…"
	}
	payload, err := json.Marshal(map[string]any{"chat_id": chat, "text": text, "disable_web_page_preview": true})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.apiURL+"/bot"+t.token+"/sendMessage", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := t.http.Do(req)
	if err != nil {
		// The URL holds the bot token; never put it in the error.
		return errors.New("telegram: could not reach the Bot API")
	}
	defer func() { _ = res.Body.Close() }()
	var out struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	_ = json.Unmarshal(raw, &out)
	if res.StatusCode != http.StatusOK || !out.OK {
		return fmt.Errorf("telegram: sendMessage failed: status %d: %s", res.StatusCode, out.Description)
	}
	return nil
}
