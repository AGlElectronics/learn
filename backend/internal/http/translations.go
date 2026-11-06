package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/hlog"

	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/denlink"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/learn"
)

// requestLangs is the caller's languages, best first: ?lang=, then their
// saved preference (/me/preferences), then Accept-Language. Each course
// picks the first one it offers, else its default language.
func requestLangs(r *http.Request, svc *learn.Service) learn.Langs {
	pref := ""
	if u, ok := auth.FromContext(r.Context()); ok && u.ID != uuid.Nil {
		if l, err := svc.UserLanguage(r.Context(), u.ID); err == nil {
			pref = l
		}
	}
	return learn.NewLangs(r.URL.Query().Get("lang"), pref, r.Header.Get("Accept-Language"))
}

// contentLangs is requestLangs for course content that staff also edit:
// staff without an explicit ?lang= get nil, i.e. the default text they
// are editing, so a translation is never saved over it.
func (h *courseHandler) contentLangs(r *http.Request, course learn.Course) (learn.Langs, error) {
	if r.URL.Query().Get("lang") == "" {
		editable, err := h.canEdit(r, course)
		if err != nil {
			return nil, err
		}
		if editable {
			return nil, nil
		}
	}
	return requestLangs(r, h.svc), nil
}

// mountTranslationRoutes registers the staff translation editor:
//
//	GET|PUT /courses/{course}/translations?lang=               — course staff
//	GET|PUT /qualifications/{qualification}/translations?lang= — settings.manage
//	GET|PUT /paths/{path}/translations?lang=                   — course.manage
//
// GET returns every translatable field with its default text, the
// translation and a missing flag; PUT {translations: [{entity_type,
// entity_id, field, text}]} upserts in bulk (empty text removes one).
func mountTranslationRoutes(r chi.Router, courses *courseHandler) {
	q := &qualificationHandler{courseHandler: courses}
	p := &pathHandler{svc: courses.svc}

	courseScope := func(r *http.Request) (uuid.UUID, error) {
		course, _, err := courses.loadCourse(r)
		if err != nil {
			return uuid.Nil, err
		}
		return course.ID, courses.requireEdit(r, course)
	}
	qualificationScope := func(r *http.Request) (uuid.UUID, error) {
		ql, err := q.loadManagedQualification(r)
		return ql.ID, err
	}
	pathScope := func(r *http.Request) (uuid.UUID, error) {
		path, _, err := p.loadManaged(r)
		return path.ID, err
	}

	r.Get("/courses/{course}/translations", translationSheetHandler(courses.svc, learn.TrScopeCourse, courseScope))
	r.Put("/courses/{course}/translations", putTranslationsHandler(courses.svc, learn.TrScopeCourse, courseScope))
	r.Get("/qualifications/{qualification}/translations", translationSheetHandler(courses.svc, learn.TrScopeQualification, qualificationScope))
	r.Put("/qualifications/{qualification}/translations", putTranslationsHandler(courses.svc, learn.TrScopeQualification, qualificationScope))
	r.Get("/paths/{path}/translations", translationSheetHandler(courses.svc, learn.TrScopePath, pathScope))
	r.Put("/paths/{path}/translations", putTranslationsHandler(courses.svc, learn.TrScopePath, pathScope))
}

func translationSheetHandler(svc *learn.Service, scope string, load func(*http.Request) (uuid.UUID, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := load(r)
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		sheet, err := svc.TranslationSheet(r.Context(), scope, id, r.URL.Query().Get("lang"))
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, sheet)
	}
}

func putTranslationsHandler(svc *learn.Service, scope string, load func(*http.Request) (uuid.UUID, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := load(r)
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		var in struct {
			Translations []learn.TranslationInput `json:"translations"`
		}
		if err := decodeJSON(r, &in); err != nil {
			httperror.Render(w, r, err)
			return
		}
		sheet, err := svc.PutTranslations(r.Context(), scope, id, r.URL.Query().Get("lang"), in.Translations)
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, sheet)
	}
}

// preferences is Den's shared preferences plus Learn's own language.
type preferences struct {
	denlink.Preferences
	// Language is the saved UI and content language (null = browser's).
	Language *string `json:"language"`
}

// preferencesHandler serves GET and PUT /me/preferences. The theme lives in
// Den (shared by every app); the language is stored in Learn. PUT takes
// {theme?, language?}; language "" or null clears it.
func preferencesHandler(den *denlink.Client, svc *learn.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := auth.MustFromContext(r.Context())
		if err != nil {
			httperror.Render(w, r, httperror.ErrUnauthorized)
			return
		}
		identity := u.Issuer != "" && u.Subject != ""
		load := func() denlink.Preferences {
			p := denlink.Preferences{Theme: "system"}
			if identity {
				var err error
				if p, err = den.Preferences(r.Context(), u.Issuer, u.Subject); err != nil {
					hlog.FromRequest(r).Warn().Err(err).Msg("den link: load preferences failed")
				}
			}
			return p
		}
		respond := func(p denlink.Preferences) {
			out := preferences{Preferences: p}
			if lang, err := svc.UserLanguage(r.Context(), u.ID); err == nil && lang != "" {
				out.Language = &lang
			}
			writeJSON(w, http.StatusOK, out)
		}
		if r.Method == http.MethodGet {
			respond(load())
			return
		}
		var in struct {
			Theme    *string         `json:"theme"`
			Language json.RawMessage `json:"language"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in); err != nil {
			httperror.Render(w, r, httperror.BadRequest("invalid JSON body"))
			return
		}
		if in.Theme != nil {
			switch *in.Theme {
			case "system", "light", "dark":
			default:
				httperror.Render(w, r, httperror.Validation("theme must be system, light or dark", nil))
				return
			}
		}
		if len(in.Language) > 0 {
			var lang *string
			if err := json.Unmarshal(in.Language, &lang); err != nil {
				httperror.Render(w, r, httperror.Validation("language must be a string or null", nil))
				return
			}
			value := ""
			if lang != nil {
				value = *lang
			}
			if err := svc.SetUserLanguage(r.Context(), u.ID, value); err != nil {
				httperror.Render(w, r, err)
				return
			}
		}
		if in.Theme == nil {
			respond(load())
			return
		}
		if !identity {
			respond(denlink.Preferences{Theme: *in.Theme})
			return
		}
		p, err := den.SetPreferences(r.Context(), u.Issuer, u.Subject, *in.Theme)
		if err != nil {
			if !errors.Is(err, denlink.ErrNotPaired) {
				hlog.FromRequest(r).Warn().Err(err).Msg("den link: save preferences failed")
			}
			p = denlink.Preferences{Theme: *in.Theme}
		}
		respond(p)
	}
}
