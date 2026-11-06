package learn

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// Certificate statuses (public verify answers only these three).
const (
	CertificateValid   = "valid"
	CertificateExpired = "expired"
	CertificateRevoked = "revoked"
)

const (
	// RenewalWindow is how long before expiry a learner may renew.
	RenewalWindow = 60 * 24 * time.Hour
	// ExpiringSoonWindow flags certificates that expire within 30 days.
	ExpiringSoonWindow = 30 * 24 * time.Hour
)

// Certificate is one issued qualification, as its holder or staff see it.
type Certificate struct {
	ID                       uuid.UUID  `json:"id"`
	UserID                   uuid.UUID  `json:"user_id"`
	DisplayName              string     `json:"display_name"`
	OrganizationID           uuid.UUID  `json:"organization_id"`
	QualificationID          uuid.UUID  `json:"qualification_id"`
	QualificationKey         string     `json:"qualification_key"`
	QualificationName        string     `json:"qualification_name"`
	QualificationDescription string     `json:"qualification_description"`
	CourseID                 *uuid.UUID `json:"course_id"`
	CourseTitle              string     `json:"course_title"`
	Cycle                    int        `json:"cycle"`
	IssuedAt                 time.Time  `json:"issued_at"`
	ExpiresAt                *time.Time `json:"expires_at"`
	VerifyCode               string     `json:"verify_code"`
	RevokedAt                *time.Time `json:"revoked_at"`
	RevokedReason            string     `json:"revoked_reason"`
	Status                   string     `json:"status"`
	ExpiringSoon             bool       `json:"expiring_soon"`
	// VerifyURL is filled by the HTTP layer from PUBLIC_BASE_URL.
	VerifyURL string `json:"verify_url,omitempty"`
}

// MyQualification is the latest certificate per qualification for its
// holder, with what they can do about renewal.
type MyQualification struct {
	Certificate
	// RenewableFrom is when renewal opens (nil = never expires).
	RenewableFrom *time.Time `json:"renewable_from"`
	// Renewable: the learner may start a new cycle on the course now.
	Renewable bool `json:"renewable"`
	// RenewalInProgress: a new cycle has started and isn't finished yet.
	RenewalInProgress bool `json:"renewal_in_progress"`
}

