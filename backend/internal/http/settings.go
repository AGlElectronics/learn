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
)

// DenSettingsFunc exposes Den's module settings for Learn (nil when
// standalone) to the service layer.
func DenSettingsFunc(den *denlink.Client) learn.DenSettings {
	return func() (map[string]any, bool) {
		if den == nil {
			return nil, false
		}
		b := den.Bundle()
		if b == nil {
			return nil, false
		}
		return b.Settings.Data, true
	}
}

// settingsHandler serves Learn's module settings and erasure (settings.manage).
type settingsHandler struct {
	svc   *learn.Service
	audit *audit.Log
	den   learn.DenSettings
	cfg   *config.Config
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
	} `json:"telegram"`
	Retention struct {
		learn.RetentionSettings
		// Source: den (Den's module settings decide), learn, or default.
		Source string `json:"source"`
		// Editable is false while Den decides.
		Editable bool `json:"editable"`
		// PendingLegalReview marks the built-in defaults.
		PendingLegalReview bool `json:"pending_legal_review"`
	} `json:"retention"`
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
	out.Telegram.Configured = strings.TrimSpace(h.cfg.TelegramBotToken) != ""
	out.Telegram.Enabled = ms.TelegramEnabled
	ret, source, err := h.svc.EffectiveRetention(r.Context(), h.den)
	if err != nil {
		return out, err
	}
	out.Retention.RetentionSettings = ret
	out.Retention.Source = source
	out.Retention.Editable = source != learn.RetentionFromDen
	out.Retention.PendingLegalReview = source == learn.RetentionFromDefault
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

// put changes telegram_enabled and/or retention. Retention can't be changed
// here while Den's module settings decide it (409).
func (h *settingsHandler) put(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	var in struct {
		TelegramEnabled *bool                    `json:"telegram_enabled"`
		Retention       *learn.RetentionSettings `json:"retention"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	if in.TelegramEnabled == nil && in.Retention == nil {
		httperror.Render(w, r, httperror.Validation("nothing to change (give telegram_enabled or retention)", nil))
		return
	}
	if in.Retention != nil {
		if _, source, err := h.svc.EffectiveRetention(r.Context(), h.den); err == nil && source == learn.RetentionFromDen {
			httperror.Render(w, r, httperror.Conflict("retention is set in Den (Admin → Modules → Learn) while Learn is paired"))
			return
		}
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
