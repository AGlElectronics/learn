package http

import (
	"net/http"
	"strconv"
	"strings"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/AGlElectronics/learn/backend/internal/audit"
	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// auditListHandler serves GET /api/v1/audit (settings.manage): entries of
// the caller's organizations, newest first. Query: action, target_type,
// target_id, course, actor, before (the next_before of the previous page),
// limit (1-200, default 50).
func auditListHandler(log *audit.Log) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := auth.MustFromContext(r.Context())
		if err != nil {
			httperror.Render(w, r, httperror.ErrUnauthorized)
			return
		}
		orgs := orgsWith(u, domain.PermSettingsManage)
		if len(orgs) == 0 {
			httperror.Render(w, r, httperror.New(httperror.CodeForbidden, "missing permission: "+domain.PermSettingsManage))
			return
		}
		q := r.URL.Query()
		f := audit.Filter{
			Action:        strings.TrimSpace(q.Get("action")),
			TargetType:    strings.TrimSpace(q.Get("target_type")),
			TargetID:      strings.TrimSpace(q.Get("target_id")),
			Organizations: orgs,
		}
		for key, dst := range map[string]**uuid.UUID{"course": &f.CourseID, "actor": &f.ActorID} {
			if raw := strings.TrimSpace(q.Get(key)); raw != "" {
				id, err := uuid.Parse(raw)
				if err != nil {
					httperror.Render(w, r, httperror.BadRequest("invalid "+key+" id: "+raw))
					return
				}
				*dst = &id
			}
		}
		for key, dst := range map[string]*int64{"before": &f.BeforeID} {
			if raw := strings.TrimSpace(q.Get(key)); raw != "" {
				n, err := strconv.ParseInt(raw, 10, 64)
				if err != nil || n < 0 {
					httperror.Render(w, r, httperror.BadRequest(key+" must be a positive number"))
					return
				}
				*dst = n
			}
		}
		if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 200 {
				httperror.Render(w, r, httperror.BadRequest("limit must be between 1 and 200"))
				return
			}
			f.Limit = n
		}
		entries, next, err := log.List(r.Context(), f)
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		var nextBefore *int64
		if next > 0 {
			nextBefore = &next
		}
		writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "next_before": nextBefore})
	}
}

// auditOnSuccess records action after a handler it wraps answered 2xx. It
// audits handlers that live outside this package (the Den pairing routes).
func auditOnSuccess(log *audit.Log, action, targetType string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			if status := ww.Status(); status >= 200 && status < 300 {
				log.Record(r.Context(), actorOf(r), audit.Entry{Action: action, TargetType: targetType, TargetID: "den"})
			}
		})
	}
}