// PublicVerification is all the public verify page may learn.
type PublicVerification struct {
	DisplayName   string     `json:"display_name"`
	Qualification string     `json:"qualification"`
	IssuedAt      time.Time  `json:"issued_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
	Status        string     `json:"status"`
	// QualificationID is only used to translate the name.
	QualificationID uuid.UUID `json:"-"`
}

// Holding is one valid qualification of a person, for HR.
type Holding struct {
	Key       string     `json:"key"`
	ExpiresAt *time.Time `json:"expires_at"`
}

func certificateStatus(expiresAt, revokedAt *time.Time, now time.Time) string {
	switch {
	case revokedAt != nil:
		return CertificateRevoked
	case expiresAt != nil && !now.Before(*expiresAt):
		return CertificateExpired
	default:
		return CertificateValid
	}
}

// newVerifyCode is 7 random bytes in unpadded base32: 12 characters
// (A-Z, 2-7), easy to read out and type.
func newVerifyCode() (string, error) {
	b := make([]byte, 7)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}

// awardIfComplete issues the course's certificate when the person (an
// active learner) has completed every activity in their current cycle and
// the course awards a qualification. Idempotent per cycle; returns the
// certificate id when one was issued now.
func awardIfComplete(ctx context.Context, q dbtx, courseID, userID uuid.UUID) (*uuid.UUID, error) {
	var qualificationID *uuid.UUID
	var validMonths *int
	var cycle int
	var role, status string
	err := q.QueryRow(ctx, `
		SELECT c.awards_qualification_id, ql.valid_months, e.cycle, e.role, e.status
		  FROM courses c
		  JOIN enrolments e ON e.course_id = c.id AND e.user_id = $2
		  LEFT JOIN qualifications ql ON ql.id = c.awards_qualification_id
		 WHERE c.id = $1`, courseID, userID).Scan(&qualificationID, &validMonths, &cycle, &role, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if qualificationID == nil || role != domain.EnrolmentLearner || status != EnrolmentStatusActive {
		return nil, nil
	}
	var total, done int
	if err := q.QueryRow(ctx, `
		SELECT count(a.id), count(cp.id)
		  FROM activities a
		  JOIN sections s ON s.id = a.section_id
		  LEFT JOIN completions cp ON cp.activity_id = a.id AND cp.user_id = $2 AND cp.cycle = $3
		 WHERE s.course_id = $1`, courseID, userID, cycle).Scan(&total, &done); err != nil {
		return nil, err
	}
	if total == 0 || done < total {
		return nil, nil
	}
	for range 5 {
		code, err := newVerifyCode()
		if err != nil {
			return nil, err
		}
		var id uuid.UUID
		err = q.QueryRow(ctx, `
			INSERT INTO certificates (user_id, qualification_id, course_id, cycle, expires_at, verify_code)
			VALUES ($1, $2, $3, $4,
			        CASE WHEN $5::int IS NULL THEN NULL ELSE now() + make_interval(months => $5::int) END, $6)
			ON CONFLICT DO NOTHING
			RETURNING id`, userID, *qualificationID, courseID, cycle, validMonths, code).Scan(&id)
		if err == nil {
			return &id, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		// Either this cycle's certificate exists already, or the code
		// collided: only the latter is worth another try.
		var exists bool
		if err := q.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM certificates WHERE course_id = $1 AND user_id = $2 AND cycle = $3)`,
			courseID, userID, cycle).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			return nil, nil
		}
	}
	return nil, errors.New("could not generate a unique verify code")
}

const certificateSelect = `
	SELECT ct.id, ct.user_id, u.display_name, ql.organization_id, ql.id, ql.key, ql.name, ql.description,
	       ct.course_id, COALESCE(c.title, pth.name, ''), ct.cycle, ct.issued_at, ct.expires_at, ct.verify_code,
	       ct.revoked_at, ct.revoked_reason
	  FROM certificates ct
	  JOIN users u ON u.id = ct.user_id
	  JOIN qualifications ql ON ql.id = ct.qualification_id
	  LEFT JOIN courses c ON c.id = ct.course_id
	  LEFT JOIN paths pth ON pth.id = ct.path_id`

func scanCertificate(row pgx.Row, now time.Time) (Certificate, error) {
	var c Certificate
	err := row.Scan(&c.ID, &c.UserID, &c.DisplayName, &c.OrganizationID, &c.QualificationID, &c.QualificationKey,
		&c.QualificationName, &c.QualificationDescription, &c.CourseID, &c.CourseTitle, &c.Cycle, &c.IssuedAt,
		&c.ExpiresAt, &c.VerifyCode, &c.RevokedAt, &c.RevokedReason)
	c.Status = certificateStatus(c.ExpiresAt, c.RevokedAt, now)
	c.ExpiringSoon = c.Status == CertificateValid && c.ExpiresAt != nil && c.ExpiresAt.Sub(now) <= ExpiringSoonWindow
	return c, err
}

// GetCertificate loads one certificate or a typed 404.
func (s *Service) GetCertificate(ctx context.Context, id uuid.UUID) (Certificate, error) {
	c, err := scanCertificate(s.pool.QueryRow(ctx, certificateSelect+` WHERE ct.id = $1`, id), time.Now())
	if errors.Is(err, pgx.ErrNoRows) {
		return Certificate{}, httperror.NotFound("certificate")
	}
	return c, err
}

