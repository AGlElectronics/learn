// Package domain holds the neutral learning vocabulary — role names and
// permission identifiers — used by both the service layer and the HTTP
// layer. Keeping these as typed constants prevents stringly typed drift
// between tables, APIs, and the access config file.
package domain

// Role names recognized by default. The access config file (TOML) may add
// more roles or redefine the permission set attached to any of these.
const (
	// RoleOrganizationAdmin manages every course, enrolment and setting.
	RoleOrganizationAdmin = "organization_admin"
	// RoleTrainer creates courses and manages the courses they staff.
	// Being staff on a course is an enrolment with role "trainer", not a
	// permission — the permission only opens the teaching feature.
	RoleTrainer = "trainer"
	// RoleCoordinator (team leads) only reads the completion and
	// qualification reports.
	RoleCoordinator = "coordinator"
)

// Enrolment roles. An enrolment row is what links a person to one course;
// there is no "learner" IdP group — reading a published course requires an
// enrolment, whatever the person's other roles are.
const (
	EnrolmentLearner = "learner"
	EnrolmentTrainer = "trainer"
)

// Activity kinds. The column is the extension point for sessions and file
// activities later.
const (
	ActivityPage = "page"
	ActivityQuiz = "quiz"
	// ActivitySession is an in-person session with bookable time slots.
	ActivitySession = "session"
)

// Question kinds in the quiz question bank.
const (
	QuestionSingle   = "single"
	QuestionMultiple = "multiple"
	// QuestionHotspot: tap the right places on a picture.
	QuestionHotspot = "hotspot"
)

// Media kinds in a course's media library.
const (
	MediaImage = "image"
	MediaAudio = "audio"
	MediaVideo = "video"
)

// Permission identifiers.
const (
	// PermCourseManage: create, edit, publish and enrol on EVERY course.
	PermCourseManage = "course.manage"
	// PermCourseTeach: create courses; edit and enrol only on courses the
	// holder staffs (enrolment role "trainer").
	PermCourseTeach = "course.teach"
	// PermEnrolManage: add and remove people on a course the holder may
	// manage (see PermCourseManage / PermCourseTeach).
	PermEnrolManage = "enrol.manage"
	// PermSettingsManage: module settings, including the Den pairing.
	PermSettingsManage = "settings.manage"
	// PermReportView: completion and qualification reports for every
	// course and person of the organization.
	PermReportView = "report.view"
)

// AllPermissions returns every permission the access-config validator will
// accept. The access config file may only reference identifiers listed here;
// unknown names fail configuration load with a clear error.
func AllPermissions() []string {
	return []string{
		PermCourseManage,
		PermCourseTeach,
		PermEnrolManage,
		PermSettingsManage,
		PermReportView,
	}
}

// DefaultRolePermissions is the built-in role → permission catalog applied
// when the access config file omits a role (or isn't configured at all).
func DefaultRolePermissions() map[string][]string {
	return map[string][]string{
		RoleOrganizationAdmin: AllPermissions(),
		RoleTrainer: {
			PermCourseTeach,
			PermEnrolManage,
		},
	}
}
