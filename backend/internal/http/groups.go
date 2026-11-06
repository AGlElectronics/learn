package http

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/hlog"

	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/learn"
)

// groupHandler owns Keycloak-group enrolment: the group picker and the
// groups linked to a course. Links follow requireEnrolManage (enrol.manage
// plus edit rights on the course). sync is nil when the Keycloak admin
// client is not configured: links still work from the login-time group
// cache, but listing groups and "Sync now" answer 503.
type groupHandler struct {
	*courseHandler
	sync *learn.GroupSync
}

// mountGroupRoutes registers the group routes on the authenticated
// /api/v1 router.
func mountGroupRoutes(r chi.Router, courses *courseHandler, sync *learn.GroupSync) {
	h := &groupHandler{courseHandler: courses, sync: sync}

	r.Get("/groups", h.listGroups)
	r.Get("/courses/{course}/group-links", h.listLinks)
	r.Post("/courses/{course}/group-links", h.addLink)
	r.Delete("/courses/{course}/group-links", h.removeLink)
	r.Post("/courses/{course}/group-links/sync", h.syncNow)
}

var errSyncUnavailable = httperror.New(httperror.CodeUnavailable, "Keycloak group sync is not configured")

func (h *groupHandler) listGroups(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	if !u.HasAny(domain.PermCourseManage) && !u.HasAny(domain.PermCourseTeach) {
		httperror.Render(w, r, httperror.ErrForbidden)
		return
	}
	if h.sync == nil {
		httperror.Render(w, r, errSyncUnavailable)
		return
	}
	groups, err := h.sync.ListGroups(r.Context())
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	out := make([]map[string]string, 0, len(groups))
	for _, g := range groups {
		out = append(out, map[string]string{"id": g.ID, "name": g.Name, "path": g.Path})
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}

func (h *groupHandler) loadManaged(r *http.Request) (learn.Course, *auth.User, error) {
	course, u, err := h.loadCourse(r)
	if err != nil {
		return course, nil, err
	}
	return course, u, h.requireEnrolManage(r, course)
}

func (h *groupHandler) listLinks(w http.ResponseWriter, r *http.Request) {
	course, _, err := h.loadManaged(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	links, err := h.svc.ListGroupLinks(r.Context(), course.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": links, "sync_available": h.sync != nil})
}

// addLink links a group, enrols the members Learn already knows, then runs
// a Keycloak sync for the course when one is configured.
func (h *groupHandler) addLink(w http.ResponseWriter, r *http.Request) {
	course, u, err := h.loadManaged(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		GroupName string `json:"group_name"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	name := strings.TrimSpace(in.GroupName)
	if h.sync != nil && name != "" {
		groups, err := h.sync.ListGroups(r.Context())
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		found := false
		for _, g := range groups {
			found = found || g.Name == name
		}
		if !found {
			httperror.Render(w, r, httperror.Validation("there is no Keycloak group called "+name, nil))
			return
		}
	}
	link, err := h.svc.AddGroupLink(r.Context(), course.ID, name, u.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	resp := map[string]any{"link": link, "sync": nil}
	if h.sync != nil {
		res, err := h.sync.SyncCourse(r.Context(), course.ID)
		if err != nil {
			hlog.FromRequest(r).Error().Err(err).Msg("group sync after linking failed")
			resp["sync_error"] = "the group was linked but the first sync failed; try Sync now"
		} else {
			resp["sync"] = res
			if link, err = h.svc.GetGroupLink(r.Context(), course.ID, name); err == nil {
				resp["link"] = link
			}
		}
	}
	writeJSON(w, http.StatusCreated, resp)
}

// removeLink unlinks ?group=NAME. Synced learners in no remaining linked
// group turn inactive; nothing is deleted.
func (h *groupHandler) removeLink(w http.ResponseWriter, r *http.Request) {
	course, _, err := h.loadManaged(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("group"))
	if name == "" {
		httperror.Render(w, r, httperror.BadRequest("the group query parameter is required"))
		return
	}
	n, err := h.svc.RemoveGroupLink(r.Context(), course.ID, name)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deactivated": n})
}

func (h *groupHandler) syncNow(w http.ResponseWriter, r *http.Request) {
	course, _, err := h.loadManaged(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if h.sync == nil {
		httperror.Render(w, r, errSyncUnavailable)
		return
	}
	res, err := h.sync.SyncCourse(r.Context(), course.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
