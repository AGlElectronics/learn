package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/learn"
)

// mountRuleRoutes registers activity prerequisites and per-learner due
// dates on the authenticated /api/v1 router. The course-level due_days is
// part of PATCH /courses/{course}.
func mountRuleRoutes(r chi.Router, courses *courseHandler) {
	r.Put("/activities/{activity}/prerequisites", courses.setPrerequisites)
	r.Patch("/enrolments/{enrolment}", courses.updateEnrolment)
}

// requireUnlocked answers 403 `locked` when the activity's prerequisites
// are incomplete for the caller. Course staff are never locked.
func (h *courseHandler) requireUnlocked(r *http.Request, course learn.Course, activityID, userID uuid.UUID) error {
	staff, err := h.canEdit(r, course)
	if err != nil || staff {
		return err
	}
	return h.svc.RequireUnlocked(r.Context(), activityID, userID)
}

// nullableTime tells an explicit JSON null ("clear it") from an absent field.
type nullableTime struct {
	Set   bool
	Value *time.Time
}

func (n *nullableTime) UnmarshalJSON(b []byte) error {
	n.Set = true
	if string(b) == "null" {
		n.Value = nil
		return nil
	}
	var v time.Time
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	n.Value = &v
	return nil
}

// setPrerequisites replaces what must be complete before the activity
// opens: {requires: [activity ids]} (same course, no cycles).
func (h *courseHandler) setPrerequisites(w http.ResponseWriter, r *http.Request) {
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
		Requires []uuid.UUID `json:"requires"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	out, err := h.svc.SetPrerequisites(r.Context(), id, in.Requires)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"activity_id": id, "prerequisites": out})
}

// updateEnrolment lets staff override one learner's due date:
// {due_at: RFC 3339 timestamp | null}.
func (h *courseHandler) updateEnrolment(w http.ResponseWriter, r *http.Request) {
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
	if err := h.requireEnrolManage(r, course); err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		DueAt nullableTime `json:"due_at"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	if !in.DueAt.Set {
		httperror.Render(w, r, httperror.Validation("nothing to change (give due_at)", nil))
		return
	}
	e, err := h.svc.SetEnrolmentDue(r.Context(), id, in.DueAt.Value)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}
