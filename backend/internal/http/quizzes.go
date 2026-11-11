package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AGlElectronics/learn/backend/internal/audit"
	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/learn"
)

// quizHandler owns the question bank, quiz slots, attempts and the
// gradebook. Authoring and learner data follow canEdit (course staff);
// taking a quiz needs an enrolment on a published course, or staff.
type quizHandler struct {
	*courseHandler
}

// mountQuizRoutes registers the quiz routes on the authenticated /api/v1
// router.
func mountQuizRoutes(r chi.Router, courses *courseHandler) {
	h := &quizHandler{courses}

	r.Get("/courses/{course}/question-categories", h.listCategories)
	r.Post("/courses/{course}/question-categories", h.createCategory)
	r.Get("/courses/{course}/gradebook", h.gradebook)
	r.Patch("/question-categories/{category}", h.updateCategory)
	r.Delete("/question-categories/{category}", h.deleteCategory)
	r.Get("/question-categories/{category}/questions", h.listQuestions)
	r.Post("/question-categories/{category}/questions", h.createQuestion)
	r.Patch("/questions/{question}", h.updateQuestion)
	r.Delete("/questions/{question}", h.deleteQuestion)
	r.Put("/questions/{question}/regions", h.replaceRegions)
	r.Post("/questions/{question}/options", h.createOption)
	r.Patch("/options/{option}", h.updateOption)
	r.Delete("/options/{option}", h.deleteOption)

	r.Get("/activities/{activity}/slots", h.listSlots)
	r.Post("/activities/{activity}/slots", h.createSlot)
	r.Patch("/slots/{slot}", h.updateSlot)
	r.Delete("/slots/{slot}", h.deleteSlot)

	r.Get("/activities/{activity}/quiz", h.quizInfo)
	r.Post("/activities/{activity}/attempts", h.startAttempt)
	r.Post("/activities/{activity}/reset", h.resetAttempts)
	r.Get("/attempts/{attempt}", h.getAttempt)
	r.Post("/attempts/{attempt}/submit", h.submitAttempt)
}

// nullableInt tells an explicit JSON null ("clear it") from an absent field.
type nullableInt struct {
	Set   bool
	Value *int
}

func (n *nullableInt) UnmarshalJSON(b []byte) error {
	n.Set = true
	if string(b) == "null" {
		n.Value = nil
		return nil
	}
	var v int
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	n.Value = &v
	return nil
}

// staffOn parses the {param} id, resolves its course and requires edit
// rights on it.
func (h *quizHandler) staffOn(r *http.Request, param string, resolve func(context.Context, uuid.UUID) (learn.Course, error)) (uuid.UUID, learn.Course, error) {
	id, err := parseUUIDParam(r, param)
	if err != nil {
		return uuid.Nil, learn.Course{}, err
	}
	course, err := resolve(r.Context(), id)
	if err != nil {
		return uuid.Nil, learn.Course{}, err
	}
	if err := h.requireEdit(r, course); err != nil {
		return uuid.Nil, learn.Course{}, err
	}
	return id, course, nil
}

// requireTake lets staff and enrolled people take a course's quizzes.
// Drafts stay invisible (404) to everyone else.
func (h *quizHandler) requireTake(r *http.Request, course learn.Course) (*auth.User, error) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		return nil, httperror.ErrUnauthorized
	}
	staff, err := h.canEdit(r, course)
	if err != nil {
		return nil, err
	}
	if staff {
		return u, nil
	}
	if !course.Published {
		return nil, httperror.NotFound("course")
	}
	role, err := h.svc.EnrolmentRole(r.Context(), course.ID, u.ID)
	if err != nil {
		return nil, err
	}
	if role == "" {
		return nil, httperror.New(httperror.CodeForbidden, "you are not enrolled in this course")
	}
	return u, nil
}

// ----------------------------------------------------------------------------
// Bank: categories
// ----------------------------------------------------------------------------

