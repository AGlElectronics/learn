package learn

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// Qualification is something a person can hold ("First aid"), awarded by
// finishing a course. ValidMonths nil = never expires.
type Qualification struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	Key            string    `json:"key"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	ValidMonths    *int      `json:"valid_months"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	// Courses awarding it and certificates issued (list views only).
	Courses      int `json:"courses"`
	Certificates int `json:"certificates"`
}

// QualificationRef is the short form attached to a course.
type QualificationRef struct {
	ID          uuid.UUID `json:"id"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	ValidMonths *int      `json:"valid_months"`
}

var qualificationKeyRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)

const qualificationCols = `q.id, q.organization_id, q.key, q.name, q.description, q.valid_months, q.created_at, q.updated_at,
	(SELECT count(*) FROM courses c WHERE c.awards_qualification_id = q.id),
	(SELECT count(*) FROM certificates ct WHERE ct.qualification_id = q.id)`

func scanQualification(row pgx.Row) (Qualification, error) {
	var q Qualification
	err := row.Scan(&q.ID, &q.OrganizationID, &q.Key, &q.Name, &q.Description, &q.ValidMonths,
		&q.CreatedAt, &q.UpdatedAt, &q.Courses, &q.Certificates)
	return q, err
}

// ListQualifications returns every qualification, by name. orgIDs nil means
// all organizations.
func (s *Service) ListQualifications(ctx context.Context, orgIDs []uuid.UUID) ([]Qualification, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+qualificationCols+` FROM qualifications q
		 WHERE $1::uuid[] IS NULL OR q.organization_id = ANY($1)
		 ORDER BY q.name, q.key`, orgIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Qualification{}
	for rows.Next() {
		q, err := scanQualification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// GetQualification loads one qualification or a typed 404.
func (s *Service) GetQualification(ctx context.Context, id uuid.UUID) (Qualification, error) {
	q, err := scanQualification(s.pool.QueryRow(ctx, `SELECT `+qualificationCols+` FROM qualifications q WHERE q.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Qualification{}, httperror.NotFound("qualification")
	}
	return q, err
}

// QualificationInput is the create/update payload; nil = unchanged on update.
// ClearValidMonths makes the qualification never expire.
type QualificationInput struct {
	Key              *string
	Name             *string
	Description      *string
	ValidMonths      *int
	ClearValidMonths bool
}

func (in *QualificationInput) normalize(create bool) error {
	if in.Key != nil {
		k := strings.ToLower(strings.TrimSpace(*in.Key))
		if !qualificationKeyRE.MatchString(k) {
			return httperror.Validation("key must be lowercase letters, digits, '-', '_' or '.' (e.g. first-aid)", nil)
		}
		in.Key = &k
	} else if create {
		return httperror.Validation("key is required", nil)
	}
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" {
			return httperror.Validation("name must not be empty", nil)
		}
		in.Name = &n
	} else if create {
		return httperror.Validation("name is required", nil)
	}
	if in.Description != nil {
		d := strings.TrimSpace(*in.Description)
		in.Description = &d
	}
	if in.ValidMonths != nil && (*in.ValidMonths <= 0 || *in.ValidMonths > 1200) {
		return httperror.Validation("valid_months must be between 1 and 1200 (or null for no expiry)", nil)
	}
	return nil
}

func qualificationWriteErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return httperror.Conflict("a qualification with this key already exists")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return httperror.NotFound("qualification")
	}
	return err
}

// CreateQualification adds a qualification to an organization.
func (s *Service) CreateQualification(ctx context.Context, orgID uuid.UUID, in QualificationInput) (Qualification, error) {
	if err := in.normalize(true); err != nil {
		return Qualification{}, err
	}
	desc := ""
	if in.Description != nil {
		desc = *in.Description
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO qualifications (organization_id, key, name, description, valid_months)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		orgID, *in.Key, *in.Name, desc, in.ValidMonths).Scan(&id)
	if err != nil {
		return Qualification{}, qualificationWriteErr(err)
	}
	return s.GetQualification(ctx, id)
}