func (s *Service) listCertificates(ctx context.Context, where string, args ...any) ([]Certificate, error) {
	rows, err := s.pool.Query(ctx, certificateSelect+` WHERE `+where+` ORDER BY ct.issued_at DESC, ct.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now()
	out := []Certificate{}
	for rows.Next() {
		c, err := scanCertificate(rows, now)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// QualificationCertificates lists every certificate of a qualification
// (staff view, newest first).
func (s *Service) QualificationCertificates(ctx context.Context, qualificationID uuid.UUID) ([]Certificate, error) {
	return s.listCertificates(ctx, `ct.qualification_id = $1`, qualificationID)
}

// UserCertificates lists every certificate a person holds, newest first.
func (s *Service) UserCertificates(ctx context.Context, userID uuid.UUID) ([]Certificate, error) {
	return s.listCertificates(ctx, `ct.user_id = $1`, userID)
}

// MyQualifications returns the person's latest certificate per
// qualification, with renewal state.
func (s *Service) MyQualifications(ctx context.Context, userID uuid.UUID) ([]MyQualification, error) {
	all, err := s.UserCertificates(ctx, userID)
	if err != nil {
		return nil, err
	}
	cycles := map[uuid.UUID]int{}
	rows, err := s.pool.Query(ctx, `
		SELECT course_id, cycle FROM enrolments
		 WHERE user_id = $1 AND role = 'learner' AND status = 'active'`, userID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id uuid.UUID
		var cycle int
		if err := rows.Scan(&id, &cycle); err != nil {
			rows.Close()
			return nil, err
		}
		cycles[id] = cycle
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	now := time.Now()
	seen := map[uuid.UUID]bool{}
	out := []MyQualification{}
	for _, c := range all {
		if seen[c.QualificationID] {
			continue
		}
		seen[c.QualificationID] = true
		mq := MyQualification{Certificate: c}
		if c.ExpiresAt != nil {
			from := c.ExpiresAt.Add(-RenewalWindow)
			mq.RenewableFrom = &from
		}
		if c.CourseID != nil {
			if cycle, ok := cycles[*c.CourseID]; ok {
				mq.RenewalInProgress = cycle > c.Cycle
				mq.Renewable = cycle == c.Cycle && renewalOpen(c, now)
			}
		}
		out = append(out, mq)
	}
	return out, nil
}

func renewalOpen(c Certificate, now time.Time) bool {
	if c.RevokedAt != nil {
		return true
	}
	return c.ExpiresAt != nil && !now.Before(c.ExpiresAt.Add(-RenewalWindow))
}

// RenewResult is the learner's enrolment after renewing.
type RenewResult struct {
	CourseID uuid.UUID  `json:"course_id"`
	Cycle    int        `json:"cycle"`
	DueAt    *time.Time `json:"due_at"`
}

// Renew starts a new cycle on a course for an active learner whose
// certificate from the current cycle is revoked, expired or expires within
// 60 days. Progress resets (earlier completions and attempts stay as
// history). The new due date is the certificate's expiry when that is
// still ahead, else the course's due_days from now, else none.
func (s *Service) Renew(ctx context.Context, courseID, userID uuid.UUID) (RenewResult, error) {
	res := RenewResult{CourseID: courseID}
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var enrolmentID uuid.UUID
		var cycle int
		err := tx.QueryRow(ctx, `
			SELECT id, cycle FROM enrolments
			 WHERE course_id = $1 AND user_id = $2 AND role = 'learner' AND status = 'active'
			   FOR UPDATE`, courseID, userID).Scan(&enrolmentID, &cycle)
		if errors.Is(err, pgx.ErrNoRows) {
			return httperror.New(httperror.CodeForbidden, "you are not enrolled in this course as a learner")
		}
		if err != nil {
			return err
		}
		cert, err := scanCertificate(tx.QueryRow(ctx, certificateSelect+`
			WHERE ct.course_id = $1 AND ct.user_id = $2 AND ct.cycle = $3`, courseID, userID, cycle), time.Now())
		if errors.Is(err, pgx.ErrNoRows) {
			return httperror.Conflict("there is nothing to renew yet: finish the course first")
		}
		if err != nil {
			return err
		}
		now := time.Now()
		if !renewalOpen(cert, now) {
			if cert.ExpiresAt == nil {
				return httperror.Conflict("this qualification does not expire")
			}
			return httperror.Conflict("renewal opens on " + cert.ExpiresAt.Add(-RenewalWindow).Format("2 Jan 2006"))
		}
		var keepDue *time.Time
		if cert.RevokedAt == nil && cert.ExpiresAt != nil && cert.ExpiresAt.After(now) {
			keepDue = cert.ExpiresAt
		}
		return tx.QueryRow(ctx, `
			UPDATE enrolments e
			   SET cycle = e.cycle + 1,
			       due_at = COALESCE($2, now() + make_interval(days => (SELECT c.due_days FROM courses c WHERE c.id = e.course_id)))
			 WHERE e.id = $1
			 RETURNING e.cycle, e.due_at`, enrolmentID, keepDue).Scan(&res.Cycle, &res.DueAt)
	})
	return res, err
}

// RevokeCertificate marks a certificate revoked. The row stays.
func (s *Service) RevokeCertificate(ctx context.Context, id, by uuid.UUID, reason string) (Certificate, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Certificate{}, httperror.Validation("reason is required", nil)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE certificates SET revoked_at = now(), revoked_reason = $2, revoked_by = $3
		 WHERE id = $1 AND revoked_at IS NULL`, id, reason, by)
	if err != nil {
		return Certificate{}, err
	}
	if tag.RowsAffected() == 0 {
		c, err := s.GetCertificate(ctx, id)
		if err != nil {
			return Certificate{}, err
		}
		if c.RevokedAt != nil {
			return Certificate{}, httperror.Conflict("this certificate is already revoked")
		}
	}
	return s.GetCertificate(ctx, id)
}

