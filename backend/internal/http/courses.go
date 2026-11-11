package http

import (
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/AGlElectronics/learn/backend/internal/audit"
	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/learn"
)

// courseHandler owns every course-scoped route. Authorization model:
//
//   - course.manage (admin)  — full authority on every course in the org.
//   - course.teach (trainer) — authority only on courses the caller staffs
//     (an enrolment with role "trainer").
//   - enrolment              — lets a person read a published course and
//     mark pages complete. No IdP group involved.
//
// The owning organization is always resolved from the resource row, never
// from the request body.
type courseHandler struct {
	svc   *learn.Service
	audit *audit.Log
	// directory is the identity provider's people and group directory;
	// nil when GROUP_DIRECTORY is off or KEYCLOAK_* is unset.
	directory *learn.GroupSync
}

// actorOf is the signed-in caller as an audit actor.
func actorOf(r *http.Request) audit.Actor {
	u, ok := auth.FromContext(r.Context())
	if !ok {
		return audit.System
	}
	id := u.ID
	return audit.Actor{UserID: &id, Name: u.DisplayName}
}

// record writes an audit entry for the caller. It never fails the request.
func (h *courseHandler) record(r *http.Request, e audit.Entry) {
	h.audit.Record(r.Context(), actorOf(r), e)
}

// courseEntry is an audit entry about something on a course.
func courseEntry(action string, c learn.Course, targetType, targetID string, details map[string]any) audit.Entry {
	org, id := c.OrganizationID, c.ID
	return audit.Entry{Action: action, TargetType: targetType, TargetID: targetID,
		OrganizationID: &org, CourseID: &id, Details: details}
}

// mayManageStaff reports whether the caller may add and remove trainers on
// the course (staff.manage in its organization).
func mayManageStaff(r *http.Request, course learn.Course) bool {
	u, ok := auth.FromContext(r.Context())
	return ok && u.HasInOrg(domain.PermStaffManage, course.OrganizationID)
}

// maySeeEmails reports whether the caller may see people's email addresses
// in an organization: course.manage or staff.manage, never trainers.
func maySeeEmails(u *auth.User, orgID uuid.UUID) bool {
	return u.HasInOrg(domain.PermCourseManage, orgID) || u.HasInOrg(domain.PermStaffManage, orgID)
}

// canEdit reports whether the caller may change the course or its content.
func (h *courseHandler) canEdit(r *http.Request, course learn.Course) (bool, error) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		return false, httperror.ErrUnauthorized
	}
	if u.HasInOrg(domain.PermCourseManage, course.OrganizationID) {
		return true, nil
	}
	if u.HasInOrg(domain.PermCourseTeach, course.OrganizationID) {
		return h.svc.IsStaff(r.Context(), course.ID, u.ID)
	}
	return false, nil
}

// errArchived refuses changes to an archived course.
var errArchived = httperror.New(httperror.CodeArchived, "this course is archived and read-only; unarchive it to make changes")

// requireEdit renders unpublished and archived courses invisible: outsiders
// get the same 404 a missing course would give, never a 403 confirming it
// exists. An archived course is read-only for its staff too: every request
// other than GET answers 409 archived.
func (h *courseHandler) requireEdit(r *http.Request, course learn.Course) error {
	ok, err := h.canEdit(r, course)
	if err != nil {
		return err
	}
	if ok {
		if course.Archived() && r.Method != http.MethodGet && r.Method != http.MethodHead {
			return errArchived
		}
		return nil
	}
	if !course.Published || course.Archived() {
		return httperror.NotFound("course")
	}
	return httperror.ErrForbidden
}

// requireEnrolManage guards enrolment changes: the caller must hold
// enrol.manage in the course's org AND be able to edit the course (admin
// everywhere, trainer only where they staff).
func (h *courseHandler) requireEnrolManage(r *http.Request, course learn.Course) error {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		return httperror.ErrUnauthorized
	}
	if !u.HasInOrg(domain.PermEnrolManage, course.OrganizationID) {
		if !course.Published {
			return httperror.NotFound("course")
		}
		return httperror.ErrForbidden
	}
	return h.requireEdit(r, course)
}

