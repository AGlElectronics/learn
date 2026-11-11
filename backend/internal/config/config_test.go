package config

import (
	"strings"
	"testing"
)

func TestCheckSecret(t *testing.T) {
	for s, ok := range map[string]bool{
		"":                                   false,
		"short":                              false,
		"learn-service-dev-token":            false,
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": false, // long, one character
		"change-me-change-me-change-me-1234": false,
		"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08": true,
		"Q3xv8Jw2LpR7nT4cYhE9sKdF6mBzA1gU":                                 true,
	} {
		if err := CheckSecret(s); (err == nil) != ok {
			t.Errorf("CheckSecret(%q) = %v, want ok=%v", s, err, ok)
		}
	}
}

func TestValidate(t *testing.T) {
	strong := "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	base := func() *Config {
		return &Config{AppEnv: "production", DatabaseURL: "postgres://x", AuthMode: AuthModeDev,
			GroupDirectory: GroupDirectoryKeycloak, FeedbackPepper: strong}
	}
	if err := base().validate(nil); err != nil {
		t.Fatalf("valid config refused: %v", err)
	}
	for name, tc := range map[string]struct {
		edit func(*Config)
		want string
	}{
		"weak service token":  {func(c *Config) { c.LearnServiceToken = "learn-service-dev-token" }, "LEARN_SERVICE_TOKEN"},
		"short HR token":      {func(c *Config) { c.HRServiceToken = "hr-token" }, "HR_SERVICE_TOKEN"},
		"no pepper":           {func(c *Config) { c.FeedbackPepper = "" }, "LEARN_FEEDBACK_PEPPER is required"},
		"dev pepper":          {func(c *Config) { c.FeedbackPepper = DevFeedbackPepper }, "development value"},
		"bad directory":       {func(c *Config) { c.GroupDirectory = "ldap" }, "GROUP_DIRECTORY"},
		"oidc without issuer": {func(c *Config) { c.AuthMode = AuthModeOIDC }, "OIDC_ISSUER_URL"},
		"no database":         {func(c *Config) { c.DatabaseURL = "" }, "DATABASE_URL"},
	} {
		t.Run(name, func(t *testing.T) {
			c := base()
			tc.edit(c)
			err := c.validate(nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}

	dev := base()
	dev.AppEnv = "development"
	dev.LearnServiceToken = "learn-service-dev-token"
	dev.FeedbackPepper = DevFeedbackPepper
	if err := dev.validate(nil); err != nil {
		t.Fatalf("development accepts dev secrets: %v", err)
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("APP_ENV", "development")
	// AUTH_MODE defaults to oidc, which needs an issuer. These defaults are
	// the development ones (pepper, directory, linkable groups).
	t.Setenv("AUTH_MODE", "dev")
	t.Setenv("LEARN_FEEDBACK_PEPPER", "")
	t.Setenv("LEARN_LINKABLE_GROUPS", "")
	t.Setenv("GROUP_DIRECTORY", "")
	t.Setenv("TELEGRAM_API_URL", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FeedbackPepper != DevFeedbackPepper || cfg.GroupDirectory != GroupDirectoryKeycloak ||
		len(cfg.LinkableGroups) != 1 || cfg.LinkableGroups[0] != "/learn/*" || cfg.TelegramAPIURL != "https://api.telegram.org" {
		t.Fatalf("defaults = pepper %q, directory %q, linkable %v, telegram %q",
			cfg.FeedbackPepper, cfg.GroupDirectory, cfg.LinkableGroups, cfg.TelegramAPIURL)
	}

	t.Setenv("GROUP_DIRECTORY", "OFF")
	t.Setenv("KEYCLOAK_BASE_URL", "http://kc")
	t.Setenv("KEYCLOAK_REALM", "r")
	t.Setenv("KEYCLOAK_CLIENT_ID", "c")
	t.Setenv("KEYCLOAK_CLIENT_SECRET", "s")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KeycloakAdminEnabled() {
		t.Fatal("GROUP_DIRECTORY=off must switch the Keycloak directory off")
	}
}

// Safe defaults (authbase, denlink): unset APP_ENV is production, unset
// AUTH_MODE is oidc, dev auth and a missing Den link key stop the start-up.
func TestLoadSafeDefaults(t *testing.T) {
	strong := "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("APP_ENV", "")
	t.Setenv("AUTH_MODE", "")
	t.Setenv("OIDC_ISSUER_URL", "https://auth.example.org/realms/conservices")
	t.Setenv("OIDC_CLIENT_ID", "learn-spa")
	t.Setenv("OIDC_AUDIENCE", "")
	t.Setenv("LEARN_FEEDBACK_PEPPER", strong)
	t.Setenv("DEN_LINK_ENCRYPTION_KEY", "")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "DEN_LINK_ENCRYPTION_KEY") {
		t.Fatalf("production without DEN_LINK_ENCRYPTION_KEY: %v", err)
	}
	t.Setenv("DEN_LINK_ENCRYPTION_KEY", "q3V0w6m3cXn8m1x0ZrQm9b7c2H4v5o6p7q8r9s0t1u4=")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppEnv != "production" || cfg.AuthMode != AuthModeOIDC || cfg.Audience() != "learn-api" ||
		cfg.GroupMatch.ClaimName() != "groups" || len(cfg.TrustedProxies) == 0 {
		t.Fatalf("defaults: env %q mode %q audience %q claim %q proxies %v",
			cfg.AppEnv, cfg.AuthMode, cfg.Audience(), cfg.GroupMatch.ClaimName(), cfg.TrustedProxies)
	}
	if d := cfg.DenConfig("1.0.0"); d.Slug != "learn" || d.Version != "1.0.0" || d.AppEnv != "production" {
		t.Fatalf("den config = %+v", d)
	}

	t.Setenv("AUTH_MODE", "dev")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "AUTH_MODE=dev") {
		t.Fatalf("AUTH_MODE=dev in production: %v", err)
	}
	t.Setenv("AUTH_MODE", "")
	t.Setenv("OIDC_GROUP_MATCH", "prefix")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "OIDC_GROUP_MATCH") {
		t.Fatalf("bad OIDC_GROUP_MATCH: %v", err)
	}
	t.Setenv("OIDC_GROUP_MATCH", "")
	t.Setenv("TRUSTED_PROXY_CIDRS", "10.0.0.0/33")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXY_CIDRS") {
		t.Fatalf("bad TRUSTED_PROXY_CIDRS: %v", err)
	}
}
