package http

import (
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/importer"
	"github.com/AGlElectronics/learn/backend/internal/learn"
)

// maxImportBytes caps an uploaded question file (Moodle XML can carry
// base64 images).
const maxImportBytes = 32 << 20

// importHandler owns question import into a course's bank (staff).
type importHandler struct {
	*quizHandler
	store learn.ObjectPutter
}

// mountImportRoutes registers:
//
//	POST /courses/{course}/question-import?step=preview|commit — multipart
//	     file, format (optional: detected), category_id (commit)
//	GET  /question-import/csv-template — the CSV template (text/csv)
//
// The preview saves nothing; the client sends the same file again to
// commit. store may be nil: embedded images are then skipped with a warning.
func mountImportRoutes(r chi.Router, courses *courseHandler, store learn.ObjectStore) {
	putter, _ := store.(learn.ObjectPutter)
	h := &importHandler{quizHandler: &quizHandler{courses}, store: putter}
	r.Post("/courses/{course}/question-import", h.importQuestions)
	r.Get("/question-import/csv-template", h.csvTemplate)
}

func (h *importHandler) importQuestions(w http.ResponseWriter, r *http.Request) {
	courseID, _, err := h.staffOn(r, "course", h.svc.GetCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	step := r.URL.Query().Get("step")
	if step == "" {
		step = "preview"
	}
	if step != "preview" && step != "commit" {
		httperror.Render(w, r, httperror.BadRequest("step must be preview or commit"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes+1<<20)
	if err := r.ParseMultipartForm(maxImportBytes); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			httperror.Render(w, r, httperror.Validation("the file is too large (at most 32 MB)", nil))
			return
		}
		httperror.Render(w, r, httperror.BadRequest("send the file as multipart/form-data in the field \"file\""))
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	file, header, err := r.FormFile("file")
	if err != nil {
		httperror.Render(w, r, httperror.Validation("choose a file to import", nil))
		return
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxImportBytes+1))
	if err != nil {
		httperror.Render(w, r, httperror.BadRequest("the file could not be read"))
		return
	}
	if len(data) > maxImportBytes {
		httperror.Render(w, r, httperror.Validation("the file is too large (at most 32 MB)", nil))
		return
	}
	if len(data) == 0 {
		httperror.Render(w, r, httperror.Validation("the file is empty", nil))
		return
	}
	format, err := learn.ImportFormat(r.FormValue("format"), header.Filename, data)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}

	if step == "preview" {
		out, err := learn.PreviewImport(format, data)
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	categoryID, err := uuid.Parse(r.FormValue("category_id"))
	if err != nil {
		httperror.Render(w, r, httperror.Validation("choose the category to import into", nil))
		return
	}
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	res, err := h.svc.CommitImport(r.Context(), h.store, courseID, categoryID, u.ID, format, data)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *importHandler) csvTemplate(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="question-import-template.csv"`)
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, importer.CSVTemplate())
}