// ----------------------------------------------------------------------------
// Course lists
// ----------------------------------------------------------------------------

func (h *courseHandler) listMine(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	out, err := h.svc.ListMine(r.Context(), u.ID)
	if err == nil {
		err = h.svc.TranslateSummaries(r.Context(), out, requestLangs(r, h.svc))
	}
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"courses": out})
}

func (h *courseHandler) listCatalog(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	out, err := h.svc.ListCatalog(r.Context(), u.ID)
	if err == nil {
		err = h.svc.TranslateSummaries(r.Context(), out, requestLangs(r, h.svc))
	}
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"courses": out})
}

func (h *courseHandler) listTeach(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	if !u.HasAny(domain.PermCourseTeach) && !u.HasAny(domain.PermCourseManage) {
		httperror.Render(w, r, httperror.ErrForbidden)
		return
	}
	manageOrgs := []uuid.UUID{}
	for orgID := range u.OrgPermissions {
		if u.HasInOrg(domain.PermCourseManage, orgID) {
			manageOrgs = append(manageOrgs, orgID)
		}
	}
	archived := r.URL.Query().Get("archived") == "true"
	out, err := h.svc.ListTeach(r.Context(), u.ID, manageOrgs, archived)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"courses": out})
}

// setArchived archives or unarchives a course (course staff with edit
// rights). There is no delete: archiving is reversible.
func (h *courseHandler) setArchived(archived bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		course, u, err := h.loadCourse(r)
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		ok, err := h.canEdit(r, course)
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		if !ok {
			if !course.Published || course.Archived() {
				httperror.Render(w, r, httperror.NotFound("course"))
				return
			}
			httperror.Render(w, r, httperror.ErrForbidden)
			return
		}
		updated, err := h.svc.SetArchived(r.Context(), course.ID, u.ID, archived)
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		if course.Archived() != archived {
			action := audit.CourseUnarchived
			if archived {
				action = audit.CourseArchived
			}
			h.record(r, courseEntry(action, course, "course", course.ID.String(), nil))
		}
		writeJSON(w, http.StatusOK, updated)
	}
}

// ----------------------------------------------------------------------------
// Course CRUD
// ----------------------------------------------------------------------------

