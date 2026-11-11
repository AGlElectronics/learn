package http

import (
	"net/http"
	"strings"
	"time"

	"github.com/AGlElectronics/learn/backend/internal/config"
	"github.com/AGlElectronics/learn/backend/internal/denlink"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/learn"
)

// portalBaseURL is the SPA origin for to-do links: PUBLIC_BASE_URL, else
// APP_BASE_URL.
func portalBaseURL(cfg *config.Config) string {
	if cfg.PublicBaseURL != "" {
		return cfg.PublicBaseURL
	}
	return cfg.AppBaseURL
}

// portalTodosHandler serves GET /api/portal/todos, the suite-wide to-do
// contract Den's backend reads: {"items": [{id, title, detail, due_at, url,
// severity}]} for the person Den names. Mount it with denlink.RequireDen.
// URLs are absolute on baseURL (PUBLIC_BASE_URL) so Den can link straight
// into Learn. A person who has never signed in has no to-dos.
func portalTodosHandler(svc *learn.Service, baseURL string) http.HandlerFunc {
	baseURL = strings.TrimRight(baseURL, "/")
	return func(w http.ResponseWriter, r *http.Request) {
		caller, ok := denlink.PortalCallerFrom(r.Context())
		if !ok {
			httperror.Render(w, r, httperror.ErrUnauthorized)
			return
		}
		userID, found, err := svc.UserIDByIdentity(r.Context(), caller.Issuer, caller.Subject)
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		items := []learn.PortalTodo{}
		if found {
			pref, err := svc.UserLanguage(r.Context(), userID)
			if err != nil {
				httperror.Render(w, r, err)
				return
			}
			langs := learn.NewLangs(r.URL.Query().Get("lang"), pref, r.Header.Get("Accept-Language"))
			items, err = svc.PortalTodos(r.Context(), userID, time.Now(), langs)
			if err != nil {
				httperror.Render(w, r, err)
				return
			}
			if items == nil {
				items = []learn.PortalTodo{}
			}
			for i := range items {
				items[i].URL = baseURL + items[i].Path
			}
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}