// VerifyCertificate answers the public verify page. Codes are matched
// case-insensitively.
func (s *Service) VerifyCertificate(ctx context.Context, code string) (PublicVerification, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" || len(code) > 16 {
		return PublicVerification{}, httperror.NotFound("certificate")
	}
	var v PublicVerification
	var revokedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT u.display_name, ql.id, ql.name, ct.issued_at, ct.expires_at, ct.revoked_at
		  FROM certificates ct
		  JOIN users u ON u.id = ct.user_id
		  JOIN qualifications ql ON ql.id = ct.qualification_id
		 WHERE ct.verify_code = $1`, code).Scan(&v.DisplayName, &v.QualificationID, &v.Qualification, &v.IssuedAt, &v.ExpiresAt, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, httperror.NotFound("certificate")
	}
	v.Status = certificateStatus(v.ExpiresAt, revokedAt, time.Now())
	return v, err
}

// Holdings returns the valid (not expired, not revoked) qualification keys
// of each OIDC subject under issuer. Every requested subject is present,
// with an empty list when they hold nothing. A key held through several
// certificates reports the latest expiry (nil = never expires).
func (s *Service) Holdings(ctx context.Context, issuer string, subjects []string) (map[string][]Holding, error) {
	out := make(map[string][]Holding, len(subjects))
	for _, sub := range subjects {
		out[sub] = []Holding{}
	}
	if len(subjects) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT u.subject, ql.key,
		       CASE WHEN bool_or(ct.expires_at IS NULL) THEN NULL ELSE max(ct.expires_at) END
		  FROM certificates ct
		  JOIN users u ON u.id = ct.user_id
		  JOIN qualifications ql ON ql.id = ct.qualification_id
		 WHERE u.issuer = $1 AND u.subject = ANY($2)
		   AND ct.revoked_at IS NULL AND (ct.expires_at IS NULL OR ct.expires_at > now())
		 GROUP BY u.subject, ql.key
		 ORDER BY u.subject, ql.key`, issuer, subjects)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sub string
		var h Holding
		if err := rows.Scan(&sub, &h.Key, &h.ExpiresAt); err != nil {
			return nil, err
		}
		out[sub] = append(out[sub], h)
	}
	return out, rows.Err()
}