// UpdateQualification edits a qualification. A new validity applies to
// certificates issued from now on; existing expiry dates stay.
func (s *Service) UpdateQualification(ctx context.Context, id uuid.UUID, in QualificationInput) (Qualification, error) {
	if err := in.normalize(false); err != nil {
		return Qualification{}, err
	}
	var got uuid.UUID
	err := s.pool.QueryRow(ctx, `
		UPDATE qualifications
		   SET key = COALESCE($2, key), name = COALESCE($3, name), description = COALESCE($4, description),
		       valid_months = CASE WHEN $6 THEN NULL ELSE COALESCE($5, valid_months) END,
		       updated_at = now()
		 WHERE id = $1 RETURNING id`,
		id, in.Key, in.Name, in.Description, in.ValidMonths, in.ClearValidMonths).Scan(&got)
	if err != nil {
		return Qualification{}, qualificationWriteErr(err)
	}
	return s.GetQualification(ctx, id)
}

// DeleteQualification removes a qualification nobody holds. Courses that
// awarded it stop awarding anything.
func (s *Service) DeleteQualification(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM qualifications WHERE id = $1`, id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return httperror.Conflict("certificates have been issued for this qualification; it can't be deleted")
	}
	if err == nil && tag.RowsAffected() == 0 {
		return httperror.NotFound("qualification")
	}
	return err
}

// CourseQualification returns the qualification a course awards (nil = none).
func (s *Service) CourseQualification(ctx context.Context, courseID uuid.UUID) (*QualificationRef, error) {
	var ref QualificationRef
	err := s.pool.QueryRow(ctx, `
		SELECT q.id, q.key, q.name, q.valid_months
		  FROM courses c JOIN qualifications q ON q.id = c.awards_qualification_id
		 WHERE c.id = $1`, courseID).Scan(&ref.ID, &ref.Key, &ref.Name, &ref.ValidMonths)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ref, nil
}

// AttachQualification fills the course's awarded qualification and the
// caller's cycle on a course detail.
func (s *Service) AttachQualification(ctx context.Context, d *CourseDetail, userID uuid.UUID) error {
	ref, err := s.CourseQualification(ctx, d.ID)
	if err != nil {
		return err
	}
	d.AwardsQualification = ref
	return s.pool.QueryRow(ctx, `SELECT `+cycleOf("$1", "$2"), d.ID, userID).Scan(&d.Cycle)
}

// SetCourseQualification sets (or clears, with nil) the qualification a
// course awards. It must belong to the course's organization. Learners who
// already finished the course in their current cycle get their
// certificate right away.
func (s *Service) SetCourseQualification(ctx context.Context, course Course, qualificationID *uuid.UUID) error {
	if qualificationID != nil {
		var ok bool
		if err := s.pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM qualifications WHERE id = $1 AND organization_id = $2)`,
			*qualificationID, course.OrganizationID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return httperror.Validation("awards_qualification_id must be a qualification of this organization", nil)
		}
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE courses SET awards_qualification_id = $2, updated_at = now() WHERE id = $1`,
		course.ID, qualificationID); err != nil {
		return err
	}
	if qualificationID == nil {
		return nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT user_id FROM enrolments WHERE course_id = $1 AND role = 'learner' AND status = 'active'`, course.ID)
	if err != nil {
		return err
	}
	users, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, u := range users {
		if _, err := awardIfComplete(ctx, s.pool, course.ID, u); err != nil {
			return err
		}
	}
	return nil
}

// CatalogQualification is one entry of the public qualification catalog.
type CatalogQualification struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ValidMonths *int   `json:"valid_months"`
}

// QualificationCatalog lists every qualification for other modules (HR).
func (s *Service) QualificationCatalog(ctx context.Context) ([]CatalogQualification, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (key) key, name, description, valid_months
		  FROM qualifications ORDER BY key, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CatalogQualification{}
	for rows.Next() {
		var q CatalogQualification
		if err := rows.Scan(&q.Key, &q.Name, &q.Description, &q.ValidMonths); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
