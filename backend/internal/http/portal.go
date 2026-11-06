package http

import (
	"net/http"
	"strings"
	"time"

	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/config"
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
// contract Den's portal widget reads: {"items": [{id, title, detail,
// due_at, url, severity}]} for the caller only. URLs are absolute on
// baseURL (PUBLIC_BASE_URL) so Den can link straight into Learn.
func portalTodosHandler(svc *learn.Service, baseURL string) http.HandlerFunc {
	baseURL = strings.TrimRight(baseURL, "/")
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := auth.MustFromContext(r.Context())
		if err != nil {
			httperror.Render(w, r, httperror.ErrUnauthorized)
			return
		}
		items, err := svc.PortalTodos(r.Context(), u.ID, time.Now(), requestLangs(r, svc))
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		for i := range items {
			items[i].URL = baseURL + items[i].Path
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}
