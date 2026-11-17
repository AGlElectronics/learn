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

	"github.com/AGlElectronics/learn/backend/internal/tgcatchall"
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
	// Catchall, when enabled (Compose / APP_ENV=development), sends every
	// notice to one test chat with a prefix naming the intended person.
	Catchall *tgcatchall.Client
}

// Telegram delivers notices with the Bot API's sendMessage to the chat id
// a person saved under "My notifications".
type Telegram struct {
	token    string
	apiURL   string
	enabled  func(ctx context.Context) bool
	http     *http.Client
	logger   zerolog.Logger
	catchall *tgcatchall.Client
}

// NewTelegram returns nil when the bot token is empty and no catch-all is on.
func NewTelegram(cfg TelegramConfig) *Telegram {
	if strings.TrimSpace(cfg.BotToken) == "" && !cfg.Catchall.Enabled() {
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
		catchall: cfg.Catchall,
	}
}

// Enabled reports whether a bot token or a dev catch-all is configured.
func (t *Telegram) Enabled() bool { return t != nil && (t.token != "" || t.catchall.Enabled()) }

// Name implements Channel.
func (t *Telegram) Name() string { return ChannelTelegram }

// telegramMaxText is the Bot API's message length limit.
const telegramMaxText = 4096

// Send implements Channel. A recipient without a chat id gets
// ErrNoAddress; with the module setting off it returns ErrChannelOff.
// With a Compose catch-all, every notice goes there (no personal chat id
// required), prefixed with who it was for.
func (t *Telegram) Send(ctx context.Context, r Recipient, n Notice) error {
	chat := strings.TrimSpace(r.Channels[ChannelTelegram])
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
	if t.catchall.Enabled() {
		who := strings.TrimSpace(r.DisplayName)
		if em := strings.TrimSpace(r.Email); em != "" {
			if who != "" {
				who += " <" + em + ">"
			} else {
				who = em
			}
		}
		if who == "" {
			if chat != "" {
				who = "chat " + chat
			} else {
				who = r.UserID.String()
			}
		}
		kind := strings.TrimSpace(n.Kind)
		if kind == "" {
			kind = "notice"
		}
		return t.catchall.Send(ctx, "Learn · "+who+" · "+kind, text)
	}
	if chat == "" {
		return ErrNoAddress
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
