// Package http wires the chi router and ties handlers to their
// dependencies. The structure deliberately mirrors the other Con Services
// modules: one Deps struct consumed by one NewRouter, with per-resource
// handler types owning their own routing closure.
//
// API shape:
//
//	GET /livez             — process liveness (unauthenticated)
//	GET /readyz            — service readiness (unauthenticated)
//	GET /api/health        — richer status for humans
//	GET /api/den/state     — Den mode and pairing (unauthenticated; also /api/v1/den/state)
//	GET /api/role-catalog  — role catalog for HR's access picker (HR_SERVICE_TOKEN or staff sign-in; ETag)
//	GET /api/v1/brand      — active brand package (unauthenticated)
//	GET /api/v1/me         — authenticated principal + permissions
//	GET|PUT /api/v1/me/preferences — theme (via Den) and language (Learn)
//	GET /api/v1/courses/{mine|catalog|teach} — course lists
//	GET|PUT /api/v1/{courses|qualifications|paths}/{id}/translations?lang= — see mountTranslationRoutes
//
// Learner-facing payloads are served in the caller's language: ?lang=,
// else the saved preference, else Accept-Language, matched against the
// course's languages, falling back to its default language.
//
//	POST /api/v1/courses   — create a course (trainers and admins)
//	GET|PATCH /api/v1/courses/{course}, POST …/{publish|unpublish|enrol}
//	GET|POST /api/v1/courses/{course}/enrolments, DELETE /api/v1/enrolments/{enrolment}
//	POST /api/v1/courses/{course}/sections, PATCH|DELETE /api/v1/sections/{section}
//	POST /api/v1/sections/{section}/activities, PATCH|DELETE /api/v1/activities/{activity}
//	PUT /api/v1/activities/{activity}/completion — mark a page (un)done
//	question bank, quiz slots, attempts, gradebook — see mountQuizRoutes
//	media library, view links, page attachments — see mountMediaRoutes
//	prerequisites, enrolment due dates — see mountRuleRoutes
//	session slots, booking, QR check-in, roster — see mountSessionRoutes
//	GET /api/v1/groups, /courses/{course}/group-links… — see mountGroupRoutes
//	GET /api/v1/courses/{course}/question-stats — see mountStatsRoutes
//	course and session feedback — see mountFeedbackRoutes
//	question import (preview/commit), CSV template — see mountImportRoutes
//	qualifications, certificates, renewal — see mountQualificationRoutes
//	GET /api/v1/reports/{scope|completion|qualifications} — see mountReportRoutes
//	POST /api/v1/courses/{course}/copy — copy for a new edition
//	learning paths, GET /api/v1/me/paths — see mountPathRoutes
//	POST /api/v1/reminders/run — send due reminders now (settings.manage)
//	team progress boards, GET /api/v1/me/teams — see mountTeamBoardRoutes
//	GET|PUT /api/v1/me/channels — notification channels (Telegram chat id)
//	GET /api/portal/todos  — suite to-do contract; also accepts OIDC_EXTRA_AUDIENCES tokens
//	GET /api/qualification-catalog — public qualification catalog for HR
//	GET /api/internal/holdings?subjects= — valid qualifications (LEARN_SERVICE_TOKEN)
//	GET /api/public/verify/{code} — public certificate check (no auth, rate-limited)
//	GET /api/v1/users?q=   — people picker for enrolment (staff only)
//	/api/v1/den…           — Den pairing admin (settings.manage)
//
// Everything under /api/v1 (except /brand) requires an access token for
// the learn-api audience (OIDC_AUDIENCE); ID tokens are refused. The auth
// middleware is applied inside a chi.Group so the shape is explicit. JSON
// bodies are capped at 1 MiB (413 request_too_large).
// With DEN_MODE=required, the /api routes other than health, role catalog,
// brand, me and den answer 503 den_pairing_required until Den pairs.
package http

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"

	"github.com/AGlElectronics/learn/backend/internal/audit"
	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/authbase"
	"github.com/AGlElectronics/learn/backend/internal/brand"
	"github.com/AGlElectronics/learn/backend/internal/config"
	"github.com/AGlElectronics/learn/backend/internal/denlink"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/keycloakadmin"
	"github.com/AGlElectronics/learn/backend/internal/learn"
	"github.com/AGlElectronics/learn/backend/internal/mail"
	"github.com/AGlElectronics/learn/backend/internal/notify"
)