func (h *courseHandler) create(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	var in struct {
		Title     string `json:"title"`
		Summary   string `json:"summary"`
		SelfEnrol bool   `json:"self_enrol"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	// The course is created in the organization where the caller may teach
	// or manage. One org in this slice; first match wins if there are more.
	var orgID uuid.UUID
	for id := range u.OrgPermissions {
		if u.HasInOrg(domain.PermCourseManage, id) || u.HasInOrg(domain.PermCourseTeach, id) {
			orgID = id
			break
		}
	}
	if orgID == uuid.Nil {
		httperror.Render(w, r, httperror.ErrForbidden)
		return
	}
	course, err := h.svc.CreateCourse(r.Context(), u.ID, learn.CreateCourseInput{
		OrganizationID: orgID,
		Title:          in.Title,
		Summary:        in.Summary,
		SelfEnrol:      in.SelfEnrol,
	})
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, course)
}

// get returns the full course detail. Staff always see it; everyone else
// needs an enrolment on a published course.
func (h *courseHandler) get(w http.ResponseWriter, r *http.Request) {
	course, u, err := h.loadCourse(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	editable, err := h.canEdit(r, course)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if !editable {
		if !course.Published || course.Archived() {
			httperror.Render(w, r, httperror.NotFound("course"))
			return
		}
		role, err := h.svc.EnrolmentRole(r.Context(), course.ID, u.ID)
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		if role == "" {
			httperror.Render(w, r, httperror.New(httperror.CodeForbidden, "you are not enrolled in this course"))
			return
		}
		if err := h.svc.RequireCourseUnlocked(r.Context(), course.ID, u.ID); err != nil {
			httperror.Render(w, r, err)
			return
		}
	}
	detail, err := h.svc.Detail(r.Context(), course, u.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.svc.AttachRules(r.Context(), &detail, u.ID, editable); err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.svc.AttachQualification(r.Context(), &detail, u.ID); err != nil {
		httperror.Render(w, r, err)
		return
	}
	langs, err := h.contentLangs(r, course)
	if err == nil {
		err = h.svc.TranslateDetail(r.Context(), &detail, langs)
	}
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *courseHandler) update(w http.ResponseWriter, r *http.Request) {
	course, _, err := h.loadCourse(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.requireEdit(r, course); err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Title     *string `json:"title"`
		Summary   *string `json:"summary"`
		SelfEnrol *bool   `json:"self_enrol"`
		// DueDays: learners are due N days after enrolling; null clears.
		DueDays nullableInt `json:"due_days"`
		// Languages offered and the language of the base text.
		Languages   *[]string `json:"languages"`
		DefaultLang *string   `json:"default_lang"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.svc.SetCourseLanguages(r.Context(), course.ID, in.Languages, in.DefaultLang); err != nil {
		httperror.Render(w, r, err)
		return
	}
	if in.DueDays.Set {
		if err := h.svc.SetCourseDueDays(r.Context(), course.ID, in.DueDays.Value); err != nil {
			httperror.Render(w, r, err)
			return
		}
	}
	updated, err := h.svc.UpdateCourse(r.Context(), course.ID, learn.UpdateCourseInput{
		Title: in.Title, Summary: in.Summary, SelfEnrol: in.SelfEnrol,
	})
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *courseHandler) setPublished(published bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		course, _, err := h.loadCourse(r)
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		if err := h.requireEdit(r, course); err != nil {
			httperror.Render(w, r, err)
			return
		}
		updated, err := h.svc.SetPublished(r.Context(), course.ID, published)
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		action := audit.CourseUnpublished
		if published {
			action = audit.CoursePublished
		}
		if course.Published != published {
			h.record(r, courseEntry(action, course, "course", course.ID.String(), nil))
		}
		writeJSON(w, http.StatusOK, updated)
	}
}

// ----------------------------------------------------------------------------
// Sections & pages
// ----------------------------------------------------------------------------

func (h *courseHandler) createSection(w http.ResponseWriter, r *http.Request) {
	course, _, err := h.loadCourse(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.requireEdit(r, course); err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Title string `json:"title"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	section, err := h.svc.CreateSection(r.Context(), course.ID, in.Title)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, section)
}

func (h *courseHandler) updateSection(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "section")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	course, err := h.svc.SectionCourse(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.requireEdit(r, course); err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Title    *string `json:"title"`
		Position *int    `json:"position"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	section, err := h.svc.UpdateSection(r.Context(), id, learn.UpdateSectionInput{Title: in.Title, Position: in.Position})
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, section)
}

