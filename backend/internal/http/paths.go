package http

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/hlog"

	"github.com/AGlElectronics/learn/backend/internal/audit"
	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/learn"
)

// pathHandler owns learning paths. Managing a path (and its courses,
// enrolments and group links) needs course.manage in the path's org;
// learners see their own paths under /me/paths and may join self-enrol
// paths. sync is nil when Keycloak group sync is off.
type pathHandler struct {
	svc    *learn.Service
	sync   *learn.GroupSync
	audit  *audit.Log
	policy groupPolicy
}

// pathEntry is an audit entry about something on a learning path.
func pathEntry(action string, p learn.Path, targetType, targetID string, details map[string]any) audit.Entry {
	org := p.OrganizationID
	if details == nil {
		details = map[string]any{}
	}
	details["path_id"] = p.ID
	return audit.Entry{Action: action, TargetType: targetType, TargetID: targetID, OrganizationID: &org, Details: details}
}

func mountPathRoutes(r chi.Router, courses *courseHandler, sync *learn.GroupSync, policy groupPolicy) {
	h := &pathHandler{svc: courses.svc, sync: sync, audit: courses.audit, policy: policy}
	r.Get("/me/paths", h.mine)
	r.Get("/paths", h.list)
	r.Post("/paths", h.create)
	r.Get("/paths/{path}", h.get)
	r.Patch("/paths/{path}", h.update)
	r.Delete("/paths/{path}", h.remove)
	r.Put("/paths/{path}/courses", h.setCourses)
	r.Post("/paths/{path}/courses", h.addCourse)
	r.Delete("/paths/{path}/courses/{course}", h.removeCourse)
	r.Get("/paths/{path}/enrolments", h.listEnrolments)
	r.Post("/paths/{path}/enrolments", h.enrol)
	r.Post("/paths/{path}/enrol", h.selfEnrol)
	r.Delete("/path-enrolments/{enrolment}", h.unenrol)
	r.Get("/paths/{path}/group-links", h.listLinks)
	r.Post("/paths/{path}/group-links", h.addLink)
	r.Delete("/paths/{path}/group-links", h.removeLink)
	r.Post("/paths/{path}/group-links/sync", h.syncNow)
}

func manageOrgs(u *auth.User) []uuid.UUID {
	out := []uuid.UUID{}
	for orgID := range u.OrgPermissions {
		if u.HasInOrg(domain.PermCourseManage, orgID) {
			out = append(out, orgID)
		}
	}
	return out
}

// loadManaged loads {path} for a caller with course.manage in its org;
// anyone else gets a 404.
func (h *pathHandler) loadManaged(r *http.Request) (learn.Path, *auth.User, error) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		return learn.Path{}, nil, httperror.ErrUnauthorized
	}
	id, err := parseUUIDParam(r, "path")
	if err != nil {
		return learn.Path{}, nil, err
	}
	p, err := h.svc.GetPath(r.Context(), id)
	if err != nil {
		return learn.Path{}, nil, err
	}
	if !u.HasInOrg(domain.PermCourseManage, p.OrganizationID) {
		return learn.Path{}, nil, httperror.NotFound("path")
	}
	return p, u, nil
}

func (h *pathHandler) mine(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	paths, available, err := h.svc.MyPaths(r.Context(), u.ID)
	if err == nil {
		err = h.svc.TranslateMyPaths(r.Context(), paths, available, requestLangs(r, h.svc))
	}
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"paths": paths, "available": available})
}

func (h *pathHandler) list(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	orgs := manageOrgs(u)
	if len(orgs) == 0 {
		httperror.Render(w, r, httperror.New(httperror.CodeForbidden, "missing permission: "+domain.PermCourseManage))
		return
	}
	out, err := h.svc.ListPaths(r.Context(), orgs)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"paths": out})
}

type pathBody struct {
	Name                  *string      `json:"name"`
	Description           *string      `json:"description"`
	Sequential            *bool        `json:"sequential"`
	SelfEnrol             *bool        `json:"self_enrol"`
	AwardsQualificationID nullableUUID `json:"awards_qualification_id"`
}

func (b pathBody) input() learn.PathInput {
	return learn.PathInput{
		Name: b.Name, Description: b.Description, Sequential: b.Sequential, SelfEnrol: b.SelfEnrol,
		AwardsQualificationID: b.AwardsQualificationID.Value,
		ClearQualification:    b.AwardsQualificationID.Set && b.AwardsQualificationID.Value == nil,
	}
}