// Deps groups the runtime dependencies the HTTP layer needs.
type Deps struct {
	Config       *config.Config
	Pool         *pgxpool.Pool
	Logger       zerolog.Logger
	Brand        brand.Provider
	OIDCVerifier *auth.OIDCVerifier
	// PortalVerifier also accepts OIDC_EXTRA_AUDIENCES, on /api/portal/*
	// only; nil = those routes use OIDCVerifier.
	PortalVerifier *auth.PortalVerifier
	Access         *auth.AccessConfig
	// Den is the pairing with Den; nil means a fresh standalone client.
	Den *denlink.Client
	// Keycloak is the read-only admin client for group sync; nil = off.
	Keycloak *keycloakadmin.Client
	// Files is object storage for the media library; nil = uploads off (503).
	Files learn.ObjectStore
	// Notifier delivers reminders (email, Telegram); nil = none.
	Notifier *notify.Notifier
	// Limiters overrides the rate limiter of a route group; the rest use
	// the defaults (see ratelimit.go).
	Limiters map[LimitName]Limiter
	// Service is the learn service the workers use (group matching and
	// Den's module settings set); nil = a new one.
	Service *learn.Service
	// Mail is the mail server switch, for the settings page; nil = no email.
	Mail *mail.Switch
}

// NewRouter builds the chi router with middleware, CORS, and routes.
// Version is passed in from cmd/server at build time.
func NewRouter(deps Deps, version string) http.Handler {
	r := chi.NewRouter()

	authCfg := deps.Config.Auth()
	r.Use(chimiddleware.RequestID)
	r.Use(authCfg.TrustedProxies.Middleware)
	r.Use(authCfg.StripDevHeaders)
	r.Use(authbase.SecurityHeaders)
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.Timeout(60 * time.Second))

	r.Use(hlog.NewHandler(deps.Logger))
	r.Use(hlog.AccessHandler(func(req *http.Request, status, size int, duration time.Duration) {
		hlog.FromRequest(req).Info().
			Str("method", req.Method).
			Str("path", req.URL.Path).
			Int("status", status).
			Int("size", size).
			Dur("duration", duration).
			Msg("http")
	}))
	r.Use(hlog.RequestIDHandler("request_id", "X-Request-ID"))

	// The dev headers (X-Dev-User, X-Dev-Groups) pass CORS only in
	// development with AUTH_MODE=dev; X-Request-ID is exposed.
	r.Use(authCfg.CORS(authbase.CORSOptions{
		AllowedOrigins: deps.Config.CORSAllowedOrigins,
		ExtraHeaders:   []string{"Accept-Language"},
	}))

	health := newHealthHandler(deps.Pool, version)
	resolver := auth.NewResolver(deps.Pool, deps.Access, deps.Config.GroupMatch)
	authMW := auth.Middleware(deps.Config, resolver, deps.OIDCVerifier)

	den := deps.Den
	if den == nil {
		den = denlink.New(deps.Pool, deps.Config.DenConfig(version), deps.Logger)
	}
	brandH := &brandHandler{provider: deps.Brand, den: den}
	audits := audit.New(deps.Pool, deps.Logger)
	svc := deps.Service
	if svc == nil {
		svc = learn.NewService(deps.Pool)
		svc.SetGroupMatch(deps.Config.GroupMatch)
		svc.SetModuleSettings(den.ModuleSettings)
	}
	courses := &courseHandler{svc: svc, audit: audits}
	courses.svc.SetFeedbackPepper(deps.Config.FeedbackPepper)
	resolver.SetGroupsHook(courses.svc.ApplyLoginGroups)
	groupSync := learn.NewGroupSync(courses.svc, deps.Keycloak, learn.GroupSyncIssuer(deps.Config), deps.Logger)
	courses.directory = groupSync
	policy := newGroupPolicy(deps.Config)
	limits := newLimitSet(deps.Limiters)
	reminders := learn.NewReminders(courses.svc, deps.Notifier, deps.Config.PublicBaseURL, OrgNameFunc(deps.Brand, den), deps.Logger)

	// Orchestrator probes — unauthenticated, served from the root so they
	// don't collide with any /api/* middleware.
	r.Get("/livez", health.livez)
	r.Get("/readyz", health.readyz)

	r.Route("/api", func(api chi.Router) {
		api.Use(den.RequirePaired)
		api.Get("/health", health.get)
		api.With(roleCatalogAuth(deps.Config.HRServiceToken, authMW)).
			Get("/role-catalog", roleCatalogHandler(deps.Access))
		api.Get("/den/state", den.StateHandler)
		api.Get("/qualification-catalog", qualificationCatalogHandler(courses.svc))
		api.Get("/internal/holdings", holdingsHandler(deps.Config, courses.svc))
		api.With(verifyLimiter().Middleware).Get("/public/verify/{code}", verifyHandler(courses.svc))
		api.With(auth.PortalMiddleware(deps.Config, resolver, deps.PortalVerifier, deps.OIDCVerifier)).
			Get("/portal/todos", portalTodosHandler(courses.svc, portalBaseURL(deps.Config)))

		api.Route("/v1", func(v1 chi.Router) {
			// Brand is public so the login screen can render before the
			// user authenticates.
			v1.Get("/brand", brandH.get)
			v1.Get("/brand/{role}", den.AssetHandler)
			v1.Get("/den/state", den.StateHandler)

			v1.Group(func(secure chi.Router) {
				secure.Use(authMW)
				secure.Get("/me", meHandler)
				prefs := preferencesHandler(den, courses.svc)
				secure.Get("/me/preferences", prefs)
				secure.Put("/me/preferences", prefs)
				secure.Route("/den", func(dr chi.Router) {
					dr.Use(requirePermission(domain.PermSettingsManage))
					dr.Get("/", den.StatusHandler)
					dr.With(auditOnSuccess(audits, audit.DenPaired, "den_link")).Post("/pair", den.PairHandler)
					dr.With(auditOnSuccess(audits, audit.DenSynced, "den_link")).Post("/sync", den.SyncHandler)
					dr.With(auditOnSuccess(audits, audit.DenUnpaired, "den_link")).Delete("/pair", den.UnpairHandler)
				})

				secure.With(limits.hook(LimitPeopleSearch)).Get("/users", courses.searchPeople)
				secure.Get("/course-staff", courses.listCourseStaff)
				secure.With(requirePermission(domain.PermSettingsManage)).Get("/audit", auditListHandler(audits))

				secure.Get("/courses/mine", courses.listMine)
				secure.Get("/courses/catalog", courses.listCatalog)
				secure.Get("/courses/teach", courses.listTeach)
				secure.Post("/courses", courses.create)

				secure.Route("/courses/{course}", func(cr chi.Router) {
					cr.Get("/", courses.get)
					cr.Patch("/", courses.update)
					cr.Post("/publish", courses.setPublished(true))
					cr.Post("/unpublish", courses.setPublished(false))
					cr.Post("/archive", courses.setArchived(true))
					cr.Post("/unarchive", courses.setArchived(false))
					cr.Post("/sections", courses.createSection)
					cr.Get("/enrolments", courses.listEnrolments)
					cr.Post("/enrolments", courses.enrol)
					cr.With(limits.hook(LimitSelfEnrol)).Post("/enrol", courses.selfEnrol)
				})

				secure.Patch("/sections/{section}", courses.updateSection)
				secure.Delete("/sections/{section}", courses.deleteSection)
				secure.Post("/sections/{section}/activities", courses.createActivity)

				secure.Patch("/activities/{activity}", courses.updateActivity)
				secure.Delete("/activities/{activity}", courses.deleteActivity)
				secure.Put("/activities/{activity}/completion", courses.setCompletion)

				secure.Delete("/enrolments/{enrolment}", courses.unenrol)

				mountQuizRoutes(secure, courses)
				mountMediaRoutes(secure, courses, deps.Files)
				mountRuleRoutes(secure, courses)
				mountSessionRoutes(secure, courses, deps.Config.PublicBaseURL, limits)
				mountGroupRoutes(secure, courses, groupSync, policy)
				mountStatsRoutes(secure, courses)
				mountFeedbackRoutes(secure, courses, limits)
				mountImportRoutes(secure, courses, deps.Files)
				mountQualificationRoutes(secure, courses, deps.Config.PublicBaseURL)
				mountReportRoutes(secure, courses)
				mountCopyRoutes(secure, courses)
				mountPathRoutes(secure, courses, groupSync, policy)
				mountTeamBoardRoutes(secure, courses)
				mountTranslationRoutes(secure, courses)
				mountChannelRoutes(secure, courses.svc, deps.Notifier)
				mountSettingsRoutes(secure, &settingsHandler{
					svc: courses.svc, audit: audits, cfg: deps.Config, mail: deps.Mail, directory: groupSync != nil,
				})
				secure.With(requirePermission(domain.PermSettingsManage)).Post("/reminders/run", runRemindersHandler(reminders))
			})
		})
	})

	return r
}
