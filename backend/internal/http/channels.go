package http

import (
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"

	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/learn"
	"github.com/AGlElectronics/learn/backend/internal/notify"
)

// mountChannelRoutes registers the caller's notification channels:
//
//	GET /me/channels — {email, telegram_chat_id, email_enabled, telegram_enabled}
//	PUT /me/channels — {telegram_chat_id: "123" | ""}
func mountChannelRoutes(r chi.Router, svc *learn.Service, notifier *notify.Notifier) {
	enabled := notifier.Channels()
	get := func(w http.ResponseWriter, r *http.Request) {
		u, err := auth.MustFromContext(r.Context())
		if err != nil {
			httperror.Render(w, r, httperror.ErrUnauthorized)
			return
		}
		channels, err := svc.UserChannels(r.Context(), u.ID)
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"email":            u.Email,
			"telegram_chat_id": channels[notify.ChannelTelegram],
			"email_enabled":    slices.Contains(enabled, notify.ChannelEmail),
			"telegram_enabled": slices.Contains(enabled, notify.ChannelTelegram),
		})
	}
	r.Get("/me/channels", get)
	r.Put("/me/channels", func(w http.ResponseWriter, r *http.Request) {
		u, err := auth.MustFromContext(r.Context())
		if err != nil {
			httperror.Render(w, r, httperror.ErrUnauthorized)
			return
		}
		var in struct {
			TelegramChatID *string `json:"telegram_chat_id"`
		}
		if err := decodeJSON(r, &in); err != nil {
			httperror.Render(w, r, err)
			return
		}
		if in.TelegramChatID != nil {
			if err := svc.SetTelegramChatID(r.Context(), u.ID, *in.TelegramChatID); err != nil {
				httperror.Render(w, r, err)
				return
			}
		}
		get(w, r)
	})
}
