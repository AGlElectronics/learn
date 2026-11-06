package http

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/learn"
)

// sessionHandler owns in-person sessions: slot authoring, booking, QR
// check-in and the attendance roster. Authoring, the check-in code and the
// roster follow canEdit (course staff); booking and checking in need an
// enrolment on a published course, or staff.
type sessionHandler struct {
	*quizHandler
	publicBaseURL string
}

// mountSessionRoutes registers the session routes on the authenticated
// /api/v1 router. Session slots live under /slots/{slot}/… next to the
// quiz-slot routes (PATCH|DELETE /slots/{slot}), so slot editing uses
// /session-slots/{slot}.
func mountSessionRoutes(r chi.Router, courses *courseHandler, publicBaseURL string) {
	h := &sessionHandler{quizHandler: &quizHandler{courses}, publicBaseURL: publicBaseURL}

	r.Get("/activities/{activity}/session", h.sessionInfo)
	r.Post("/activities/{activity}/session-slots", h.createSlot)
	r.Patch("/session-slots/{slot}", h.updateSlot)
	r.Delete("/session-slots/{slot}", h.deleteSlot)

	r.Post("/slots/{slot}/booking", h.book)
	r.Delete("/slots/{slot}/booking", h.cancel)
	r.Get("/slots/{slot}/checkin-code", h.checkinCode)
	r.Post("/slots/{slot}/checkin", h.checkIn)
	r.Get("/slots/{slot}/roster", h.roster)
	r.Put("/signups/{signup}", h.setSignupStatus)
}

// requireSlotUnlocked answers 403 `locked` while the session's
// prerequisites are incomplete for the caller.
func (h *sessionHandler) requireSlotUnlocked(r *http.Request, course learn.Course, slotID, userID uuid.UUID) error {
	activityID, err := h.svc.SlotActivityID(r.Context(), slotID)
	if err != nil {
		return err
	}
	return h.requireUnlocked(r, course, activityID, userID)
}

func (h *sessionHandler) sessionInfo(w http.ResponseWriter, r *http.Request) {
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
	u, err := h.requireTake(r, course)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	info, err := h.svc.SessionInfo(r.Context(), id, u.ID)
	if err == nil {
		err = h.svc.TranslateSessionInfo(r.Context(), course.ID, &info, requestLangs(r, h.svc))
	}
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (h *sessionHandler) createSlot(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "activity", h.svc.ActivityCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		StartsAt time.Time `json:"starts_at"`
		EndsAt   time.Time `json:"ends_at"`
		Location string    `json:"location"`
		Capacity int       `json:"capacity"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	slot, err := h.svc.CreateSessionSlot(r.Context(), id, learn.SessionSlotInput{
		StartsAt: in.StartsAt, EndsAt: in.EndsAt, Location: in.Location, Capacity: in.Capacity,
	})
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, slot)
}

func (h *sessionHandler) updateSlot(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "slot", h.svc.SessionSlotCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		StartsAt *time.Time `json:"starts_at"`
		EndsAt   *time.Time `json:"ends_at"`
		Location *string    `json:"location"`
		Capacity *int       `json:"capacity"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	slot, err := h.svc.UpdateSessionSlot(r.Context(), id, learn.UpdateSessionSlotInput{
		StartsAt: in.StartsAt, EndsAt: in.EndsAt, Location: in.Location, Capacity: in.Capacity,
	})
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, slot)
}

func (h *sessionHandler) deleteSlot(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "slot", h.svc.SessionSlotCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.svc.DeleteSessionSlot(r.Context(), id); err != nil {
		httperror.Render(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// book answers 201 for a new booking or waitlist place, 200 when the
// caller already holds one on this slot.
func (h *sessionHandler) book(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "slot")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	course, err := h.svc.SessionSlotCourse(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	u, err := h.requireTake(r, course)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.requireSlotUnlocked(r, course, id, u.ID); err != nil {
		httperror.Render(w, r, err)
		return
	}
	signup, created, err := h.svc.BookSlot(r.Context(), id, u.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, signup)
}

func (h *sessionHandler) cancel(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "slot")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	course, err := h.svc.SessionSlotCourse(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	u, err := h.requireTake(r, course)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.svc.CancelBooking(r.Context(), id, u.ID); err != nil {
		httperror.Render(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *sessionHandler) checkinCode(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "slot", h.svc.SessionSlotCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	code, err := h.svc.CurrentCheckinCode(r.Context(), id, h.publicBaseURL)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, code)
}

func (h *sessionHandler) checkIn(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "slot")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	course, err := h.svc.SessionSlotCourse(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	u, err := h.requireTake(r, course)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.requireSlotUnlocked(r, course, id, u.ID); err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	res, err := h.svc.CheckIn(r.Context(), id, u.ID, in.Code)
	if err == nil {
		err = h.svc.TranslateCheckin(r.Context(), &res, requestLangs(r, h.svc))
	}
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *sessionHandler) roster(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "slot", h.svc.SessionSlotCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	roster, err := h.svc.Roster(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, roster)
}

func (h *sessionHandler) setSignupStatus(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "signup", h.svc.SignupCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	signup, err := h.svc.SetSignupStatus(r.Context(), id, in.Status)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, signup)
}
