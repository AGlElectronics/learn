package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/learn"
)

// mountFeedbackRoutes registers course and session feedback:
//
//	GET  /courses/{course}/feedback           — staff summary (+ settings)
//	PUT  /courses/{course}/feedback-settings  — staff: {enabled?, anonymous?}
//	GET  /courses/{course}/feedback/mine      — learner: what they may rate
//	POST /courses/{course}/feedback           — learner: {activity_id?, rating, comment}
func mountFeedbackRoutes(r chi.Router, courses *courseHandler, limits limitSet) {
	h := &quizHandler{courses}
	r.Get("/courses/{course}/feedback", h.feedbackSummary)
	r.Put("/courses/{course}/feedback-settings", h.setFeedbackSettings)
	r.Get("/courses/{course}/feedback/mine", h.myFeedback)
	r.With(limits.hook(LimitFeedback)).Post("/courses/{course}/feedback", h.submitFeedback)
}

func (h *quizHandler) feedbackSummary(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "course", h.svc.GetCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	out, err := h.svc.FeedbackSummary(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *quizHandler) setFeedbackSettings(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "course", h.svc.GetCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Enabled   *bool `json:"enabled"`
		Anonymous *bool `json:"anonymous"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	out, err := h.svc.SetFeedbackSettings(r.Context(), id, in.Enabled, in.Anonymous)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *quizHandler) myFeedback(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "course")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	course, err := h.svc.GetCourse(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	u, err := h.requireTake(r, course)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	out, err := h.svc.MyFeedback(r.Context(), id, u.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// submitFeedback answers 201, 403 (feedback off or not eligible yet) or
// 409 (already given).
func (h *quizHandler) submitFeedback(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "course")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	course, err := h.svc.GetCourse(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	u, err := h.requireTake(r, course)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		ActivityID *uuid.UUID `json:"activity_id"`
		Rating     int        `json:"rating"`
		Comment    string     `json:"comment"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.svc.SubmitFeedback(r.Context(), id, u.ID, learn.FeedbackInput{
		ActivityID: in.ActivityID, Rating: in.Rating, Comment: in.Comment,
	}); err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
}
