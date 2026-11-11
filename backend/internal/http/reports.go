package http

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/learn"
)

// reportHandler serves the completion and qualification reports.
// report.view sees every course and qualification of its organization;
// course staff (course.teach) see the completion report of their own
// courses only; course.manage sees completion for every course of the org.
type reportHandler struct {
	*courseHandler
}

func mountReportRoutes(r chi.Router, courses *courseHandler) {
	h := &reportHandler{courseHandler: courses}
	r.Get("/reports/scope", h.scope)
	r.Get("/reports/completion", h.completion)
	r.Get("/reports/qualifications", h.qualifications)
}

func reportScopeFor(u *auth.User) learn.ReportScope {
	sc := learn.ReportScope{UserID: u.ID, AllOrgs: []uuid.UUID{}, ManageOrgs: []uuid.UUID{}, StaffOrgs: []uuid.UUID{}}
	for orgID := range u.OrgPermissions {
		if u.HasInOrg(domain.PermReportView, orgID) {
			sc.AllOrgs = append(sc.AllOrgs, orgID)
		}
		if u.HasInOrg(domain.PermCourseManage, orgID) {
			sc.ManageOrgs = append(sc.ManageOrgs, orgID)
		}
		if u.HasInOrg(domain.PermCourseTeach, orgID) {
			sc.StaffOrgs = append(sc.StaffOrgs, orgID)
		}
	}
	return sc
}

// scope tells the reports page what the caller may see: whether they have
// the full view (and the qualification report), and which courses.
func (h *reportHandler) scope(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	sc := reportScopeFor(u)
	courses, err := h.svc.ReportCourses(r.Context(), sc)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var quals []learn.Qualification
	if len(sc.AllOrgs) > 0 {
		if quals, err = h.svc.ListQualifications(r.Context(), sc.AllOrgs); err != nil {
			httperror.Render(w, r, err)
			return
		}
	}
	if quals == nil {
		quals = []learn.Qualification{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"all":            len(sc.AllOrgs) > 0,
		"qualifications": quals,
		"courses":        courses,
	})
}

func optionalUUIDQuery(r *http.Request, name string) (*uuid.UUID, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, httperror.BadRequest("invalid " + name + " id: " + raw)
	}
	return &id, nil
}

func (h *reportHandler) completion(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	sc := reportScopeFor(u)
	if len(sc.AllOrgs) == 0 && len(sc.ManageOrgs) == 0 && len(sc.StaffOrgs) == 0 {
		httperror.Render(w, r, httperror.New(httperror.CodeForbidden, "missing permission: "+domain.PermReportView))
		return
	}
	courseID, err := optionalUUIDQuery(r, "course")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if courseID != nil {
		courses, err := h.svc.ReportCourses(r.Context(), sc)
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		allowed := false
		for _, c := range courses {
			allowed = allowed || c.ID == *courseID
		}
		if !allowed {
			httperror.Render(w, r, httperror.New(httperror.CodeForbidden, "you can only report on courses you teach"))
			return
		}
	}
	q := r.URL.Query()
	rows, err := h.svc.CompletionReport(r.Context(), sc, learn.CompletionFilter{
		CourseID: courseID, Group: learn.NormalizeGroup(q.Get("group")), Status: strings.TrimSpace(q.Get("status")),
	})
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if q.Get("format") == "csv" {
		out := [][]string{{"course", "person", "email", "source", "enrolled_at", "due_at", "completed", "total", "last_activity_at", "status"}}
		for _, row := range rows {
			out = append(out, []string{
				csvText(row.CourseTitle), csvText(row.DisplayName), csvText(row.Email), row.Source,
				csvTime(&row.EnrolledAt), csvTime(row.DueAt), strconv.Itoa(row.Completed), strconv.Itoa(row.Total),
				csvTime(row.LastActivityAt), row.Status,
			})
		}
		writeCSV(w, "completion-report", out)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
}

func (h *reportHandler) qualifications(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	sc := reportScopeFor(u)
	if len(sc.AllOrgs) == 0 {
		httperror.Render(w, r, httperror.New(httperror.CodeForbidden, "missing permission: "+domain.PermReportView))
		return
	}
	qualID, err := optionalUUIDQuery(r, "qualification")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	q := r.URL.Query()
	rows, err := h.svc.QualificationReport(r.Context(), sc.AllOrgs, learn.QualificationFilter{
		QualificationID: qualID, Group: learn.NormalizeGroup(q.Get("group")), Status: strings.TrimSpace(q.Get("status")),
	})
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if q.Get("format") == "csv" {
		out := [][]string{{"qualification_key", "qualification", "person", "email", "required", "issued_at", "expires_at", "status"}}
		for _, row := range rows {
			out = append(out, []string{
				csvText(row.QualificationKey), csvText(row.QualificationName), csvText(row.DisplayName), csvText(row.Email),
				strconv.FormatBool(row.Required), csvTime(row.IssuedAt), csvTime(row.ExpiresAt), row.Status,
			})
		}
		writeCSV(w, "qualification-report", out)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
}

// csvText neutralises spreadsheet formulas in free text (CSV injection):
// a cell starting with = + - @ (or a tab/CR) is prefixed with a quote.
func csvText(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func csvTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// writeCSV sends rows as a CSV attachment; encoding/csv quotes commas,
// quotes and newlines.
func writeCSV(w http.ResponseWriter, name string, rows [][]string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+"-"+time.Now().UTC().Format("2006-01-02")+`.csv"`)
	w.WriteHeader(http.StatusOK)
	cw := csv.NewWriter(w)
	_ = cw.WriteAll(rows)
}
