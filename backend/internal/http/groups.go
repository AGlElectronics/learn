package http

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/hlog"

	"github.com/AGlElectronics/learn/backend/internal/audit"
	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/config"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/learn"
)

// groupPolicy is what limits group links: which groups trainers may link
// (LEARN_LINKABLE_GROUPS) and whether a group directory is configured
// (GROUP_DIRECTORY).
type groupPolicy struct {
	linkable  []string
	directory config.GroupDirectoryMode
}

func newGroupPolicy(cfg *config.Config) groupPolicy {
	mode := cfg.GroupDirectory
	if mode == "" {
		mode = config.GroupDirectoryKeycloak
	}
	return groupPolicy{linkable: cfg.LinkableGroups, directory: mode}
}

// mayLink reports whether the caller may link the group at path in an
// organization: course.manage links any group, everyone else only the
// groups LEARN_LINKABLE_GROUPS allows.
func (p groupPolicy) mayLink(u *auth.User, orgID uuid.UUID, path string) bool {
	return u.HasInOrg(domain.PermCourseManage, orgID) || learn.GroupLinkable(path, p.linkable)
}

// groupHandler owns Keycloak-group enrolment: the group picker and the
// groups linked to a course. Links follow requireEnrolManage (enrol.manage
// plus edit rights on the course) and groupPolicy. sync is nil when the
// group directory is off or not configured: links still work from the
// login-time group cache, but listing groups and "Sync now" answer 503.
type groupHandler struct {
	*courseHandler
	sync   *learn.GroupSync
	policy groupPolicy
}

// mountGroupRoutes registers the group routes on the authenticated
// /api/v1 router.
func mountGroupRoutes(r chi.Router, courses *courseHandler, sync *learn.GroupSync, policy groupPolicy) {
	h := &groupHandler{courseHandler: courses, sync: sync, policy: policy}

	r.Get("/groups", h.listGroups)
	r.Get("/courses/{course}/group-links", h.listLinks)
	r.Post("/courses/{course}/group-links", h.addLink)
	r.Delete("/courses/{course}/group-links", h.removeLink)
	r.Post("/courses/{course}/group-links/sync", h.syncNow)
}

var errSyncUnavailable = httperror.New(httperror.CodeUnavailable, "the group directory is off or not configured (GROUP_DIRECTORY, KEYCLOAK_*)")

// listGroups serves the group picker. course.manage holders see every
// group; trainers only the groups they may link.
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
	all := u.HasAny(domain.PermCourseManage)
	out := make([]map[string]string, 0, len(groups))
	for _, g := range groups {
		if !all && !learn.GroupLinkable(g.Path, h.policy.linkable) {
			continue
		}
		out = append(out, map[string]string{"id": g.ID, "name": g.Name, "path": g.Path})
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out, "limited": !all, "linkable": h.policy.linkable})
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
	writeJSON(w, http.StatusOK, map[string]any{"links": links, "sync_available": h.sync != nil, "directory": h.policy.directory})
}

// groupLinkBody is the body of POST …/group-links. group_name is the old
// field name and is read as a path.
type groupLinkBody struct {
	GroupPath string `json:"group_path"`
	GroupName string `json:"group_name"`
}

func (b groupLinkBody) path() string {
	if p := strings.TrimSpace(b.GroupPath); p != "" {
		return p
	}
	return strings.TrimSpace(b.GroupName)
}

// checkGroupLink applies the link policy and, with a directory, looks the
// group up by full path. It returns the path as the directory spells it.
func checkGroupLink(r *http.Request, sync *learn.GroupSync, policy groupPolicy, u *auth.User, orgID uuid.UUID, path string) (string, error) {
	if path == "" {
		return "", httperror.Validation("group_path is required (the group's full path, e.g. /learn/cohort-a)", nil)
	}
	if !policy.mayLink(u, orgID, path) {
		return "", httperror.New(httperror.CodeForbidden,
			"trainers may only link the groups set in LEARN_LINKABLE_GROUPS ("+strings.Join(policy.linkable, ", ")+")")
	}
	if sync == nil {
		return path, nil
	}
	g, ok, err := sync.FindGroup(r.Context(), path)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", httperror.Validation("there is no group at "+path+" in the directory; give the full path, e.g. /learn/cohort-a", nil)
	}
	return g.Path, nil
}

// addLink links a group by full path, enrols the members Learn already
// knows, then runs a Keycloak sync for the course when one is configured.
func (h *groupHandler) addLink(w http.ResponseWriter, r *http.Request) {
	course, u, err := h.loadManaged(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in groupLinkBody
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	path, err := checkGroupLink(r, h.sync, h.policy, u, course.OrganizationID, in.path())
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	link, err := h.svc.AddGroupLink(r.Context(), course.ID, path, u.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	h.record(r, courseEntry(audit.GroupLinkAdded, course, "course_group_link", link.GroupName,
		map[string]any{"group_path": link.GroupPath}))
	resp := map[string]any{"link": link, "sync": nil}
	if h.sync != nil {
		res, err := h.sync.SyncCourse(r.Context(), course.ID)
		if err != nil {
			hlog.FromRequest(r).Error().Err(err).Msg("group sync after linking failed")
			resp["sync_error"] = "the group was linked but the first sync failed; try Sync now"
		} else {
			resp["sync"] = res
			if link, err = h.svc.GetGroupLink(r.Context(), course.ID, link.GroupName); err == nil {
				resp["link"] = link
			}
		}
	}
	writeJSON(w, http.StatusCreated, resp)
}

// removeLink unlinks ?group=PATH (or the link's group_name key). Synced
// learners in no remaining linked group turn inactive; nothing is deleted.
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
	h.record(r, courseEntry(audit.GroupLinkRemoved, course, "course_group_link", learn.NormalizeGroup(name),
		map[string]any{"deactivated": n}))
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
