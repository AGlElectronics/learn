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

	"github.com/AGlElectronics/learn/backend/internal/authbase"
	"github.com/AGlElectronics/learn/backend/internal/denlink"
	"github.com/AGlElectronics/learn/backend/internal/groupmatch"
)

// AuthMode selects how incoming requests are authenticated (authbase).
//
//   - AuthModeDev reads an X-Dev-User header (identity) and an X-Dev-Groups
//     header (simulated IdP groups) and provisions a local user tied to the
//     "dev" issuer. Only allowed with APP_ENV=development.
//   - AuthModeOIDC verifies a Bearer access token via the configured issuer.
type AuthMode = authbase.AuthMode

const (
	AuthModeOIDC = authbase.AuthOIDC
	AuthModeDev  = authbase.AuthDev
)

// DefaultAudience is OIDC_AUDIENCE when unset: the access token audience
// Keycloak adds for Learn's API (token contract).
const DefaultAudience = "learn-api"

// Config is the validated process configuration.
type Config struct {
	// Core. AppEnv defaults to production; only "development" relaxes rules.
	AppEnv     string
	AppBaseURL string
	HTTPAddr   string

	// Database
	DatabaseURL string

	// Auth
	AuthMode         AuthMode
	OIDCIssuerURL    string
	OIDCClientID     string
	OIDCAudience     string
	AccessConfigPath string
	DevDefaultUser   string
	DevDefaultGroups []string
	// GroupMatch is OIDC_GROUPS_CLAIM, OIDC_GROUP_MATCH, OIDC_GROUP_PREFIX.
	GroupMatch groupmatch.Config
	// HRServiceToken lets HR read GET /api/role-catalog without a sign-in
	// (HR_SERVICE_TOKEN). Empty: only signed-in staff may read it.
	HRServiceToken string

	// Brand provider
	BrandLocalPath string // optional brand.json; Den's brand overlays it once paired
	// Den is the pairing configuration (DEN_MODE, DEN_INTERNAL_URL,
	// DEN_ALLOWED_HOSTS, DEN_LINK_ENCRYPTION_KEY[_PREVIOUS], APP_ENV).
	Den denlink.Config

	// HTTP
	CORSAllowedOrigins []string
	// TrustedProxies are the proxies whose X-Forwarded-For entries are
	// believed when working out the client IP (TRUSTED_PROXY_CIDRS).
	TrustedProxies  authbase.TrustedProxies
	ShutdownTimeout time.Duration

	// PublicBaseURL is the browser-facing origin of the Learn SPA, used to
	// build links in notifications (e.g. https://learn.conservices.test).
	PublicBaseURL string

	// Object storage (S3-compatible; SeaweedFS in dev). Empty S3Endpoint
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

	// GroupDirectory is GROUP_DIRECTORY: keycloak reads groups and people
	// from the Keycloak Admin API (when KEYCLOAK_* is set); off never does,
	// whatever KEYCLOAK_* says (an identity provider without that API).
	GroupDirectory GroupDirectoryMode
	// LinkableGroups is LEARN_LINKABLE_GROUPS: the groups trainers may see
	// and link to courses and paths. Entries are full paths; "/a/*" is
	// every group below /a, "*" is every group. Holders of course.manage
	// may link any group.
	LinkableGroups []string

	// LearnServiceToken authenticates service-to-service calls (HR -> Learn).
	LearnServiceToken string

	// FeedbackPepper is mixed into the hash that stops a person giving
	// anonymous feedback twice (LEARN_FEEDBACK_PEPPER, a secret).
	FeedbackPepper string

	// TelegramBotToken enables the Telegram notification channel; the
	// module setting telegram_enabled switches delivery on and off.
	TelegramBotToken string
	// TelegramAPIURL is the Bot API origin (TELEGRAM_API_URL).
	TelegramAPIURL string
}

// GroupDirectoryMode is GROUP_DIRECTORY.
type GroupDirectoryMode string

const (
	GroupDirectoryKeycloak GroupDirectoryMode = "keycloak"
	GroupDirectoryOff      GroupDirectoryMode = "off"
)

// DevFeedbackPepper is used in development when LEARN_FEEDBACK_PEPPER is
// unset. Never valid outside development.
const DevFeedbackPepper = "learn-dev-only-feedback-pepper"

// Development reports whether APP_ENV is development.
func (c *Config) Development() bool { return c.AppEnv == authbase.Development }

// Auth is the authbase view of AUTH_MODE, APP_ENV and TRUSTED_PROXY_CIDRS.
func (c *Config) Auth() authbase.Config {
	return authbase.Config{AppEnv: c.AppEnv, AuthMode: c.AuthMode, TrustedProxies: c.TrustedProxies}
}

// Audience is the audience access tokens must carry.
func (c *Config) Audience() string {
	if c.OIDCAudience != "" {
		return c.OIDCAudience
	}
	return DefaultAudience
}

// DenConfig is Den with the slug and version filled in, and APP_ENV taken
// from the process when Den does not set its own.
func (c *Config) DenConfig(version string) denlink.Config {
	d := c.Den
	d.Slug, d.Version = "learn", version
	if d.AppEnv == "" {
		d.AppEnv = c.AppEnv
	}
	return d
}

// S3Enabled reports whether object storage is configured.
func (c *Config) S3Enabled() bool { return strings.TrimSpace(c.S3Endpoint) != "" }

// KeycloakAdminEnabled reports whether the Keycloak Admin API client is
// configured and the group directory is not switched off.
func (c *Config) KeycloakAdminEnabled() bool {
	return c.GroupDirectory != GroupDirectoryOff &&
		c.KeycloakBaseURL != "" && c.KeycloakRealm != "" && c.KeycloakClientID != "" && c.KeycloakClientSecret != ""
}

