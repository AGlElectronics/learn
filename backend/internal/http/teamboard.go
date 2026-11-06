package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/learn"
)

// mountTeamBoardRoutes registers the team progress boards. Boards only
// carry per-group counts, never names.
//
//	GET     /courses/{course}/team-board                       — enrolled people (when enabled) and staff
//	GET|PUT /courses/{course}/team-board/settings              — course staff: {enabled?, groups?}
//	GET     /qualifications/{qualification}/team-board          — holders, enrolled in awarding courses, staff
//	GET|PUT /qualifications/{qualification}/team-board/settings — settings.manage
//	GET     /me/teams                                           — the caller's teams on enabled boards
func mountTeamBoardRoutes(r chi.Router, courses *courseHandler) {
	h := &qualificationHandler{courseHandler: courses}
	r.Get("/courses/{course}/team-board", h.courseTeamBoard)
	r.Get("/courses/{course}/team-board/settings", h.courseTeamBoardSettings)
	r.Put("/courses/{course}/team-board/settings", h.setCourseTeamBoardSettings)
	r.Get("/qualifications/{qualification}/team-board", h.qualificationTeamBoard)
	r.Get("/qualifications/{qualification}/team-board/settings", h.qualificationTeamBoardSettings)
	r.Put("/qualifications/{qualification}/team-board/settings", h.setQualificationTeamBoardSettings)
	r.Get("/me/teams", h.myTeams)
}

var errNoTeamBoard = httperror.NotFound("team board")

type teamBoardSettingsBody struct {
	Enabled *bool     `json:"enabled"`
	Groups  *[]string `json:"groups"`
}

func (h *qualificationHandler) courseTeamBoard(w http.ResponseWriter, r *http.Request) {
	course, u, err := h.loadCourse(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	staff, err := h.canEdit(r, course)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if !staff {
		if !course.Published {
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
	}
	board, err := h.svc.TeamBoardFor(r.Context(), learn.TeamBoardCourse, course.ID, course.Title)
	if err == nil {
		err = h.svc.TranslateTeamBoards(r.Context(), requestLangs(r, h.svc), &board)
	}
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if !board.Enabled && !staff {
		httperror.Render(w, r, errNoTeamBoard)
		return
	}
	writeJSON(w, http.StatusOK, board)
}

func (h *qualificationHandler) courseTeamBoardSettings(w http.ResponseWriter, r *http.Request) {
	course, _, err := h.loadCourse(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.requireEdit(r, course); err != nil {
		httperror.Render(w, r, err)
		return
	}
	out, err := h.svc.TeamBoardSettings(r.Context(), learn.TeamBoardCourse, course.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *qualificationHandler) setCourseTeamBoardSettings(w http.ResponseWriter, r *http.Request) {
	course, _, err := h.loadCourse(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.requireEdit(r, course); err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in teamBoardSettingsBody
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	out, err := h.svc.SetTeamBoardSettings(r.Context(), learn.TeamBoardCourse, course.ID, in.Enabled, in.Groups)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// qualificationStaff: settings or course managers of the qualification's org.
func qualificationStaff(u *auth.User, orgID uuid.UUID) bool {
	return u.HasInOrg(domain.PermSettingsManage, orgID) || u.HasInOrg(domain.PermCourseManage, orgID)
}

func (h *qualificationHandler) qualificationTeamBoard(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	id, err := parseUUIDParam(r, "qualification")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	q, err := h.svc.GetQualification(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	staff := qualificationStaff(u, q.OrganizationID)
	if !staff {
		ok, err := h.svc.CanSeeQualificationBoard(r.Context(), q.ID, u.ID)
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		if !ok {
			httperror.Render(w, r, errNoTeamBoard)
			return
		}
	}
	board, err := h.svc.TeamBoardFor(r.Context(), learn.TeamBoardQualification, q.ID, q.Name)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if !board.Enabled && !staff {
		httperror.Render(w, r, errNoTeamBoard)
		return
	}
	writeJSON(w, http.StatusOK, board)
}

func (h *qualificationHandler) qualificationTeamBoardSettings(w http.ResponseWriter, r *http.Request) {
	q, err := h.loadManagedQualification(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	out, err := h.svc.TeamBoardSettings(r.Context(), learn.TeamBoardQualification, q.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *qualificationHandler) setQualificationTeamBoardSettings(w http.ResponseWriter, r *http.Request) {
	q, err := h.loadManagedQualification(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in teamBoardSettingsBody
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	out, err := h.svc.SetTeamBoardSettings(r.Context(), learn.TeamBoardQualification, q.ID, in.Enabled, in.Groups)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *qualificationHandler) myTeams(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	out, err := h.svc.MyTeams(r.Context(), u.ID)
	if err == nil {
		boards := make([]*learn.TeamBoard, len(out))
		for i := range out {
			boards[i] = &out[i]
		}
		err = h.svc.TranslateTeamBoards(r.Context(), requestLangs(r, h.svc), boards...)
	}
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"teams": out})
}