func (h *quizHandler) listCategories(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "course", h.svc.GetCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	out, err := h.svc.ListCategories(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": out})
}

func (h *quizHandler) createCategory(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "course", h.svc.GetCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	c, err := h.svc.CreateCategory(r.Context(), id, in.Name)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (h *quizHandler) updateCategory(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "category", h.svc.CategoryCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Name     *string `json:"name"`
		Position *int    `json:"position"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	c, err := h.svc.UpdateCategory(r.Context(), id, in.Name, in.Position)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *quizHandler) deleteCategory(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "category", h.svc.CategoryCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.svc.DeleteCategory(r.Context(), id); err != nil {
		httperror.Render(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----------------------------------------------------------------------------
// Bank: questions and options
// ----------------------------------------------------------------------------

func (h *quizHandler) listQuestions(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "category", h.svc.CategoryCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	out, err := h.svc.ListQuestions(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"questions": out})
}

func (h *quizHandler) createQuestion(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "category", h.svc.CategoryCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Kind     string `json:"kind"`
		Prompt   string `json:"prompt"`
		Feedback string `json:"feedback"`
		Points   *int   `json:"points"`
		Options  []struct {
			Label   string     `json:"label"`
			Correct bool       `json:"correct"`
			MediaID *uuid.UUID `json:"media_id"`
		} `json:"options"`
		MediaID         *uuid.UUID    `json:"media_id"`
		FeedbackMediaID *uuid.UUID    `json:"feedback_media_id"`
		HotspotClicks   *int          `json:"hotspot_clicks"`
		Regions         []regionInput `json:"regions"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	opts := make([]learn.OptionInput, len(in.Options))
	for i, o := range in.Options {
		opts[i] = learn.OptionInput{Label: o.Label, Correct: o.Correct, MediaID: o.MediaID}
	}
	q, err := h.svc.CreateQuestion(r.Context(), id, learn.CreateQuestionInput{
		Kind: in.Kind, Prompt: in.Prompt, Feedback: in.Feedback, Points: in.Points, Options: opts,
		MediaID: in.MediaID, FeedbackMediaID: in.FeedbackMediaID, HotspotClicks: in.HotspotClicks,
		Regions: regionInputs(in.Regions),
	})
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, q)
}

func (h *quizHandler) updateQuestion(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "question", h.svc.QuestionCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Kind            *string      `json:"kind"`
		Prompt          *string      `json:"prompt"`
		Feedback        *string      `json:"feedback"`
		Points          *int         `json:"points"`
		Position        *int         `json:"position"`
		MediaID         nullableUUID `json:"media_id"`
		FeedbackMediaID nullableUUID `json:"feedback_media_id"`
		HotspotClicks   nullableInt  `json:"hotspot_clicks"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	q, err := h.svc.UpdateQuestion(r.Context(), id, learn.UpdateQuestionInput{
		Kind: in.Kind, Prompt: in.Prompt, Feedback: in.Feedback, Points: in.Points, Position: in.Position,
		MediaID: in.MediaID.Value, ClearMedia: in.MediaID.Set && in.MediaID.Value == nil,
		FeedbackMediaID: in.FeedbackMediaID.Value, ClearFeedbackMedia: in.FeedbackMediaID.Set && in.FeedbackMediaID.Value == nil,
		HotspotClicks: in.HotspotClicks.Value, ClearHotspotClicks: in.HotspotClicks.Set && in.HotspotClicks.Value == nil,
	})
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, q)
}

func (h *quizHandler) deleteQuestion(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "question", h.svc.QuestionCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.svc.DeleteQuestion(r.Context(), id); err != nil {
		httperror.Render(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Option changes answer with the whole updated question.

func (h *quizHandler) createOption(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "question", h.svc.QuestionCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Label   string     `json:"label"`
		Correct bool       `json:"correct"`
		MediaID *uuid.UUID `json:"media_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	q, err := h.svc.CreateOption(r.Context(), id, learn.OptionInput{Label: in.Label, Correct: in.Correct, MediaID: in.MediaID})
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, q)
}

func (h *quizHandler) updateOption(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "option", h.svc.OptionCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Label    *string      `json:"label"`
		Correct  *bool        `json:"correct"`
		Position *int         `json:"position"`
		MediaID  nullableUUID `json:"media_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	q, err := h.svc.UpdateOption(r.Context(), id, learn.UpdateOptionInput{
		Label: in.Label, Correct: in.Correct, Position: in.Position,
		MediaID: in.MediaID.Value, ClearMedia: in.MediaID.Set && in.MediaID.Value == nil,
	})
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, q)
}

func (h *quizHandler) deleteOption(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "option", h.svc.OptionCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	q, err := h.svc.DeleteOption(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, q)
}

// ----------------------------------------------------------------------------
// Quiz slots
// ----------------------------------------------------------------------------

// listSlots answers with the slots plus what still stops the quiz from
// being taken (empty when ready).
func (h *quizHandler) listSlots(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "activity", h.svc.ActivityCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if _, err := h.svc.QuizActivity(r.Context(), id); err != nil {
		httperror.Render(w, r, err)
		return
	}
	slots, err := h.svc.ListSlots(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	problems, err := h.svc.QuizProblems(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"slots": slots, "problems": problems})
}

func (h *quizHandler) createSlot(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "activity", h.svc.ActivityCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		QuestionID *uuid.UUID `json:"question_id"`
		CategoryID *uuid.UUID `json:"category_id"`
		DrawCount  *int       `json:"draw_count"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	slot, err := h.svc.CreateSlot(r.Context(), id, learn.CreateSlotInput{
		QuestionID: in.QuestionID, CategoryID: in.CategoryID, DrawCount: in.DrawCount,
	})
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, slot)
}

