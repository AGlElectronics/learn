package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// mountStatsRoutes registers GET /courses/{course}/question-stats (staff).
func mountStatsRoutes(r chi.Router, courses *courseHandler) {
	h := &quizHandler{courses}
	r.Get("/courses/{course}/question-stats", h.questionStats)
}

func (h *quizHandler) questionStats(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "course", h.svc.GetCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	out, err := h.svc.QuestionStats(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"questions": out})
}
