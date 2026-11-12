package http

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/hlog"

	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/learn"
)

// People sources for the picker, reported as "source" in GET /users.
//
// Learn searches the people it knows (signed in once, or synced from a
// group). With the group directory on it also searches the identity
// provider's directory, so people who never signed in can be picked.
// HR does not offer a people search for other modules, so a paired Learn
// still uses this directory.
const (
	peopleSourceLearn     = "learn"
	peopleSourceDirectory = "learn+directory"
)

// minDirectoryQuery is the shortest query sent to the directory.
const minDirectoryQuery = 2

// withDirectoryPeople appends directory people Learn doesn't know yet.
// A directory failure is logged and the local results are returned.
func (h *courseHandler) withDirectoryPeople(r *http.Request, people []learn.Person, q string, withEmail bool) ([]learn.Person, string) {
	if h.directory == nil {
		return people, peopleSourceLearn
	}
	if len([]rune(q)) < minDirectoryQuery {
		return people, peopleSourceDirectory
	}
	members, err := h.directory.SearchDirectory(r.Context(), q, 20)
	if err == nil && len(members) > 0 {
		ids := make([]string, 0, len(members))
		for _, m := range members {
			ids = append(ids, m.ID)
		}
		var known map[string]bool
		if known, err = h.directory.KnownDirectoryIDs(r.Context(), ids); err == nil {
			for _, m := range members {
				if known[m.ID] {
					continue
				}
				p := learn.Person{DirectoryID: m.ID, DisplayName: strings.TrimSpace(m.FirstName + " " + m.LastName)}
				if p.DisplayName == "" {
					p.DisplayName = m.Username
				}
				if withEmail {
					p.Email = m.Email
				}
				people = append(people, p)
			}
		}
	}
	if err != nil {
		hlog.FromRequest(r).Warn().Err(err).Msg("directory people search failed")
	}
	return people, peopleSourceDirectory
}

// resolvePerson turns an enrol body's user_id or directory_id into a users
// row id. A directory person is created on first use.
func (h *courseHandler) resolvePerson(r *http.Request, course learn.Course, userID uuid.UUID, directoryID string) (uuid.UUID, error) {
	directoryID = strings.TrimSpace(directoryID)
	switch {
	case directoryID != "" && userID != uuid.Nil:
		return uuid.Nil, httperror.Validation("give user_id or directory_id, not both", nil)
	case directoryID != "":
		if h.directory == nil {
			return uuid.Nil, httperror.Validation("the group directory is off; pick someone who has signed in to Learn", nil)
		}
		return h.directory.EnsureDirectoryUser(r.Context(), directoryID, course.OrganizationID)
	case userID == uuid.Nil:
		return uuid.Nil, httperror.Validation("user_id is required", nil)
	}
	ok, err := h.svc.InOrganization(r.Context(), userID, course.OrganizationID)
	if err != nil {
		return uuid.Nil, err
	}
	if !ok {
		return uuid.Nil, httperror.NotFound("user")
	}
	return userID, nil
}
