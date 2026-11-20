// Code generated from con-services shared/go/tgcatchall; DO NOT EDIT. Run scripts/sync-shared.

// Package tgcatchall is a Compose / local-dev sink that posts every outbound
// Telegram (and similar) notice to one chat or forum topic, with a prefix
// naming who or what the message was meant for.
//
// It only activates when APP_ENV=development and both TELEGRAM_BOT_TOKEN and
// TELEGRAM_DEV_CATCHALL_CHAT_ID are set. Production must never set the
// catch-all chat id; FromEnv refuses to enable outside development.
package tgcatchall

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Env keys (Compose / .env).
const (
	EnvAppEnv   = "APP_ENV"
	EnvToken    = "TELEGRAM_BOT_TOKEN"
	EnvChatID   = "TELEGRAM_DEV_CATCHALL_CHAT_ID"
	EnvThreadID = "TELEGRAM_DEV_CATCHALL_THREAD_ID"
	EnvAPIURL   = "TELEGRAM_API_URL"
)

// Development is the only APP_ENV that may enable the catch-all.
const Development = "development"

// DefaultAPIURL is the public Bot API origin.
const DefaultAPIURL = "https://api.telegram.org"

// Config is the catch-all destination. Zero value is disabled.
type Config struct {
	AppEnv   string
	Token    string
	ChatID   string
	ThreadID *int64
	APIURL   string
}

// FromEnv reads the catch-all settings. It never errors for empty optional
// values; a chat id set outside APP_ENV=development is ignored (disabled)
// so a mistaken prod env cannot fan everything into one chat.
func FromEnv(getenv func(string) string) Config {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	appEnv := strings.ToLower(strings.TrimSpace(getenv(EnvAppEnv)))
	if appEnv == "" {
		appEnv = "production"
	}
	api := strings.TrimRight(strings.TrimSpace(getenv(EnvAPIURL)), "/")
	if api == "" {
		api = DefaultAPIURL
	}
	cfg := Config{
		AppEnv: appEnv,
		Token:  strings.TrimSpace(getenv(EnvToken)),
		ChatID: strings.TrimSpace(getenv(EnvChatID)),
		APIURL: api,
	}
	if raw := strings.TrimSpace(getenv(EnvThreadID)); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			cfg.ThreadID = &n
		}
	}
	return cfg
}

// Enabled reports whether messages should be redirected to the catch-all.
func (c Config) Enabled() bool {
	return c.AppEnv == Development && c.Token != "" && c.ChatID != ""
}

// Client posts prefixed messages to the catch-all chat.
type Client struct {
	cfg  Config
	http *http.Client
}

// New returns a client, or nil when the catch-all is not enabled.
func New(cfg Config) *Client {
	if !cfg.Enabled() {
		return nil
	}
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: 15 * time.Second},
	}
}

// Enabled reports whether this client will send.
func (c *Client) Enabled() bool { return c != nil && c.cfg.Enabled() }

// Format builds the prefixed body: who/what it was for, then the real text.
func Format(intended, text string) string {
	intended = strings.TrimSpace(intended)
	text = strings.TrimSpace(text)
	if intended == "" {
		intended = "(unknown destination)"
	}
	if text == "" {
		text = "(empty message)"
	}
	return "[dev catch-all] intended for: " + intended + "\n\n" + text
}

// Send posts text to the catch-all, prefixed with intended (person, chat,
// department, …). Nil client or empty payload is a no-op.
func (c *Client) Send(ctx context.Context, intended, text string) error {
	if !c.Enabled() {
		return nil
	}
	body := map[string]any{
		"chat_id":                  c.cfg.ChatID,
		"text":                     Format(intended, text),
		"disable_web_page_preview": true,
	}
	if c.cfg.ThreadID != nil && *c.cfg.ThreadID > 0 {
		body["message_thread_id"] = *c.cfg.ThreadID
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.APIURL+"/bot"+c.cfg.Token+"/sendMessage", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram catch-all unreachable")
	}
	defer func() { _ = res.Body.Close() }()
	var out struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	if !out.OK {
		if out.Description == "" {
			out.Description = res.Status
		}
		return fmt.Errorf("telegram catch-all: %s", out.Description)
	}
	return nil
}
