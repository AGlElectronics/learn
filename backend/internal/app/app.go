// Package app wires configuration, the database pool, auth and HTTP into a
// runnable server. cmd/server/main.go just calls Run.
//
// This package does NOT run Goose migrations on start. Migrations are a
// one-shot controlled operation handled by cmd/migrate (Compose service /
// K8s Job), same as every other Con Services module.
package app

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/learn/backend/internal/audit"
	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/brand"
	"github.com/AGlElectronics/learn/backend/internal/config"
	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/internal/denlink"
	"github.com/AGlElectronics/learn/backend/internal/files"
	httpx "github.com/AGlElectronics/learn/backend/internal/http"
	"github.com/AGlElectronics/learn/backend/internal/keycloakadmin"
	"github.com/AGlElectronics/learn/backend/internal/learn"
	"github.com/AGlElectronics/learn/backend/internal/mail"
	"github.com/AGlElectronics/learn/backend/internal/notify"
	"github.com/AGlElectronics/learn/backend/internal/tlscert"
)

// Version is set via -ldflags at build time; left as "dev" for local runs.
var Version = "dev"

// Run boots the server and blocks until a shutdown signal is received or
// ListenAndServe fails.
func Run(ctx context.Context, logger zerolog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger.Info().
		Str("env", cfg.AppEnv).
		Str("addr", cfg.HTTPAddr).
		Str("auth_mode", string(cfg.AuthMode)).
		Str("version", Version).
		Msg("starting learn backend")

	// DB pool. Migrations are NOT applied here; cmd/migrate ran first.
	dbCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := db.Open(dbCtx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer pool.Close()
	logger.Info().Msg("database pool ready")

	// OIDC verifier. Only required when AUTH_MODE=oidc; dev mode skips.
	var oidcVerifier *auth.OIDCVerifier
	if cfg.AuthMode == config.AuthModeOIDC {
		oidcCtx, cancelOIDC := context.WithTimeout(ctx, 30*time.Second)
		oidcVerifier, err = auth.NewOIDCVerifier(oidcCtx, cfg)
		cancelOIDC()
		if err != nil {
			return fmt.Errorf("init oidc verifier: %w", err)
		}
		logger.Info().Str("issuer", cfg.OIDCIssuerURL).Msg("oidc verifier ready")
	}
	// Portal routes (/api/portal/todos) also accept OIDC_EXTRA_AUDIENCES.
	var portalVerifier *auth.PortalVerifier
	if cfg.AuthMode == config.AuthModeOIDC && len(cfg.OIDCExtraAudiences) > 0 {
		portalCtx, cancelPortal := context.WithTimeout(ctx, 30*time.Second)
		portalVerifier, err = auth.NewPortalVerifier(portalCtx, cfg)
		cancelPortal()
		if err != nil {
			return fmt.Errorf("init portal verifier: %w", err)
		}
		logger.Info().Strs("audiences", auth.PortalAudiences(cfg)).Msg("portal verifier ready")
	}

	// Access config catalog. A bogus ACCESS_CONFIG_PATH fails loudly; no
	// file at all falls back to the built-in role catalog (dev only).
	var access *auth.AccessConfig
	if cfg.AccessConfigPath != "" {
		access, err = auth.LoadAccessConfig(cfg.AccessConfigPath)
		if err != nil {
			return fmt.Errorf("load access config: %w", err)
		}
		logger.Info().
			Str("path", cfg.AccessConfigPath).
			Strs("roles", access.Roles()).
			Msg("access config loaded")
	} else {
		access = auth.DefaultAccessConfig()
		logger.Info().Msg("no access config file configured; using built-in role catalog")
	}

	brandProvider := brand.New(cfg)
	// Den link: pairing, the settings bundle, revocation. Open fails when
	// DEN_LINK_ENCRYPTION_KEY is wrong or the stored link can't be read.
	den, err := denlink.Open(pool, cfg.DenConfig(Version), logger)
	if err != nil {
		return fmt.Errorf("den link: %w", err)
	}
	if err := den.Restore(ctx); err != nil {
		logger.Warn().Err(err).Msg("den link: load failed")
	}
	go den.Run(ctx)

	// svc is the learn service with the group matching rule and Den's
	// module settings; workers share it.
	svc := learn.NewService(pool)
	svc.SetGroupMatch(cfg.GroupMatch)
	svc.SetModuleSettings(den.ModuleSettings)

	// Keycloak group sync: every 5 minutes, off when GROUP_DIRECTORY=off or
	// KEYCLOAK_* is unset.
	keycloak, err := keycloakadmin.NewFromConfig(cfg)
	if err != nil {
		return fmt.Errorf("init keycloak admin client: %w", err)
	}
	if groupSync := learn.NewGroupSync(svc, keycloak, learn.GroupSyncIssuer(cfg), logger); groupSync != nil {
		go groupSync.Run(ctx)
		logger.Info().Str("issuer", learn.GroupSyncIssuer(cfg)).Strs("linkable_groups", cfg.LinkableGroups).
			Msg("keycloak group directory and sync enabled")
	} else {
		logger.Info().Str("group_directory", string(cfg.GroupDirectory)).
			Msg("group directory off; group links use the groups people sign in with")
	}

	// Retention: anonymise inactive people, delete old audit entries.
	go svc.RunRetention(ctx, audit.New(pool, logger), logger)

	// Object storage for the media library: optional. Unset S3_ENDPOINT, or
	// storage unreachable at start, means uploads answer 503.
	var store learn.ObjectStore
	filesCtx, cancelFiles := context.WithTimeout(ctx, 15*time.Second)
	fileClient, err := files.New(filesCtx, cfg)
	cancelFiles()
	switch {
	case err != nil:
		logger.Error().Err(err).Msg("object storage unavailable; media uploads disabled")
	case fileClient != nil:
		store = fileClient
		logger.Info().Str("bucket", fileClient.Bucket()).Msg("object storage ready")
		go learn.NewMediaService(svc, store).RunSweeper(ctx, logger)
	default:
		logger.Info().Msg("S3_ENDPOINT not set; media uploads disabled")
	}

	// Notifications: email through Den's mail server (smtp.* module
	// settings) or SMTP_HOST, Telegram when TELEGRAM_BOT_TOKEN is set (and
	// the telegram_enabled setting is on). Email is decided per message, so
	// the channel is always there. The reminder worker runs at start and
	// hourly.
	mailer := mail.NewSwitch(mail.NewSMTP(mail.SMTPConfig{
		Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword,
		From: cfg.SMTPFrom, FromName: cfg.SMTPFromName,
	}), denSMTP(den))
	if source, warning := mailer.Source(); warning != "" {
		logger.Warn().Str("mail_source", source).Msg(warning)
	}
	notifier := notify.New(logger, notify.NewEmail(mailer), notify.NewTelegram(notify.TelegramConfig{
		BotToken: cfg.TelegramBotToken, APIURL: cfg.TelegramAPIURL, Enabled: svc.TelegramEnabled, Logger: logger,
	}))
	reminders := learn.NewReminders(svc, notifier, cfg.PublicBaseURL, httpx.OrgNameFunc(brandProvider, den), logger)
	go reminders.Run(ctx)
	logger.Info().Strs("channels", notifier.Channels()).Msg("reminders enabled")

	router := httpx.NewRouter(httpx.Deps{
		Config:         cfg,
		Pool:           pool,
		Logger:         logger,
		Brand:          brandProvider,
		Den:            den,
		OIDCVerifier:   oidcVerifier,
		PortalVerifier: portalVerifier,
		Access:         access,
		Keycloak:       keycloak,
		Files:          store,
		Notifier:       notifier,
		Service:        svc,
		Mail:           mailer,
	}, Version)

	server := &nethttp.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if _, err := tlscert.Configure(ctx, server, logger); err != nil {
		return fmt.Errorf("tls: %w", err)
	}

	return runServer(ctx, server, pool, cfg.ShutdownTimeout, logger)
}

// denSMTP reads the mail server from Den's module settings (smtp.*).
func denSMTP(den *denlink.Client) mail.DenSMTP {
	return func() (mail.SMTPConfig, bool, bool) {
		ms := den.ModuleSettings()
		if ms == nil {
			return mail.SMTPConfig{}, false, false
		}
		s, ok := ms.SMTP()
		if !ok {
			return mail.SMTPConfig{}, false, false
		}
		return mail.SMTPConfig{
			Host: s.Host, Port: s.Port, Username: s.Username,
			From: s.From, FromName: s.FromName, Security: s.Security,
		}, s.PasswordSet, true
	}
}

func runServer(ctx context.Context, server *nethttp.Server, pool *pgxpool.Pool, shutdownTimeout time.Duration, logger zerolog.Logger) error {
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info().Str("addr", server.Addr).Msg("http listening")
		if err := tlscert.ListenAndServe(server); err != nil && !errors.Is(err, nethttp.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-signalCtx.Done():
		logger.Info().Msg("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("http shutdown error")
	}
	pool.Close()
	logger.Info().Msg("shutdown complete")
	return nil
}
