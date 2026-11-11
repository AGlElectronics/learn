package learn

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// CopyCourseInput is the copy payload.
type CopyCourseInput struct {
	Title             string
	IncludeGroupLinks bool
}

// copySteps copy the content of course $1 into course $2 using the
// copy_map temp table (kind, old_id → new_id) to remap references. $1/$2
// are replaced by lookups of the 'course' row, not bound as parameters.
// Order matters: every step only refers to kinds mapped by earlier steps.
var copySteps = []string{
	`INSERT INTO copy_map SELECT 'section', id, gen_random_uuid() FROM sections WHERE course_id = $1`,
	`INSERT INTO sections (id, course_id, title, position, created_at)
	 SELECT m.new_id, $2, s.title, s.position, s.created_at
	   FROM sections s JOIN copy_map m ON m.kind = 'section' AND m.old_id = s.id`,

	`INSERT INTO copy_map SELECT 'activity', a.id, gen_random_uuid()
	   FROM activities a JOIN sections s ON s.id = a.section_id WHERE s.course_id = $1`,
	`INSERT INTO activities (id, section_id, kind, title, body, position, pass_percent, max_attempts, shuffle_options, created_at)
	 SELECT m.new_id, ms.new_id, a.kind, a.title, a.body, a.position, a.pass_percent, a.max_attempts, a.shuffle_options, a.created_at
	   FROM activities a
	   JOIN copy_map m ON m.kind = 'activity' AND m.old_id = a.id
	   JOIN copy_map ms ON ms.kind = 'section' AND ms.old_id = a.section_id`,
	`INSERT INTO activity_prerequisites (activity_id, requires_activity_id)
	 SELECT ma.new_id, mr.new_id
	   FROM activity_prerequisites p
	   JOIN copy_map ma ON ma.kind = 'activity' AND ma.old_id = p.activity_id
	   JOIN copy_map mr ON mr.kind = 'activity' AND mr.old_id = p.requires_activity_id`,

	// Media rows are new rows pointing at the same stored objects.
	`INSERT INTO copy_map SELECT 'media', id, gen_random_uuid() FROM media WHERE course_id = $1`,
	`INSERT INTO media (id, course_id, kind, content_type, size_bytes, object_key, status, alt_text, name, created_by, created_at)
	 SELECT m.new_id, $2, md.kind, md.content_type, md.size_bytes, md.object_key, md.status, md.alt_text, md.name, md.created_by, md.created_at
	   FROM media md JOIN copy_map m ON m.kind = 'media' AND m.old_id = md.id`,
	`INSERT INTO copy_map SELECT 'activity_media', am.id, gen_random_uuid()
	   FROM activity_media am
	   JOIN copy_map ma ON ma.kind = 'activity' AND ma.old_id = am.activity_id
	   LEFT JOIN copy_map mm ON mm.kind = 'media' AND mm.old_id = am.media_id
	  WHERE am.media_id IS NULL OR mm.new_id IS NOT NULL`,
	`INSERT INTO activity_media (id, activity_id, media_id, external_url, position, caption)
	 SELECT m.new_id, ma.new_id, mm.new_id, am.external_url, am.position, am.caption
	   FROM activity_media am
	   JOIN copy_map m ON m.kind = 'activity_media' AND m.old_id = am.id
	   JOIN copy_map ma ON ma.kind = 'activity' AND ma.old_id = am.activity_id
	   LEFT JOIN copy_map mm ON mm.kind = 'media' AND mm.old_id = am.media_id`,

	`INSERT INTO copy_map SELECT 'category', id, gen_random_uuid() FROM question_categories WHERE course_id = $1`,
	`INSERT INTO question_categories (id, course_id, name, position, created_at)
	 SELECT m.new_id, $2, c.name, c.position, c.created_at
	   FROM question_categories c JOIN copy_map m ON m.kind = 'category' AND m.old_id = c.id`,
	`INSERT INTO copy_map SELECT 'question', q.id, gen_random_uuid()
	   FROM questions q JOIN question_categories c ON c.id = q.category_id WHERE c.course_id = $1`,
	`INSERT INTO questions (id, category_id, kind, prompt, feedback, points, position, media_id, feedback_media_id, hotspot_clicks, created_at)
	 SELECT m.new_id, mc.new_id, q.kind, q.prompt, q.feedback, q.points, q.position,
	        mm.new_id, mf.new_id, q.hotspot_clicks, q.created_at
	   FROM questions q
	   JOIN copy_map m ON m.kind = 'question' AND m.old_id = q.id
	   JOIN copy_map mc ON mc.kind = 'category' AND mc.old_id = q.category_id
	   LEFT JOIN copy_map mm ON mm.kind = 'media' AND mm.old_id = q.media_id
	   LEFT JOIN copy_map mf ON mf.kind = 'media' AND mf.old_id = q.feedback_media_id`,
	`INSERT INTO copy_map SELECT 'option', o.id, gen_random_uuid()
	   FROM question_options o JOIN copy_map mq ON mq.kind = 'question' AND mq.old_id = o.question_id`,
	`INSERT INTO question_options (id, question_id, label, correct, position, media_id)
	 SELECT m.new_id, mq.new_id, o.label, o.correct, o.position, mm.new_id
	   FROM question_options o
	   JOIN copy_map m ON m.kind = 'option' AND m.old_id = o.id
	   JOIN copy_map mq ON mq.kind = 'question' AND mq.old_id = o.question_id
	   LEFT JOIN copy_map mm ON mm.kind = 'media' AND mm.old_id = o.media_id`,
	`INSERT INTO question_regions (question_id, shape, coords, correct, position)
	 SELECT mq.new_id, r.shape, r.coords, r.correct, r.position
	   FROM question_regions r JOIN copy_map mq ON mq.kind = 'question' AND mq.old_id = r.question_id`,
	`INSERT INTO quiz_slots (activity_id, position, question_id, category_id, draw_count)
	 SELECT ma.new_id, sl.position, mq.new_id, mc.new_id, sl.draw_count
	   FROM quiz_slots sl
	   JOIN copy_map ma ON ma.kind = 'activity' AND ma.old_id = sl.activity_id
	   LEFT JOIN copy_map mq ON mq.kind = 'question' AND mq.old_id = sl.question_id
	   LEFT JOIN copy_map mc ON mc.kind = 'category' AND mc.old_id = sl.category_id`,

	`INSERT INTO team_board_groups (course_id, group_name)
	 SELECT $2, group_name FROM team_board_groups WHERE course_id = $1`,

	// Translations follow their remapped rows. The course title is not
	// carried over: the copy gets a new title in every language.
	`INSERT INTO translations (entity_type, entity_id, field, lang, text, updated_at)
	 SELECT t.entity_type, m.new_id, t.field, t.lang, t.text, t.updated_at
	   FROM translations t JOIN copy_map m ON m.kind = t.entity_type AND m.old_id = t.entity_id
	  WHERE NOT (t.entity_type = 'course' AND t.field = 'title')`,
}