func (h *quizHandler) updateSlot(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "slot", h.svc.SlotCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Position  *int `json:"position"`
		DrawCount *int `json:"draw_count"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	slot, err := h.svc.UpdateSlot(r.Context(), id, in.Position, in.DrawCount)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, slot)
}

func (h *quizHandler) deleteSlot(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "slot", h.svc.SlotCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.svc.DeleteSlot(r.Context(), id); err != nil {
		httperror.Render(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----------------------------------------------------------------------------
// Taking a quiz
// ----------------------------------------------------------------------------

func (h *quizHandler) quizInfo(w http.ResponseWriter, r *http.Request) {
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
	info, err := h.svc.QuizInfo(r.Context(), id, u.ID)
	if err == nil {
		err = h.svc.TranslateQuizInfo(r.Context(), course.ID, &info, requestLangs(r, h.svc))
	}
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// startAttempt answers 201 for a new attempt and 200 when resuming the
// caller's open one.
func (h *quizHandler) startAttempt(w http.ResponseWriter, r *http.Request) {
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
	attempt, created, err := h.svc.StartAttempt(r.Context(), id, u.ID)
	if err == nil {
		err = h.svc.TranslateAttempt(r.Context(), course.ID, &attempt, requestLangs(r, h.svc))
	}
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, attempt)
}

// resetAttempts serves POST /activities/{activity}/reset: course.manage
// only, with the quiz's title typed as confirmation. Deletes the current
// cycle's attempts and completions; past cycles and certificates stay.
func (h *quizHandler) resetAttempts(w http.ResponseWriter, r *http.Request) {
	id, course, err := h.staffOn(r, "activity", h.svc.ActivityCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	u, _ := auth.FromContext(r.Context())
	if !u.HasInOrg(domain.PermCourseManage, course.OrganizationID) {
		httperror.Render(w, r, httperror.New(httperror.CodeForbidden, "only course managers (course.manage) can reset a quiz"))
		return
	}
	var in struct {
		Confirm string `json:"confirm"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	quiz, err := h.svc.QuizActivity(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if strings.TrimSpace(in.Confirm) != strings.TrimSpace(quiz.Title) {
		httperror.Render(w, r, httperror.Validation("type the quiz's title in confirm to reset it", map[string]string{"expected": quiz.Title}))
		return
	}
	res, err := h.svc.ResetAttempts(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	h.record(r, courseEntry(audit.QuizReset, course, "activity", id.String(),
		map[string]any{"attempts": res.Attempts, "completions": res.Completions}))
	writeJSON(w, http.StatusOK, res)
}

// getAttempt serves the attempt's owner, or course staff.
func (h *quizHandler) getAttempt(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "attempt")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	course, owner, err := h.svc.AttemptOwner(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	u, err := h.requireTake(r, course)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if owner != u.ID {
		if err := h.requireEdit(r, course); err != nil {
			httperror.Render(w, r, err)
			return
		}
	}
	attempt, err := h.svc.GetAttempt(r.Context(), id)
	if err == nil {
		err = h.svc.TranslateAttempt(r.Context(), course.ID, &attempt, requestLangs(r, h.svc))
	}
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, attempt)
}

func (h *quizHandler) submitAttempt(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "attempt")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	course, _, err := h.svc.AttemptOwner(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	u, err := h.requireTake(r, course)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Answers map[uuid.UUID][]uuid.UUID   `json:"answers"`
		Taps    map[uuid.UUID][]learn.Point `json:"taps"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	attempt, err := h.svc.SubmitAttempt(r.Context(), id, u.ID, in.Answers, in.Taps)
	if err == nil {
		err = h.svc.TranslateAttempt(r.Context(), course.ID, &attempt, requestLangs(r, h.svc))
	}
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	h.record(r, courseEntry(audit.QuizGraded, course, "quiz_attempt", attempt.ID.String(), map[string]any{
		"activity_id": attempt.ActivityID, "user_id": attempt.UserID,
		"score": attempt.Score, "max_score": attempt.MaxScore, "passed": attempt.Passed,
	}))
	writeJSON(w, http.StatusOK, attempt)
}

// ----------------------------------------------------------------------------
// Gradebook
// ----------------------------------------------------------------------------

func (h *quizHandler) gradebook(w http.ResponseWriter, r *http.Request) {
	id, _, err := h.staffOn(r, "course", h.svc.GetCourse)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	g, err := h.svc.Gradebook(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}
