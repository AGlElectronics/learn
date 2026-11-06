// Package config loads and validates the backend runtime configuration from
// environment variables (12-factor). Validation runs once at startup and
// fails loudly: a misconfigured deployment is better than a silently
// degraded one.
//
// Optional integrations (S3 object storage, SMTP, Keycloak Admin API,
// Telegram) are disabled when their primary setting is empty.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// AuthMode selects how incoming requests are authenticated.
//
//   - AuthModeDev reads an X-Dev-User header (identity) and an X-Dev-Groups
//     header (simulated IdP groups) and provisions a local user tied to the
//     "dev" issuer. Only safe for local scaffolding and CI.
//   - AuthModeOIDC verifies a Bearer JWT via the configured issuer.
type AuthMode string

const (
	AuthModeOIDC AuthMode = "oidc"
	AuthModeDev  AuthMode = "dev"
)

// Config is the validated process configuration.
type Config struct {
	// Core
	AppEnv     string
	AppBaseURL string
	HTTPAddr   string

	// Database
	DatabaseURL string

	// Auth
	AuthMode      AuthMode
	OIDCIssuerURL string
	OIDCClientID  string
	OIDCAudience  string
	// OIDCExtraAudiences are other clients' audiences (Den's SPA) accepted
	// only on the read-only portal routes (GET /api/portal/todos).
	OIDCExtraAudiences []string
	AccessConfigPath   string
	DevDefaultUser     string
	DevDefaultGroups   []string

	// Brand provider
	BrandLocalPath string // optional brand.json; Den's brand overlays it once paired
	// DenInternalURL replaces the host of pasted Den pairing URLs (Docker).
	DenInternalURL string

	// HTTP
	CORSAllowedOrigins []string
	ShutdownTimeout    time.Duration

	// PublicBaseURL is the browser-facing origin of the Learn SPA, used to
	// build links in notifications (e.g. https://learn.conservices.test).
	PublicBaseURL string

	// Object storage (S3-compatible; MinIO in dev). Empty S3Endpoint
	// disables file storage.
	S3Endpoint        string
	S3Bucket          string
	S3AccessKeyID     string
	S3SecretAccessKey string
	S3Region          string
	S3UsePathStyle    bool
	// S3PublicBaseURL rewrites presigned URLs to a browser-reachable host.
	S3PublicBaseURL string

	// SMTP for Learn's own notices. Empty SMTPHost disables email.
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	SMTPFromName string

	// Keycloak Admin API (read-only group/member lookups). Optional: all
	// four must be set to enable it.
	KeycloakBaseURL      string
	KeycloakRealm        string
	KeycloakClientID     string
	KeycloakClientSecret string

	// LearnServiceToken authenticates service-to-service calls (HR -> Learn).
	LearnServiceToken string

	// TelegramBotToken enables the (stub) Telegram notification channel.
	TelegramBotToken string
}

// S3Enabled reports whether object storage is configured.
func (c *Config) S3Enabled() bool { return strings.TrimSpace(c.S3Endpoint) != "" }

// KeycloakAdminEnabled reports whether the Keycloak Admin API client is configured.
func (c *Config) KeycloakAdminEnabled() bool {
	return c.KeycloakBaseURL != "" && c.KeycloakRealm != "" && c.KeycloakClientID != "" && c.KeycloakClientSecret != ""
}