// CopyCourse deep-copies a course for a new edition in one transaction:
// settings, sections, activities (session activities without their time
// slots), prerequisites, the question bank with hotspot regions, quiz
// slots, media rows (sharing the stored files) and page attachments, the
// languages and translations, feedback and team board settings (with the
// board's groups), plus the group links when asked. The copy is unpublished, has no learner
// data (enrolments, attempts, completions, certificates) and the copier
// becomes its trainer.
func (s *Service) CopyCourse(ctx context.Context, sourceID, userID uuid.UUID, in CopyCourseInput) (Course, error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return Course{}, httperror.Validation("title is required", nil)
	}
	var c Course
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		c, err = scanCourse(tx.QueryRow(ctx, `
			INSERT INTO courses (organization_id, title, summary, published, self_enrol, created_by,
			                     due_days, awards_qualification_id, feedback_enabled, feedback_anonymous,
			                     team_board_enabled, default_lang, languages)
			SELECT organization_id, $2, summary, FALSE, self_enrol, $3,
			       due_days, awards_qualification_id, feedback_enabled, feedback_anonymous,
			       team_board_enabled, default_lang, languages
			  FROM courses WHERE id = $1
			RETURNING `+courseCols, sourceID, in.Title, userID))
		if errors.Is(err, pgx.ErrNoRows) {
			return httperror.NotFound("course")
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO enrolments (course_id, user_id, role) VALUES ($1, $2, $3)`,
			c.ID, userID, domain.EnrolmentTrainer); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			CREATE TEMP TABLE copy_map (kind TEXT NOT NULL, old_id UUID NOT NULL, new_id UUID NOT NULL,
			                            PRIMARY KEY (kind, old_id)) ON COMMIT DROP`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO copy_map VALUES ('course', $1, $2)`, sourceID, c.ID); err != nil {
			return err
		}
		src := `(SELECT old_id FROM copy_map WHERE kind = 'course')`
		dst := `(SELECT new_id FROM copy_map WHERE kind = 'course')`
		for _, step := range copySteps {
			if _, err := tx.Exec(ctx, strings.ReplaceAll(strings.ReplaceAll(step, "$1", src), "$2", dst)); err != nil {
				return err
			}
		}
		if in.IncludeGroupLinks {
			if _, err := tx.Exec(ctx, `
				INSERT INTO course_group_links (course_id, group_name, group_path, path_resolved, role, created_by)
				SELECT $2, group_name, group_path, path_resolved, role, $3 FROM course_group_links WHERE course_id = $1`,
				sourceID, c.ID, userID); err != nil {
				return err
			}
		}
		return nil
	})
	return c, err
}
