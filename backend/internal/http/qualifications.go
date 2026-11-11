package http

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/learn"
)

// qualificationHandler owns qualifications (qualification.manage), the course's
// awarded qualification (course staff), certificates (holder or staff;
// revoking needs qualification.manage) and renewal (the learner).
type qualificationHandler struct {
	*courseHandler
	publicBaseURL string
}

// mountQualificationRoutes registers the authenticated qualification and
// certificate routes on /api/v1.
func mountQualificationRoutes(r chi.Router, courses *courseHandler, publicBaseURL string) {
	h := &qualificationHandler{courseHandler: courses, publicBaseURL: strings.TrimRight(publicBaseURL, "/")}

	r.Get("/qualifications", h.list)
	r.Post("/qualifications", h.create)
	r.Patch("/qualifications/{qualification}", h.update)
	r.Delete("/qualifications/{qualification}", h.remove)
	r.Get("/qualifications/{qualification}/certificates", h.listCertificates)

	r.Put("/courses/{course}/qualification", h.setCourseQualification)
	r.Post("/courses/{course}/renew", h.renew)

	r.Get("/me/qualifications", h.mine)
	r.Get("/certificates/{certificate}", h.getCertificate)
	r.Post("/certificates/{certificate}/revoke", h.revoke)
}

func (h *qualificationHandler) verifyURL(code string) string {
	return h.publicBaseURL + "/verify/" + code
}

func (h *qualificationHandler) withURL(c learn.Certificate) learn.Certificate {
	c.VerifyURL = h.verifyURL(c.VerifyCode)
	return c
}

// list serves the qualification picker (teach) and the settings page.
func (h *qualificationHandler) list(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	if !u.HasAny(domain.PermQualificationManage) && !u.HasAny(domain.PermCourseManage) && !u.HasAny(domain.PermCourseTeach) {
		httperror.Render(w, r, httperror.ErrForbidden)
		return
	}
	orgs := []uuid.UUID{}
	for orgID := range u.OrgPermissions {
		if u.HasInOrg(domain.PermQualificationManage, orgID) || u.HasInOrg(domain.PermCourseManage, orgID) ||
			u.HasInOrg(domain.PermCourseTeach, orgID) {
			orgs = append(orgs, orgID)
		}
	}
	out, err := h.svc.ListQualifications(r.Context(), orgs)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"qualifications": out})
}

type qualificationBody struct {
	Key         *string     `json:"key"`
	Name        *string     `json:"name"`
	Description *string     `json:"description"`
	ValidMonths nullableInt `json:"valid_months"`
}

func (b qualificationBody) input() learn.QualificationInput {
	return learn.QualificationInput{
		Key: b.Key, Name: b.Name, Description: b.Description,
		ValidMonths:      b.ValidMonths.Value,
		ClearValidMonths: b.ValidMonths.Set && b.ValidMonths.Value == nil,
	}
}

func (h *qualificationHandler) create(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	var orgID uuid.UUID
	for id := range u.OrgPermissions {
		if u.HasInOrg(domain.PermQualificationManage, id) {
			orgID = id
			break
		}
	}
	if orgID == uuid.Nil {
		httperror.Render(w, r, httperror.New(httperror.CodeForbidden, "missing permission: "+domain.PermQualificationManage))
		return
	}
	var in qualificationBody
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	q, err := h.svc.CreateQualification(r.Context(), orgID, in.input())
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, q)
}

// loadManagedQualification loads {qualification} and requires
// qualification.manage in its organization.
func (h *qualificationHandler) loadManagedQualification(r *http.Request) (learn.Qualification, error) {
	id, err := parseUUIDParam(r, "qualification")
	if err != nil {
		return learn.Qualification{}, err
	}
	q, err := h.svc.GetQualification(r.Context(), id)
	if err != nil {
		return learn.Qualification{}, err
	}
	if err := auth.RequireInOrg(r.Context(), domain.PermQualificationManage, q.OrganizationID); err != nil {
		return learn.Qualification{}, err
	}
	return q, nil
}