func (h *pathHandler) create(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	orgs := manageOrgs(u)
	if len(orgs) == 0 {
		httperror.Render(w, r, httperror.New(httperror.CodeForbidden, "missing permission: "+domain.PermCourseManage))
		return
	}
	var in pathBody
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	p, err := h.svc.CreatePath(r.Context(), orgs[0], u.ID, in.input())
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (h *pathHandler) get(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.loadManaged(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *pathHandler) update(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.loadManaged(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in pathBody
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	out, err := h.svc.UpdatePath(r.Context(), p, in.input())
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *pathHandler) remove(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.loadManaged(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.svc.DeletePath(r.Context(), p.ID); err != nil {
		httperror.Render(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func pathCourseIDs(p learn.Path) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(p.Courses))
	for _, c := range p.Courses {
		ids = append(ids, c.CourseID)
	}
	return ids
}

// setCourses replaces the ordered course list: {course_ids: [...]}.
func (h *pathHandler) setCourses(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.loadManaged(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		CourseIDs []uuid.UUID `json:"course_ids"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	out, err := h.svc.SetPathCourses(r.Context(), p, in.CourseIDs)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// addCourse appends a course: {course_id}.
func (h *pathHandler) addCourse(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.loadManaged(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		CourseID uuid.UUID `json:"course_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	ids := pathCourseIDs(p)
	for _, id := range ids {
		if id == in.CourseID {
			httperror.Render(w, r, httperror.Conflict("this course is already on the path"))
			return
		}
	}
	out, err := h.svc.SetPathCourses(r.Context(), p, append(ids, in.CourseID))
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *pathHandler) removeCourse(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.loadManaged(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	courseID, err := parseUUIDParam(r, "course")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	ids := []uuid.UUID{}
	found := false
	for _, id := range pathCourseIDs(p) {
		if id == courseID {
			found = true
			continue
		}
		ids = append(ids, id)
	}
	if !found {
		httperror.Render(w, r, httperror.NotFound("path course"))
		return
	}
	out, err := h.svc.SetPathCourses(r.Context(), p, ids)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *pathHandler) listEnrolments(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.loadManaged(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	out, err := h.svc.ListPathEnrolments(r.Context(), p.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enrolments": out})
}

func (h *pathHandler) enrol(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.loadManaged(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		UserID uuid.UUID `json:"user_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	e, err := h.svc.EnrolInPath(r.Context(), p.ID, in.UserID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	h.audit.Record(r.Context(), actorOf(r), pathEntry(audit.PathEnrolmentAdded, p, "path_enrolment", e.ID.String(),
		map[string]any{"user_id": in.UserID}))
	writeJSON(w, http.StatusCreated, e)
}

func (h *pathHandler) selfEnrol(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	id, err := parseUUIDParam(r, "path")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	p, err := h.svc.GetPath(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	e, err := h.svc.SelfEnrolPath(r.Context(), p, u.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

func (h *pathHandler) unenrol(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	id, err := parseUUIDParam(r, "enrolment")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	p, err := h.svc.PathEnrolmentPath(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if !u.HasInOrg(domain.PermCourseManage, p.OrganizationID) {
		httperror.Render(w, r, httperror.NotFound("path enrolment"))
		return
	}
	if err := h.svc.RemovePathEnrolment(r.Context(), id); err != nil {
		httperror.Render(w, r, err)
		return
	}
	h.audit.Record(r.Context(), actorOf(r), pathEntry(audit.PathEnrolmentGone, p, "path_enrolment", id.String(), nil))
	w.WriteHeader(http.StatusNoContent)
}

func (h *pathHandler) listLinks(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.loadManaged(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	links, err := h.svc.ListPathGroupLinks(r.Context(), p.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": links, "sync_available": h.sync != nil, "directory": h.policy.directory})
}

func (h *pathHandler) addLink(w http.ResponseWriter, r *http.Request) {
	p, u, err := h.loadManaged(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in groupLinkBody
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	path, err := checkGroupLink(r, h.sync, h.policy, u, p.OrganizationID, in.path())
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	link, err := h.svc.AddPathGroupLink(r.Context(), p.ID, path, u.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	h.audit.Record(r.Context(), actorOf(r), pathEntry(audit.GroupLinkAdded, p, "path_group_link", link.GroupName,
		map[string]any{"group_path": link.GroupPath}))
	resp := map[string]any{"link": link, "sync": nil}
	if h.sync != nil {
		res, err := h.sync.SyncPath(r.Context(), p.ID)
		if err != nil {
			hlog.FromRequest(r).Error().Err(err).Msg("path group sync after linking failed")
			resp["sync_error"] = "the group was linked but the first sync failed; try Sync now"
		} else {
			resp["sync"] = res
		}
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *pathHandler) removeLink(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.loadManaged(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("group"))
	if name == "" {
		httperror.Render(w, r, httperror.BadRequest("the group query parameter is required"))
		return
	}
	n, err := h.svc.RemovePathGroupLink(r.Context(), p.ID, name)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	h.audit.Record(r.Context(), actorOf(r), pathEntry(audit.GroupLinkRemoved, p, "path_group_link", learn.NormalizeGroup(name),
		map[string]any{"deactivated": n}))
	writeJSON(w, http.StatusOK, map[string]any{"deactivated": n})
}

func (h *pathHandler) syncNow(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.loadManaged(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if h.sync == nil {
		httperror.Render(w, r, errSyncUnavailable)
		return
	}
	res, err := h.sync.SyncPath(r.Context(), p.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
