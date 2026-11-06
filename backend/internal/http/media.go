package http

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/learn"
)

// mediaHandler owns the course media library, viewing links and page
// attachments. Uploading and managing files is for course staff; viewing
// is for staff, or enrolled learners of a published course when the file
// is used by content they can see (not a locked page).
type mediaHandler struct {
	*quizHandler
	media *learn.MediaService
}

// mountMediaRoutes registers the media routes on the authenticated /api/v1
// router. store may be nil: uploads and viewing links then answer 503.
func mountMediaRoutes(r chi.Router, courses *courseHandler, store learn.ObjectStore) {
	h := &mediaHandler{quizHandler: &quizHandler{courses}, media: learn.NewMediaService(courses.svc, store)}

	r.Get("/courses/{course}/media", h.list)
	r.Post("/courses/{course}/media/uploads", h.startUpload)
	r.Post("/media/{media}/complete", h.complete)
	r.Patch("/media/{media}", h.update)
	r.Delete("/media/{media}", h.remove)
	r.Get("/media/{media}", h.redirect)
	r.Get("/media/{media}/url", h.viewURL)

	r.Get("/activities/{activity}/media", h.listAttachments)
	r.Post("/activities/{activity}/media", h.addAttachment)
	r.Patch("/activity-media/{attachment}", h.updateAttachment)
	r.Delete("/activity-media/{attachment}", h.deleteAttachment)
}

// nullableUUID tells an explicit JSON null ("clear it") from an absent field.
type nullableUUID struct {
	Set   bool
	Value *uuid.UUID
}

func (n *nullableUUID) UnmarshalJSON(b []byte) error {
	n.Set = true
	if string(b) == "null" {
		n.Value = nil
		return nil
	}
	var v uuid.UUID
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	n.Value = &v
	return nil
}

// regionInput is one hotspot region in a request body.
type regionInput struct {
	Shape   string             `json:"shape"`
	Coords  map[string]float64 `json:"coords"`
	Correct bool               `json:"correct"`
}

func regionInputs(in []regionInput) []learn.RegionInput {
	out := make([]learn.RegionInput, len(in))
	for i, r := range in {
		out[i] = learn.RegionInput{Shape: r.Shape, Coords: r.Coords, Correct: r.Correct}
	}
	return out
}

// replaceRegions swaps a hotspot question's regions (409 once locked).
func (h *quizHandler) replaceRegions(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "question", h.svc.QuestionCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Regions []regionInput `json:"regions"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	q, err := h.svc.ReplaceRegions(r.Context(), id, regionInputs(in.Regions))
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, q)
}

// ----------------------------------------------------------------------------
// Library (staff)
// ----------------------------------------------------------------------------

// list serves the media library; ?filter=unused keeps files nothing uses
// (unfinished uploads included), for staff to clean up by hand.
func (h *mediaHandler) list(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "course", h.svc.GetCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	out, err := h.media.ListMedia(r.Context(), id, r.URL.Query().Get("filter") == "unused")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"media": out})
}

func (h *mediaHandler) startUpload(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "course", h.svc.GetCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.ErrUnauthorized)
		return
	}
	var in struct {
		Filename    string `json:"filename"`
		ContentType string `json:"content_type"`
		SizeBytes   int64  `json:"size_bytes"`
		AltText     string `json:"alt_text"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	ticket, err := h.media.StartUpload(r.Context(), id, u.ID, learn.UploadInput{
		Name: in.Filename, ContentType: in.ContentType, SizeBytes: in.SizeBytes, AltText: in.AltText,
	})
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, ticket)
}

func (h *mediaHandler) complete(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "media", h.svc.MediaCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	m, err := h.media.CompleteUpload(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (h *mediaHandler) update(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "media", h.svc.MediaCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		AltText *string `json:"alt_text"`
		Name    *string `json:"name"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	m, err := h.media.UpdateMedia(r.Context(), id, in.AltText, in.Name)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// remove deletes an unused file and its stored object (409 while in use).
func (h *mediaHandler) remove(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "media", h.svc.MediaCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.media.DeleteMedia(r.Context(), id); err != nil {
		httperror.Render(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----------------------------------------------------------------------------
// Viewing
// ----------------------------------------------------------------------------

// requireView lets staff see any file of their course; learners need an
// enrolment on the published course and a file used by content they can
// reach. Everyone else gets 404, never confirming the file exists.
func (h *mediaHandler) requireView(r *http.Request, mediaID uuid.UUID) error {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		return httperror.ErrUnauthorized
	}
	course, err := h.svc.MediaCourse(r.Context(), mediaID)
	if err != nil {
		return err
	}
	staff, err := h.canEdit(r, course)
	if err != nil || staff {
		return err
	}
	if !course.Published {
		return httperror.NotFound("media")
	}
	role, err := h.svc.EnrolmentRole(r.Context(), course.ID, u.ID)
	if err != nil {
		return err
	}
	if role == "" {
		return httperror.NotFound("media")
	}
	return h.svc.LearnerMayView(r.Context(), mediaID, u.ID)
}

// viewURL answers {url, expires_at}: a presigned link valid for 5 minutes.
// The SPA uses this (fetch carries the bearer token; <img src> can't).
func (h *mediaHandler) viewURL(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "media")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.requireView(r, id); err != nil {
		httperror.Render(w, r, err)
		return
	}
	u, exp, err := h.media.ViewURL(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{"url": u, "expires_at": exp})
}

// redirect sends the caller to a 5-minute presigned link.
func (h *mediaHandler) redirect(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "media")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.requireView(r, id); err != nil {
		httperror.Render(w, r, err)
		return
	}
	u, _, err := h.media.ViewURL(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	http.Redirect(w, r, u, http.StatusFound)
}

// ----------------------------------------------------------------------------
// Page attachments
// ----------------------------------------------------------------------------

// listAttachments serves a page's attachments to staff and to enrolled
// learners for whom the page is not locked.
func (h *mediaHandler) listAttachments(w http.ResponseWriter, r *http.Request) {
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
	if err := h.requireUnlocked(r, course, id, u.ID); err != nil {
		httperror.Render(w, r, err)
		return
	}
	out, err := h.svc.ListActivityMedia(r.Context(), id)
	if err == nil {
		var langs learn.Langs
		if langs, err = h.contentLangs(r, course); err == nil {
			err = h.svc.TranslateActivityMedia(r.Context(), course.ID, out, langs)
		}
	}
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"attachments": out})
}

func (h *mediaHandler) addAttachment(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "activity", h.svc.ActivityCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		MediaID     *uuid.UUID `json:"media_id"`
		ExternalURL *string    `json:"external_url"`
		Caption     string     `json:"caption"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	am, err := h.svc.AddActivityMedia(r.Context(), id, learn.AddActivityMediaInput{
		MediaID: in.MediaID, ExternalURL: in.ExternalURL, Caption: in.Caption,
	})
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, am)
}

func (h *mediaHandler) updateAttachment(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "attachment", h.svc.ActivityMediaCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Caption  *string `json:"caption"`
		Position *int    `json:"position"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	am, err := h.svc.UpdateActivityMedia(r.Context(), id, in.Caption, in.Position)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, am)
}

func (h *mediaHandler) deleteAttachment(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "attachment", h.svc.ActivityMediaCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.svc.DeleteActivityMedia(r.Context(), id); err != nil {
		httperror.Render(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