func (h *courseHandler) deleteSection(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "section")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	course, err := h.svc.SectionCourse(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.requireEdit(r, course); err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.svc.DeleteSection(r.Context(), id); err != nil {
		httperror.Render(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *courseHandler) createActivity(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "section")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	course, err := h.svc.SectionCourse(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.requireEdit(r, course); err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Kind           string `json:"kind"`
		Title          string `json:"title"`
		Body           string `json:"body"`
		PassPercent    *int   `json:"pass_percent"`
		MaxAttempts    *int   `json:"max_attempts"`
		ShuffleOptions *bool  `json:"shuffle_options"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	activity, err := h.svc.CreateActivity(r.Context(), id, learn.CreateActivityInput{
		Kind: in.Kind, Title: in.Title, Body: in.Body,
		PassPercent: in.PassPercent, MaxAttempts: in.MaxAttempts, ShuffleOptions: in.ShuffleOptions,
	})
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, activity)
}

func (h *courseHandler) updateActivity(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "activity")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	course, err := h.svc.ActivityCourse(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.requireEdit(r, course); err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Title          *string     `json:"title"`
		Body           *string     `json:"body"`
		Position       *int        `json:"position"`
		PassPercent    *int        `json:"pass_percent"`
		MaxAttempts    nullableInt `json:"max_attempts"`
		ShuffleOptions *bool       `json:"shuffle_options"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	activity, err := h.svc.UpdateActivity(r.Context(), id, learn.UpdateActivityInput{
		Title: in.Title, Body: in.Body, Position: in.Position,
		PassPercent: in.PassPercent, MaxAttempts: in.MaxAttempts.Value,
		ClearMaxAttempts: in.MaxAttempts.Set && in.MaxAttempts.Value == nil,
		ShuffleOptions:   in.ShuffleOptions,
	})
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, activity)
}

func (h *courseHandler) deleteActivity(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "activity")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	course, err := h.svc.ActivityCourse(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.requireEdit(r, course); err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.svc.DeleteActivity(r.Context(), id); err != nil {
		httperror.Render(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----------------------------------------------------------------------------
// Completion
// ----------------------------------------------------------------------------

// setCompletion marks or unmarks a page complete for the caller. Requires
// an enrolment on a published course (staff may track their own progress
// too, via their trainer enrolment).
func (h *courseHandler) setCompletion(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "activity")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	course, err := h.svc.ActivityCourse(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	role, err := h.svc.EnrolmentRole(r.Context(), course.ID, u.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if role == "" || (!course.Published && role != domain.EnrolmentTrainer) {
		if !course.Published {
			httperror.Render(w, r, httperror.NotFound("activity"))
			return
		}
		httperror.Render(w, r, httperror.New(httperror.CodeForbidden, "you are not enrolled in this course"))
		return
	}
	var in struct {
		Completed bool `json:"completed"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	if in.Completed {
		if err := h.requireUnlocked(r, course, id, u.ID); err != nil {
			httperror.Render(w, r, err)
			return
		}
	}
	if err := h.svc.SetCompleted(r.Context(), id, u.ID, in.Completed); err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"activity_id": id, "completed": in.Completed})
}

// ----------------------------------------------------------------------------
// Enrolments
// ----------------------------------------------------------------------------

func (h *courseHandler) listEnrolments(w http.ResponseWriter, r *http.Request) {
	course, u, err := h.loadCourse(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if !mayManageStaff(r, course) {
		if err := h.requireEnrolManage(r, course); err != nil {
			httperror.Render(w, r, err)
			return
		}
	}
	out, err := h.svc.ListEnrolments(r.Context(), course.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if !maySeeEmails(u, course.OrganizationID) {
		for i := range out {
			out[i].Email = ""
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"enrolments": out})
}

// enrol adds a learner (enrol.manage plus edit rights on the course) or a
// trainer (staff.manage in the course's organization; trainers cannot make
// other people trainers). directory_id instead of user_id enrols someone
// from the identity provider's directory who has never signed in.
func (h *courseHandler) enrol(w http.ResponseWriter, r *http.Request) {
	course, _, err := h.loadCourse(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		UserID      uuid.UUID `json:"user_id"`
		DirectoryID string    `json:"directory_id"`
		Role        string    `json:"role"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	if in.Role == "" {
		in.Role = domain.EnrolmentLearner
	}
	if in.Role == domain.EnrolmentTrainer {
		if !mayManageStaff(r, course) {
			if err := h.requireEdit(r, course); err != nil {
				httperror.Render(w, r, err)
				return
			}
			httperror.Render(w, r, httperror.New(httperror.CodeForbidden,
				"only a training manager (staff.manage) can make someone a trainer"))
			return
		}
		if course.Archived() {
			httperror.Render(w, r, errArchived)
			return
		}
	} else if err := h.requireEnrolManage(r, course); err != nil {
		httperror.Render(w, r, err)
		return
	}
	userID, err := h.resolvePerson(r, course, in.UserID, in.DirectoryID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	enrolment, err := h.svc.Enrol(r.Context(), course.ID, userID, in.Role)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	h.record(r, courseEntry(audit.EnrolmentAdded, course, "enrolment", enrolment.ID.String(),
		map[string]any{"user_id": userID, "role": enrolment.Role}))
	if u, _ := auth.FromContext(r.Context()); !maySeeEmails(u, course.OrganizationID) {
		enrolment.Email = ""
	}
	writeJSON(w, http.StatusCreated, enrolment)
}

func (h *courseHandler) selfEnrol(w http.ResponseWriter, r *http.Request) {
	course, u, err := h.loadCourse(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	enrolment, err := h.svc.SelfEnrol(r.Context(), course.ID, u.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	h.record(r, courseEntry(audit.EnrolmentSelf, course, "enrolment", enrolment.ID.String(),
		map[string]any{"user_id": u.ID, "role": enrolment.Role}))
	writeJSON(w, http.StatusCreated, enrolment)
}

// unenrol removes a learner (enrol.manage plus edit rights) or a trainer
// (staff.manage only). The last trainer of a course is never removed (409).
func (h *courseHandler) unenrol(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "enrolment")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	course, err := h.svc.EnrolmentCourse(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	e, err := h.svc.GetEnrolment(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if e.Role == domain.EnrolmentTrainer {
		if !mayManageStaff(r, course) {
			if err := h.requireEdit(r, course); err != nil {
				httperror.Render(w, r, err)
				return
			}
			httperror.Render(w, r, httperror.New(httperror.CodeForbidden,
				"only a training manager (staff.manage) can remove a trainer"))
			return
		}
		if course.Archived() {
			httperror.Render(w, r, errArchived)
			return
		}
	} else if err := h.requireEnrolManage(r, course); err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.svc.Unenrol(r.Context(), id); err != nil {
		httperror.Render(w, r, err)
		return
	}
	h.record(r, courseEntry(audit.EnrolmentRemoved, course, "enrolment", id.String(),
		map[string]any{"user_id": e.UserID, "role": e.Role}))
	w.WriteHeader(http.StatusNoContent)
}

// ----------------------------------------------------------------------------
// Course roles (training managers)
// ----------------------------------------------------------------------------

// listCourseStaff serves GET /course-staff?q=: every course of the
// organizations where the caller holds staff.manage, with its trainers.
func (h *courseHandler) listCourseStaff(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	orgs := orgsWith(u, domain.PermStaffManage)
	if len(orgs) == 0 {
		httperror.Render(w, r, httperror.New(httperror.CodeForbidden, "missing permission: "+domain.PermStaffManage))
		return
	}
	out, err := h.svc.ListCourseStaff(r.Context(), orgs, strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"courses": out})
}

// orgsWith lists the organizations where u holds perm.
func orgsWith(u *auth.User, perm string) []uuid.UUID {
	out := []uuid.UUID{}
	for orgID := range u.OrgPermissions {
		if u.HasInOrg(perm, orgID) {
			out = append(out, orgID)
		}
	}
	return out
}

// ----------------------------------------------------------------------------
// People picker
// ----------------------------------------------------------------------------

// searchPeople serves the enrolment and course-role pickers: people of the
// organizations where the caller holds enrol.manage or staff.manage.
// Learners can never list other people; trainers see names only.
func (h *courseHandler) searchPeople(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	orgs := orgsWith(u, domain.PermEnrolManage)
	for _, id := range orgsWith(u, domain.PermStaffManage) {
		if !slices.Contains(orgs, id) {
			orgs = append(orgs, id)
		}
	}
	if len(orgs) == 0 {
		httperror.Render(w, r, httperror.New(httperror.CodeForbidden, "missing permission: "+domain.PermEnrolManage))
		return
	}
	withEmail := true
	for _, id := range orgs {
		withEmail = withEmail && maySeeEmails(u, id)
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	people, err := h.svc.SearchPeople(r.Context(), learn.PeopleQuery{Q: q, Organizations: orgs, WithEmail: withEmail})
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	people, source := h.withDirectoryPeople(r, people, q, withEmail)
	writeJSON(w, http.StatusOK, map[string]any{"people": people, "source": source})
}

// loadCourse parses {course} and loads the row plus the caller.
func (h *courseHandler) loadCourse(r *http.Request) (learn.Course, *auth.User, error) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		return learn.Course{}, nil, httperror.ErrUnauthorized
	}
	id, err := parseUUIDParam(r, "course")
	if err != nil {
		return learn.Course{}, nil, err
	}
	course, err := h.svc.GetCourse(r.Context(), id)
	if err != nil {
		return learn.Course{}, nil, err
	}
	return course, u, nil
}