// Load reads environment variables (plus an optional .env file) and returns
// a validated Config. Required fields that are missing return an aggregated
// error so operators see every problem at once instead of fixing one at a
// time.
func Load() (*Config, error) {
	_ = godotenv.Load()

	var errs []string
	auth, err := authbase.FromEnv(os.Getenv)
	if err != nil {
		errs = append(errs, err.Error())
	}
	groups, err := groupmatch.FromEnv(os.Getenv)
	if err != nil {
		errs = append(errs, err.Error())
	}
	den, err := denlink.FromEnv("learn", "", os.Getenv)
	if err != nil {
		errs = append(errs, err.Error())
	}

	cfg := &Config{
		AppEnv: auth.AppEnv,
		// Container-internal defaults: every module in the ecosystem binds
		// :8080 inside its container. Host-side port mapping (host 8120 ->
		// container 8080 for Learn) is handled by docker-compose / K8s.
		AppBaseURL: getEnv("APP_BASE_URL", "http://localhost:8080"),
		HTTPAddr:   getEnv("HTTP_ADDR", ":8080"),

		DatabaseURL: getEnv("DATABASE_URL", ""),

		AuthMode:         auth.AuthMode,
		OIDCIssuerURL:    getEnv("OIDC_ISSUER_URL", ""),
		OIDCClientID:     getEnv("OIDC_CLIENT_ID", ""),
		OIDCAudience:     getEnv("OIDC_AUDIENCE", DefaultAudience),
		AccessConfigPath: getEnv("ACCESS_CONFIG_PATH", ""),
		DevDefaultUser:   getEnv("DEV_DEFAULT_USER", "dev-admin"),
		DevDefaultGroups: splitCSV(getEnv("DEV_DEFAULT_GROUPS", "learn-admin")),
		GroupMatch:       groups,
		HRServiceToken:   getEnv("HR_SERVICE_TOKEN", ""),

		BrandLocalPath: getEnv("BRAND_LOCAL_PATH", ""),
		Den:            den,

		CORSAllowedOrigins: splitCSV(getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:3005")),
		TrustedProxies:     auth.TrustedProxies,
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

		GroupDirectory: GroupDirectoryMode(strings.ToLower(getEnv("GROUP_DIRECTORY", string(GroupDirectoryKeycloak)))),
		LinkableGroups: splitCSV(getEnv("LEARN_LINKABLE_GROUPS", "/learn/*")),

		LearnServiceToken: getEnv("LEARN_SERVICE_TOKEN", ""),
		FeedbackPepper:    getEnv("LEARN_FEEDBACK_PEPPER", ""),
		TelegramBotToken:  getEnv("TELEGRAM_BOT_TOKEN", ""),
		TelegramAPIURL:    strings.TrimRight(getEnv("TELEGRAM_API_URL", "https://api.telegram.org"), "/"),
	}
	if cfg.FeedbackPepper == "" && cfg.Development() {
		cfg.FeedbackPepper = DevFeedbackPepper
	}

	if err := cfg.validate(errs); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate(errs []string) error {
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
		// authbase.FromEnv already refused it outside development.
	}

	switch c.GroupDirectory {
	case GroupDirectoryKeycloak, GroupDirectoryOff:
	default:
		errs = append(errs, fmt.Sprintf("GROUP_DIRECTORY must be 'keycloak' or 'off', got %q", c.GroupDirectory))
	}

	if !c.Development() {
		if c.LearnServiceToken != "" {
			if err := CheckSecret(c.LearnServiceToken); err != nil {
				errs = append(errs, "LEARN_SERVICE_TOKEN "+err.Error())
			}
		}
		if c.HRServiceToken != "" {
			if err := CheckSecret(c.HRServiceToken); err != nil {
				errs = append(errs, "HR_SERVICE_TOKEN "+err.Error())
			}
		}
		switch c.FeedbackPepper {
		case "":
			errs = append(errs, "LEARN_FEEDBACK_PEPPER is required outside development (APP_ENV="+c.AppEnv+")")
		case DevFeedbackPepper:
			errs = append(errs, "LEARN_FEEDBACK_PEPPER is the development value; generate a real one")
		default:
			if err := CheckSecret(c.FeedbackPepper); err != nil {
				errs = append(errs, "LEARN_FEEDBACK_PEPPER "+err.Error())
			}
		}
	}

	if len(errs) > 0 {
		return errors.New("invalid config: " + strings.Join(errs, "; "))
	}
	return nil
}

// MinSecretLength is the shortest accepted shared secret outside
// development (32 characters: `openssl rand -hex 32` gives 64).
const MinSecretLength = 32

// CheckSecret rejects a shared secret that is short, uses few distinct
// characters, or is an obvious placeholder. It is a guard against
// copy-pasted example values, not a full entropy estimate.
func CheckSecret(s string) error {
	if len(s) < MinSecretLength {
		return fmt.Errorf("must be at least %d characters (generate one with: openssl rand -hex 32)", MinSecretLength)
	}
	distinct := map[rune]struct{}{}
	for _, r := range s {
		distinct[r] = struct{}{}
	}
	if len(distinct) < 10 {
		return errors.New("has too few different characters to be random (generate one with: openssl rand -hex 32)")
	}
	lower := strings.ToLower(s)
	for _, p := range []string{"change-me", "changeme", "dev-token", "dev-only", "example", "password", "secret"} {
		if strings.Contains(lower, p) {
			return fmt.Errorf("looks like a placeholder (contains %q); generate a real one", p)
		}
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