func (h *qualificationHandler) update(w http.ResponseWriter, r *http.Request) {
	q, err := h.loadManagedQualification(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in qualificationBody
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	out, err := h.svc.UpdateQualification(r.Context(), q.ID, in.input())
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *qualificationHandler) remove(w http.ResponseWriter, r *http.Request) {
	q, err := h.loadManagedQualification(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.svc.DeleteQualification(r.Context(), q.ID); err != nil {
		httperror.Render(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *qualificationHandler) listCertificates(w http.ResponseWriter, r *http.Request) {
	q, err := h.loadManagedQualification(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	list, err := h.svc.QualificationCertificates(r.Context(), q.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	for i := range list {
		list[i] = h.withURL(list[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"qualification": q, "certificates": list})
}

// setCourseQualification: {qualification_id: uuid | null}. Course staff.
func (h *qualificationHandler) setCourseQualification(w http.ResponseWriter, r *http.Request) {
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
		QualificationID nullableUUID `json:"qualification_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	if !in.QualificationID.Set {
		httperror.Render(w, r, httperror.Validation("give qualification_id (or null for none)", nil))
		return
	}
	if err := h.svc.SetCourseQualification(r.Context(), course, in.QualificationID.Value); err != nil {
		httperror.Render(w, r, err)
		return
	}
	ref, err := h.svc.CourseQualification(r.Context(), course.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"course_id": course.ID, "awards_qualification": ref})
}

// renew starts a new cycle for the calling learner (see Service.Renew).
func (h *qualificationHandler) renew(w http.ResponseWriter, r *http.Request) {
	course, u, err := h.loadCourse(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if !course.Published {
		httperror.Render(w, r, httperror.NotFound("course"))
		return
	}
	res, err := h.svc.Renew(r.Context(), course.ID, u.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *qualificationHandler) mine(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	list, err := h.svc.MyQualifications(r.Context(), u.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	certs := make([]*learn.Certificate, len(list))
	for i := range list {
		list[i].Certificate = h.withURL(list[i].Certificate)
		certs[i] = &list[i].Certificate
	}
	if err := h.svc.TranslateCertificates(r.Context(), requestLangs(r, h.svc), certs...); err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"qualifications": list})
}

// canSeeCertificate: the holder, qualification/course managers of the org, or
// staff of the course that awarded it.
func (h *qualificationHandler) canSeeCertificate(r *http.Request, c learn.Certificate) (bool, error) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		return false, httperror.ErrUnauthorized
	}
	if c.UserID == u.ID || u.HasInOrg(domain.PermQualificationManage, c.OrganizationID) ||
		u.HasInOrg(domain.PermCourseManage, c.OrganizationID) {
		return true, nil
	}
	if c.CourseID == nil {
		return false, nil
	}
	course, err := h.svc.GetCourse(r.Context(), *c.CourseID)
	if err != nil {
		return false, err
	}
	return h.canEdit(r, course)
}

func (h *qualificationHandler) getCertificate(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "certificate")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	c, err := h.svc.GetCertificate(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	ok, err := h.canSeeCertificate(r, c)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if !ok {
		httperror.Render(w, r, httperror.NotFound("certificate"))
		return
	}
	if err := h.svc.TranslateCertificates(r.Context(), requestLangs(r, h.svc), &c); err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h.withURL(c))
}

// revoke: {reason}. qualification.manage in the qualification's organization.
func (h *qualificationHandler) revoke(w http.ResponseWriter, r *http.Request) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	id, err := parseUUIDParam(r, "certificate")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	c, err := h.svc.GetCertificate(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := auth.RequireInOrg(r.Context(), domain.PermQualificationManage, c.OrganizationID); err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	out, err := h.svc.RevokeCertificate(r.Context(), id, u.ID, in.Reason)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h.withURL(out))
}