// Load reads environment variables (plus an optional .env file) and returns
// a validated Config. Required fields that are missing return an aggregated
// error so operators see every problem at once instead of fixing one at a
// time.
func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		AppEnv: getEnv("APP_ENV", "development"),
		// Container-internal defaults: every module in the ecosystem binds
		// :8080 inside its container. Host-side port mapping (host 8120 ->
		// container 8080 for Learn) is handled by docker-compose / K8s.
		AppBaseURL: getEnv("APP_BASE_URL", "http://localhost:8080"),
		HTTPAddr:   getEnv("HTTP_ADDR", ":8080"),

		DatabaseURL: getEnv("DATABASE_URL", ""),

		AuthMode:           AuthMode(getEnv("AUTH_MODE", "dev")),
		OIDCIssuerURL:      getEnv("OIDC_ISSUER_URL", ""),
		OIDCClientID:       getEnv("OIDC_CLIENT_ID", ""),
		OIDCAudience:       getEnv("OIDC_AUDIENCE", ""),
		OIDCExtraAudiences: splitCSV(getEnv("OIDC_EXTRA_AUDIENCES", "")),
		AccessConfigPath:   getEnv("ACCESS_CONFIG_PATH", ""),
		DevDefaultUser:     getEnv("DEV_DEFAULT_USER", "dev-admin"),
		DevDefaultGroups:   splitCSV(getEnv("DEV_DEFAULT_GROUPS", "learn-admin")),

		BrandLocalPath: getEnv("BRAND_LOCAL_PATH", ""),
		DenInternalURL: getEnv("DEN_INTERNAL_URL", ""),

		CORSAllowedOrigins: splitCSV(getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:3005")),
		ShutdownTimeout:    getEnvDuration("SHUTDOWN_TIMEOUT", 15*time.Second),

		PublicBaseURL: strings.TrimRight(getEnv("PUBLIC_BASE_URL", ""), "/"),

		S3Endpoint:        getEnv("S3_ENDPOINT", ""),
		S3Bucket:          getEnv("S3_BUCKET", "learn-data-dev"),
		S3AccessKeyID:     getEnv("S3_ACCESS_KEY_ID", ""),
		S3SecretAccessKey: getEnv("S3_SECRET_ACCESS_KEY", ""),
		S3Region:          getEnv("S3_REGION", "us-east-1"),
		S3UsePathStyle:    getEnvBool("S3_USE_PATH_STYLE", true),
		S3PublicBaseURL:   getEnv("S3_PUBLIC_BASE_URL", ""),

		SMTPHost:     getEnv("SMTP_HOST", ""),
		SMTPPort:     getEnvInt("SMTP_PORT", 587),
		SMTPUsername: getEnv("SMTP_USERNAME", ""),
		SMTPPassword: getEnv("SMTP_PASSWORD", ""),
		SMTPFrom:     getEnv("SMTP_FROM", ""),
		SMTPFromName: getEnv("SMTP_FROM_NAME", ""),

		KeycloakBaseURL:      strings.TrimRight(getEnv("KEYCLOAK_BASE_URL", ""), "/"),
		KeycloakRealm:        getEnv("KEYCLOAK_REALM", ""),
		KeycloakClientID:     getEnv("KEYCLOAK_CLIENT_ID", ""),
		KeycloakClientSecret: getEnv("KEYCLOAK_CLIENT_SECRET", ""),

		LearnServiceToken: getEnv("LEARN_SERVICE_TOKEN", ""),
		TelegramBotToken:  getEnv("TELEGRAM_BOT_TOKEN", ""),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	var errs []string

	if c.DatabaseURL == "" {
		errs = append(errs, "DATABASE_URL is required")
	}

	switch c.AuthMode {
	case AuthModeOIDC:
		if c.OIDCIssuerURL == "" {
			errs = append(errs, "OIDC_ISSUER_URL is required when AUTH_MODE=oidc")
		}
		if c.OIDCClientID == "" {
			errs = append(errs, "OIDC_CLIENT_ID is required when AUTH_MODE=oidc")
		}
	case AuthModeDev:
		// dev mode: no extra required fields; dev users auto-provision.
	default:
		errs = append(errs, fmt.Sprintf("AUTH_MODE must be 'oidc' or 'dev', got %q", c.AuthMode))
	}

	if len(errs) > 0 {
		return errors.New("invalid config: " + strings.Join(errs, "; "))
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvInt(key string, fallback int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvBool(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return parsed
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
