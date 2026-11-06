package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/learn"
)

func mountCopyRoutes(r chi.Router, courses *courseHandler) {
	r.Post("/courses/{course}/copy", courses.copyCourse)
}

// copyCourse copies a course for a new edition: {title, include_group_links}.
// Allowed for whoever may edit the source (course.manage in its org, or
// one of its trainers); the copier becomes the copy's trainer.
func (h *courseHandler) copyCourse(w http.ResponseWriter, r *http.Request) {
	course, u, err := h.loadCourse(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.requireEdit(r, course); err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Title             string `json:"title"`
		IncludeGroupLinks bool   `json:"include_group_links"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	c, err := h.svc.CopyCourse(r.Context(), course.ID, u.ID, learn.CopyCourseInput{
		Title: in.Title, IncludeGroupLinks: in.IncludeGroupLinks,
	})
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}
