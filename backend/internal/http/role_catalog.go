package http

import (
	"net/http"
	"sort"
	"strings"

	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/domain"
)

type roleCatalogEntry struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Group       string   `json:"group"`
	Assignable  bool     `json:"assignable"`
	Scope       string   `json:"scope"`
	Permissions []string `json:"permissions"`
	Note        string   `json:"note,omitempty"`
}

var learnRoleDescriptions = map[string]string{
	domain.RoleOrganizationAdmin: "Manages every course, enrolment, qualification and the module settings.",
	domain.RoleTrainer:           "Creates courses and runs the courses they teach.",
	domain.RoleTrainingManager:   "Decides who teaches: adds and removes trainers on every course.",
	domain.RoleCoordinator:       "Training office: sees the reports and manages qualifications and certificates.",
}

// roleCatalogHandler serves GET /api/role-catalog (public, read-only). Roles
// bound to an IdP group in the access config are assignable from HR; course
// learners are enrolments inside Learn, not a role.
func roleCatalogHandler(access *auth.AccessConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		roles := make([]roleCatalogEntry, 0)
		bindings := access.Bindings()
		for _, role := range access.Roles() {
			set := access.PermissionsForRole(role)
			perms := make([]string, 0, len(set))
			for p := range set {
				perms = append(perms, p)
			}
			sort.Strings(perms)
			desc := access.RoleDescription(role)
			if desc == "" {
				desc = learnRoleDescriptions[role]
			}
			base := roleCatalogEntry{
				Key:         role,
				Label:       humanizeRole(role),
				Description: desc,
				Permissions: perms,
			}
			bound := false
			for _, b := range bindings {
				if b.Role != role {
					continue
				}
				bound = true
				entry := base
				entry.Group = b.Group
				entry.Assignable = true
				entry.Scope = "organization:" + b.Organization
				roles = append(roles, entry)
			}
			if !bound {
				entry := base
				entry.Scope = "course"
				entry.Note = "Assigned per course inside Learn, not via an IdP group."
				roles = append(roles, entry)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"module":       "learn",
			"display_name": "Learn",
			"description":  "Courses, enrolments and training progress.",
			"roles":        roles,
		})
	}
}

func humanizeRole(role string) string {
	parts := strings.Split(strings.ReplaceAll(role, "_", " "), " ")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}
