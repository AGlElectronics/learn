package http

import (
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AGlElectronics/learn/backend/internal/audit"
	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/config"
	"github.com/AGlElectronics/learn/backend/internal/denlink"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/learn"
	"github.com/AGlElectronics/learn/backend/internal/mail"
)

// ModuleSettingsFunc is where the service layer reads Den's module settings
// for Learn (nil while standalone).
func ModuleSettingsFunc(den *denlink.Client) func() *denlink.ModuleSettings {
	return func() *denlink.ModuleSettings {
		if den == nil {
			return nil
		}
		return den.ModuleSettings()
	}
}

// settingsHandler serves Learn's module settings and erasure (settings.manage).
type settingsHandler struct {
	svc   *learn.Service
	audit *audit.Log
	cfg   *config.Config
	// mail reports which mail server Learn uses (nil: no email at all).
	mail *mail.Switch
	// directory reports whether a group directory client is configured.
	directory bool
}

func mountSettingsRoutes(r chi.Router, h *settingsHandler) {
	r.With(requirePermission(domain.PermSettingsManage)).Get("/settings", h.get)
	r.With(requirePermission(domain.PermSettingsManage)).Put("/settings", h.put)
	r.With(requirePermission(domain.PermSettingsManage)).Post("/users/{user}/erase", h.erase)
}

type settingsResponse struct {
	Telegram struct {
		// Configured: TELEGRAM_BOT_TOKEN is set. Enabled: the switch.
		Configured bool `json:"configured"`
		Enabled    bool `json:"enabled"`
		// ManagedInDen: Den's learn.telegram_enabled decides.
		ManagedInDen bool `json:"managed_in_den"`
	} `json:"telegram"`
	Retention struct {
		learn.RetentionSettings
		// Source: den (Den sets at least one key), learn, or default.
		Source string `json:"source"`
		// Editable is false while Den sets every retention key.
		Editable bool `json:"editable"`
		// ManagedInDen lists the keys Den sets (module settings keys).
		ManagedInDen []string `json:"managed_in_den"`
		// PendingLegalReview marks the built-in defaults.
		PendingLegalReview bool `json:"pending_legal_review"`
	} `json:"retention"`
	Mail struct {
		// Source: den (Den's smtp.* module settings), env (SMTP_*), or none.
		Source string `json:"source"`
		// Warning explains why Den's mail server is not used.
		Warning string `json:"warning,omitempty"`
	} `json:"mail"`
	GroupDirectory struct {
		Mode      config.GroupDirectoryMode `json:"mode"`
		Available bool                      `json:"available"`
		Linkable  []string                  `json:"linkable"`
	} `json:"group_directory"`
}

func (h *settingsHandler) current(r *http.Request) (settingsResponse, error) {
	var out settingsResponse
	ms, err := h.svc.GetModuleSettings(r.Context())
	if err != nil {
		return out, err
	}
	managed := h.svc.DenManaged()
	out.Telegram.Configured = strings.TrimSpace(h.cfg.TelegramBotToken) != ""
	out.Telegram.Enabled = ms.TelegramEnabled
	if on, ok := h.svc.DenTelegramEnabled(); ok {
		out.Telegram.Enabled, out.Telegram.ManagedInDen = on, true
	}
	ret, source, err := h.svc.EffectiveRetention(r.Context())
	if err != nil {
		return out, err
	}
	out.Retention.RetentionSettings = ret
	out.Retention.Source = source
	out.Retention.ManagedInDen = []string{}
	for _, key := range []string{learn.DenKeyInactiveUsersDays, learn.DenKeyAuditLogDays} {
		if managed[key] {
			out.Retention.ManagedInDen = append(out.Retention.ManagedInDen, key)
		}
	}
	out.Retention.Editable = len(out.Retention.ManagedInDen) < 2
	out.Retention.PendingLegalReview = source == learn.RetentionFromDefault
	out.Mail.Source, out.Mail.Warning = h.mail.Source(r.Context())
	out.GroupDirectory.Mode = newGroupPolicy(h.cfg).directory
	out.GroupDirectory.Available = h.directory
	out.GroupDirectory.Linkable = h.cfg.LinkableGroups
	if out.GroupDirectory.Linkable == nil {
		out.GroupDirectory.Linkable = []string{}
	}
	return out, nil
}

func (h *settingsHandler) get(w http.ResponseWriter, r *http.Request) {
	out, err := h.current(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// put changes telegram_enabled and/or retention keys. A key Den's module
// settings decide can't be changed here (409).
func (h *settingsHandler) put(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	var in struct {
		TelegramEnabled *bool                 `json:"telegram_enabled"`
		Retention       *learn.RetentionPatch `json:"retention"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	if in.TelegramEnabled == nil && in.Retention == nil {
		httperror.Render(w, r, httperror.Validation("nothing to change (give telegram_enabled or retention)", nil))
		return
	}
	if err := h.svc.SaveModuleSettings(r.Context(), u.ID, in.TelegramEnabled, in.Retention); err != nil {
		httperror.Render(w, r, err)
		return
	}
	details := map[string]any{}
	if in.TelegramEnabled != nil {
		details["telegram_enabled"] = *in.TelegramEnabled
	}
	if in.Retention != nil {
		details["retention"] = *in.Retention
	}
	h.audit.Record(r.Context(), actorOf(r), audit.Entry{Action: audit.SettingsUpdated, TargetType: "settings", TargetID: "learn", Details: details})
	h.get(w, r)
}

// erase anonymises a person (GDPR erasure): settings.manage in one of the
// person's organizations, with {"confirm": "ERASE"}.
func (h *settingsHandler) erase(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	id, err := parseUUIDParam(r, "user")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Confirm string `json:"confirm"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	if in.Confirm != "ERASE" {
		httperror.Render(w, r, httperror.Validation(`type ERASE in confirm to erase this person; this can't be undone`, nil))
		return
	}
	orgs, err := h.svc.UserOrganizations(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	allowed := slices.ContainsFunc(orgs, func(o uuid.UUID) bool { return u.HasInOrg(domain.PermSettingsManage, o) })
	if !allowed {
		httperror.Render(w, r, httperror.NotFound("user"))
		return
	}
	res, err := h.svc.EraseUser(r.Context(), id, actorOf(r))
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
