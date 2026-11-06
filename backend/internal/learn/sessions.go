package learn

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// Signup statuses.
const (
	SignupBooked   = "booked"
	SignupWaitlist = "waitlist"
	SignupAttended = "attended"
	SignupNoShow   = "no_show"
)

const (
	// checkinStep is how long one check-in code is shown; the previous
	// code stays valid for one more step so a scan just before the QR
	// rotates still works.
	checkinStep = 30 * time.Second
	// checkinOpensBefore is how early before the start people can check in.
	checkinOpensBefore = 30 * time.Minute
)

// SessionSlot is one time slot of a session activity. Booked counts booked
// and attended seats; SeatsLeft never goes below zero.
type SessionSlot struct {
	ID         uuid.UUID `json:"id"`
	ActivityID uuid.UUID `json:"activity_id"`
	StartsAt   time.Time `json:"starts_at"`
	EndsAt     time.Time `json:"ends_at"`
	Location   string    `json:"location"`
	Capacity   int       `json:"capacity"`
	Booked     int       `json:"booked"`
	Waitlist   int       `json:"waitlist"`
	SeatsLeft  int       `json:"seats_left"`
}

// SessionSignup is one person on one slot.
type SessionSignup struct {
	ID         uuid.UUID `json:"id"`
	SlotID     uuid.UUID `json:"slot_id"`
	ActivityID uuid.UUID `json:"activity_id"`
	UserID     uuid.UUID `json:"user_id"`
	Status     string    `json:"status"`
	WalkIn     bool      `json:"walk_in"`
	CreatedAt  time.Time `json:"created_at"`
	// WaitlistPosition is 1-based, set only while on the waitlist.
	WaitlistPosition *int `json:"waitlist_position,omitempty"`
}

// SessionInfo is a session as one person sees it: its slots and their own
// signup (nil when they have none).
type SessionInfo struct {
	ActivityID uuid.UUID      `json:"activity_id"`
	Title      string         `json:"title"`
	Body       string         `json:"body"`
	Slots      []SessionSlot  `json:"slots"`
	MySignup   *SessionSignup `json:"my_signup"`
}

// RosterEntry is a signup with the person's name for the staff roster.
type RosterEntry struct {
	SessionSignup
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

// Roster is everyone signed up to one slot.
type Roster struct {
	Slot          SessionSlot   `json:"slot"`
	CourseID      uuid.UUID     `json:"course_id"`
	ActivityTitle string        `json:"activity_title"`
	Signups       []RosterEntry `json:"signups"`
}

// CheckinCode is the code currently shown on the venue QR.
type CheckinCode struct {
	Code          string    `json:"code"`
	ExpiresAt     time.Time `json:"expires_at"`
	URL           string    `json:"url"`
	WindowSeconds int       `json:"window_seconds"`
}

// CheckinResult is what the learner sees after scanning.
type CheckinResult struct {
	Signup          SessionSignup `json:"signup"`
	Slot            SessionSlot   `json:"slot"`
	CourseID        uuid.UUID     `json:"course_id"`
	ActivityTitle   string        `json:"activity_title"`
	AlreadyAttended bool          `json:"already_attended"`
}

// ----------------------------------------------------------------------------
// Check-in codes
// ----------------------------------------------------------------------------

func checkinWindow(t time.Time) int64 { return t.Unix() / int64(checkinStep/time.Second) }

// checkinCodeFor is the truncated HMAC-SHA256 of slot id + time window,
// base32 so it is easy to read out loud or type.
func checkinCodeFor(secret []byte, slotID uuid.UUID, window int64) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(slotID.String()))
	m.Write([]byte(strconv.FormatInt(window, 10)))
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(m.Sum(nil)[:5])
}

// validCheckinCode accepts the code of the current and the previous window.
func validCheckinCode(secret []byte, slotID uuid.UUID, code string, now time.Time) bool {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return false
	}
	w := checkinWindow(now)
	for _, cand := range []int64{w, w - 1} {
		if hmac.Equal([]byte(checkinCodeFor(secret, slotID, cand)), []byte(code)) {
			return true
		}
	}
	return false
}

// checkinOpen reports whether now is inside the check-in window of a slot:
// from 30 minutes before the start until the end.
func checkinOpen(startsAt, endsAt, now time.Time) bool {
	return !now.Before(startsAt.Add(-checkinOpensBefore)) && !now.After(endsAt)
}

// ----------------------------------------------------------------------------
// Lookups
// ----------------------------------------------------------------------------

// SessionActivity loads an activity and checks it is a session.
func (s *Service) SessionActivity(ctx context.Context, id uuid.UUID) (Activity, error) {
	a, err := scanActivity(s.pool.QueryRow(ctx, `SELECT `+activityCols+` FROM activities WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && a.Kind != domain.ActivitySession) {
		return Activity{}, httperror.NotFound("session")
	}
	return a, err
}

// SessionSlotCourse resolves a session slot's course for authorization.
func (s *Service) SessionSlotCourse(ctx context.Context, slotID uuid.UUID) (Course, error) {
	c, err := scanCourse(s.pool.QueryRow(ctx, `
		SELECT `+prefixedCourseCols("c")+`
		  FROM courses c
		  JOIN sections s ON s.course_id = c.id
		  JOIN activities a ON a.section_id = s.id
		  JOIN session_slots sl ON sl.activity_id = a.id
		 WHERE sl.id = $1`, slotID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Course{}, httperror.NotFound("session slot")
	}
	return c, err
}

// SignupCourse resolves a signup's course for authorization.
func (s *Service) SignupCourse(ctx context.Context, signupID uuid.UUID) (Course, error) {
	c, err := scanCourse(s.pool.QueryRow(ctx, `
		SELECT `+prefixedCourseCols("c")+`
		  FROM courses c
		  JOIN sections s ON s.course_id = c.id
		  JOIN activities a ON a.section_id = s.id
		  JOIN session_signups su ON su.activity_id = a.id
		 WHERE su.id = $1`, signupID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Course{}, httperror.NotFound("signup")
	}
	return c, err
}

// SlotActivityID returns the session activity a slot belongs to.
func (s *Service) SlotActivityID(ctx context.Context, slotID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT activity_id FROM session_slots WHERE id = $1`, slotID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, httperror.NotFound("session slot")
	}
	return id, err
}

type slotQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const slotSelect = `
	SELECT sl.id, sl.activity_id, sl.starts_at, sl.ends_at, sl.location, sl.capacity,
	       count(su.id) FILTER (WHERE su.status IN ('booked', 'attended')),
	       count(su.id) FILTER (WHERE su.status = 'waitlist')
	  FROM session_slots sl
	  LEFT JOIN session_signups su ON su.slot_id = sl.id`

func scanSlot(row pgx.Row) (SessionSlot, error) {
	var sl SessionSlot
	err := row.Scan(&sl.ID, &sl.ActivityID, &sl.StartsAt, &sl.EndsAt, &sl.Location, &sl.Capacity, &sl.Booked, &sl.Waitlist)
	sl.SeatsLeft = max(sl.Capacity-sl.Booked, 0)
	return sl, err
}

func getSessionSlot(ctx context.Context, q slotQuerier, slotID uuid.UUID) (SessionSlot, error) {
	sl, err := scanSlot(q.QueryRow(ctx, slotSelect+` WHERE sl.id = $1 GROUP BY sl.id`, slotID))
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionSlot{}, httperror.NotFound("session slot")
	}
	return sl, err
}

// ListSessionSlots returns a session's slots in time order with seat counts.
func (s *Service) ListSessionSlots(ctx context.Context, activityID uuid.UUID) ([]SessionSlot, error) {
	rows, err := s.pool.Query(ctx, slotSelect+`
		 WHERE sl.activity_id = $1
		 GROUP BY sl.id
		 ORDER BY sl.starts_at, sl.id`, activityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionSlot{}
	for rows.Next() {
		sl, err := scanSlot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sl)
	}
	return out, rows.Err()
}

const signupCols = `id, slot_id, activity_id, user_id, status, walk_in, created_at`

func scanSignup(row pgx.Row) (SessionSignup, error) {
	var su SessionSignup
	err := row.Scan(&su.ID, &su.SlotID, &su.ActivityID, &su.UserID, &su.Status, &su.WalkIn, &su.CreatedAt)
	return su, err
}

// withWaitlistPosition fills WaitlistPosition for a waitlisted signup.
func withWaitlistPosition(ctx context.Context, q slotQuerier, su SessionSignup) (SessionSignup, error) {
	if su.Status != SignupWaitlist {
		su.WaitlistPosition = nil
		return su, nil
	}
	var pos int
	if err := q.QueryRow(ctx, `
		SELECT count(*) + 1 FROM session_signups
		 WHERE slot_id = $1 AND status = 'waitlist' AND (created_at, id) < ($2, $3)`,
		su.SlotID, su.CreatedAt, su.ID).Scan(&pos); err != nil {
		return su, err
	}
	su.WaitlistPosition = &pos
	return su, nil
}

// SessionInfo assembles a session for one person: slots plus their signup.
func (s *Service) SessionInfo(ctx context.Context, activityID, userID uuid.UUID) (SessionInfo, error) {
	a, err := s.SessionActivity(ctx, activityID)
	if err != nil {
		return SessionInfo{}, err
	}
	info := SessionInfo{ActivityID: a.ID, Title: a.Title, Body: a.Body}
	if info.Slots, err = s.ListSessionSlots(ctx, activityID); err != nil {
		return info, err
	}
	su, err := scanSignup(s.pool.QueryRow(ctx, `
		SELECT `+signupCols+` FROM session_signups WHERE activity_id = $1 AND user_id = $2`, activityID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return info, nil
	}
	if err != nil {
		return info, err
	}
	if stale, err := staleAttendance(ctx, s.pool, su); err != nil || stale {
		return info, err
	}
	if su, err = withWaitlistPosition(ctx, s.pool, su); err != nil {
		return info, err
	}
	info.MySignup = &su
	return info, nil
}

// staleAttendance: the person attended in an earlier cycle (before
// renewing), which doesn't count now; they book and attend again.
func staleAttendance(ctx context.Context, q dbtx, su SessionSignup) (bool, error) {
	if su.Status != SignupAttended {
		return false, nil
	}
	done, err := completedInCurrentCycle(ctx, q, su.ActivityID, su.UserID)
	return !done, err
}

// ----------------------------------------------------------------------------
// Slot authoring (staff)
// ----------------------------------------------------------------------------

// SessionSlotInput is the create payload.
type SessionSlotInput struct {
	StartsAt time.Time
	EndsAt   time.Time
	Location string
	Capacity int
}

func validateSlotTimes(start, end time.Time, capacity int) error {
	if start.IsZero() || end.IsZero() {
		return httperror.Validation("starts_at and ends_at are required", nil)
	}
	if !end.After(start) {
		return httperror.Validation("the session must end after it starts", nil)
	}
	if capacity <= 0 {
		return httperror.Validation("capacity must be at least 1", nil)
	}
	return nil
}

// CreateSessionSlot adds a slot to a session.
func (s *Service) CreateSessionSlot(ctx context.Context, activityID uuid.UUID, in SessionSlotInput) (SessionSlot, error) {
	if _, err := s.SessionActivity(ctx, activityID); err != nil {
		return SessionSlot{}, err
	}
	if err := validateSlotTimes(in.StartsAt, in.EndsAt, in.Capacity); err != nil {
		return SessionSlot{}, err
	}
	var id uuid.UUID
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO session_slots (activity_id, starts_at, ends_at, location, capacity)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		activityID, in.StartsAt, in.EndsAt, strings.TrimSpace(in.Location), in.Capacity).Scan(&id); err != nil {
		return SessionSlot{}, err
	}
	return getSessionSlot(ctx, s.pool, id)
}

// UpdateSessionSlotInput carries optional slot fields; nil = unchanged.
type UpdateSessionSlotInput struct {
	StartsAt *time.Time
	EndsAt   *time.Time
	Location *string
	Capacity *int
}

// UpdateSessionSlot edits a slot. Raising the capacity promotes people from
// the waitlist; lowering it never bumps anyone already booked.
func (s *Service) UpdateSessionSlot(ctx context.Context, slotID uuid.UUID, in UpdateSessionSlotInput) (SessionSlot, error) {
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		cur, err := lockSlot(ctx, tx, slotID)
		if err != nil {
			return err
		}
		start, end, capacity := cur.startsAt, cur.endsAt, cur.capacity
		if in.StartsAt != nil {
			start = *in.StartsAt
		}
		if in.EndsAt != nil {
			end = *in.EndsAt
		}
		if in.Capacity != nil {
			capacity = *in.Capacity
		}
		if err := validateSlotTimes(start, end, capacity); err != nil {
			return err
		}
		var location *string
		if in.Location != nil {
			l := strings.TrimSpace(*in.Location)
			location = &l
		}
		if _, err := tx.Exec(ctx, `
			UPDATE session_slots
			   SET starts_at = $2, ends_at = $3, capacity = $4, location = COALESCE($5, location), updated_at = now()
			 WHERE id = $1`, slotID, start, end, capacity, location); err != nil {
			return err
		}
		cur.capacity = capacity
		_, err = promoteWaitlist(ctx, tx, cur)
		return err
	})
	if err != nil {
		return SessionSlot{}, err
	}
	return getSessionSlot(ctx, s.pool, slotID)
}

// DeleteSessionSlot removes a slot and its signups. Completions earned by
// attending are kept.
func (s *Service) DeleteSessionSlot(ctx context.Context, slotID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM session_slots WHERE id = $1`, slotID)
	if err == nil && tag.RowsAffected() == 0 {
		return httperror.NotFound("session slot")
	}
	return err
}

// ----------------------------------------------------------------------------
// Booking
// ----------------------------------------------------------------------------

type lockedSlot struct {
	id, activityID   uuid.UUID
	startsAt, endsAt time.Time
	capacity         int
	secret           []byte
}

// lockSlot loads a slot FOR UPDATE: every seat-changing operation on a slot
// is serialized on this row lock.
func lockSlot(ctx context.Context, tx pgx.Tx, slotID uuid.UUID) (lockedSlot, error) {
	var sl lockedSlot
	err := tx.QueryRow(ctx, `
		SELECT id, activity_id, starts_at, ends_at, capacity, checkin_secret
		  FROM session_slots WHERE id = $1 FOR UPDATE`, slotID,
	).Scan(&sl.id, &sl.activityID, &sl.startsAt, &sl.endsAt, &sl.capacity, &sl.secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return sl, httperror.NotFound("session slot")
	}
	return sl, err
}

func seatsTaken(ctx context.Context, tx pgx.Tx, slotID uuid.UUID) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `
		SELECT count(*) FROM session_signups WHERE slot_id = $1 AND status IN ('booked', 'attended')`,
		slotID).Scan(&n)
	return n, err
}

// promoteWaitlist books the first people on the waitlist into free seats.
// The caller holds the slot lock.
func promoteWaitlist(ctx context.Context, tx pgx.Tx, sl lockedSlot) (int64, error) {
	taken, err := seatsTaken(ctx, tx, sl.id)
	if err != nil {
		return 0, err
	}
	free := sl.capacity - taken
	if free <= 0 {
		return 0, nil
	}
	tag, err := tx.Exec(ctx, `
		UPDATE session_signups SET status = 'booked', updated_at = now()
		 WHERE id IN (SELECT id FROM session_signups
		               WHERE slot_id = $1 AND status = 'waitlist'
		               ORDER BY created_at, id LIMIT $2)`, sl.id, free)
	return tag.RowsAffected(), err
}

// BookSlot books the caller on a slot, or puts them on its waitlist when it
// is full. Booking again on the same slot returns the existing signup
// (created=false). One signup per session: a booking on another time must
// be cancelled first; a no-show can rebook a later time.
func (s *Service) BookSlot(ctx context.Context, slotID, userID uuid.UUID) (SessionSignup, bool, error) {
	var out SessionSignup
	created := false
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		sl, err := lockSlot(ctx, tx, slotID)
		if err != nil {
			return err
		}
		if !time.Now().Before(sl.startsAt) {
			return httperror.Conflict("this session has already started; check in at the venue instead")
		}
		existing, err := scanSignup(tx.QueryRow(ctx, `
			SELECT `+signupCols+` FROM session_signups WHERE activity_id = $1 AND user_id = $2 FOR UPDATE`,
			sl.activityID, userID))
		hasExisting := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if hasExisting {
			stale, err := staleAttendance(ctx, tx, existing)
			if err != nil {
				return err
			}
			rebookable := existing.Status == SignupNoShow || stale
			switch {
			case existing.Status == SignupAttended && !stale:
				return httperror.Conflict("you have already attended this session")
			case existing.SlotID == sl.id && !rebookable:
				out = existing
				return nil
			case !rebookable:
				return httperror.Conflict("you are already booked on another time for this session; cancel that booking first")
			}
		}
		taken, err := seatsTaken(ctx, tx, sl.id)
		if err != nil {
			return err
		}
		status := SignupBooked
		if taken >= sl.capacity {
			status = SignupWaitlist
		}
		if hasExisting {
			out, err = scanSignup(tx.QueryRow(ctx, `
				UPDATE session_signups
				   SET slot_id = $2, status = $3, walk_in = FALSE, created_at = now(), updated_at = now()
				 WHERE id = $1
				 RETURNING `+signupCols, existing.ID, sl.id, status))
		} else {
			out, err = scanSignup(tx.QueryRow(ctx, `
				INSERT INTO session_signups (slot_id, activity_id, user_id, status)
				VALUES ($1, $2, $3, $4)
				RETURNING `+signupCols, sl.id, sl.activityID, userID, status))
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return httperror.Conflict("you are already booked on another time for this session")
		}
		if err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return SessionSignup{}, false, err
	}
	out, err = withWaitlistPosition(ctx, s.pool, out)
	return out, created, err
}

// CancelBooking removes the caller's booking or waitlist place on a slot
// and promotes the first person waiting. Not possible once the session has
// started, or once attendance has been recorded.
func (s *Service) CancelBooking(ctx context.Context, slotID, userID uuid.UUID) error {
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		sl, err := lockSlot(ctx, tx, slotID)
		if err != nil {
			return err
		}
		var id uuid.UUID
		var status string
		err = tx.QueryRow(ctx, `
			SELECT id, status FROM session_signups WHERE slot_id = $1 AND user_id = $2`,
			slotID, userID).Scan(&id, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return httperror.NotFound("booking")
		}
		if err != nil {
			return err
		}
		if status == SignupAttended || status == SignupNoShow {
			return httperror.Conflict("attendance has already been recorded for this booking")
		}
		if !time.Now().Before(sl.startsAt) {
			return httperror.Conflict("bookings can't be cancelled after the session has started")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM session_signups WHERE id = $1`, id); err != nil {
			return err
		}
		if status == SignupBooked {
			_, err = promoteWaitlist(ctx, tx, sl)
		}
		return err
	})
}

// ----------------------------------------------------------------------------
// Check-in
// ----------------------------------------------------------------------------

// CurrentCheckinCode returns the code staff show on the venue QR, and the
// learner URL it encodes (relative when publicBaseURL is empty).
func (s *Service) CurrentCheckinCode(ctx context.Context, slotID uuid.UUID, publicBaseURL string) (CheckinCode, error) {
	var secret []byte
	err := s.pool.QueryRow(ctx, `SELECT checkin_secret FROM session_slots WHERE id = $1`, slotID).Scan(&secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return CheckinCode{}, httperror.NotFound("session slot")
	}
	if err != nil {
		return CheckinCode{}, err
	}
	now := time.Now()
	w := checkinWindow(now)
	code := checkinCodeFor(secret, slotID, w)
	step := int64(checkinStep / time.Second)
	return CheckinCode{
		Code:          code,
		ExpiresAt:     time.Unix((w+1)*step, 0).UTC(),
		URL:           strings.TrimRight(publicBaseURL, "/") + "/checkin/" + slotID.String() + "?code=" + url.QueryEscape(code),
		WindowSeconds: int(step),
	}, nil
}

// CheckIn marks the caller attended on a slot after a valid QR scan. A
// booked (or no-show) person is marked attended; anyone else enrolled is a
// walk-in while seats are left, and a booking on another time of the same
// session moves here. Attending completes the activity.
func (s *Service) CheckIn(ctx context.Context, slotID, userID uuid.UUID, code string) (CheckinResult, error) {
	var res CheckinResult
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		sl, err := lockSlot(ctx, tx, slotID)
		if err != nil {
			return err
		}
		now := time.Now()
		if !validCheckinCode(sl.secret, sl.id, code, now) {
			return httperror.BadRequest("this check-in code is not valid any more; scan the QR code again")
		}
		if !checkinOpen(sl.startsAt, sl.endsAt, now) {
			return httperror.Conflict("check-in opens 30 minutes before the session starts and closes when it ends")
		}
		existing, err := scanSignup(tx.QueryRow(ctx, `
			SELECT `+signupCols+` FROM session_signups WHERE activity_id = $1 AND user_id = $2 FOR UPDATE`,
			sl.activityID, userID))
		hasExisting := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		needSeat := func() error {
			taken, err := seatsTaken(ctx, tx, sl.id)
			if err != nil {
				return err
			}
			if taken >= sl.capacity {
				return httperror.Conflict("this session is full; ask the trainer at the door")
			}
			return nil
		}

		stale := false
		if hasExisting {
			if stale, err = staleAttendance(ctx, tx, existing); err != nil {
				return err
			}
		}

		switch {
		case hasExisting && existing.Status == SignupAttended && !stale:
			res.Signup = existing
			res.AlreadyAttended = true
		case hasExisting && existing.SlotID == sl.id:
			if existing.Status == SignupWaitlist {
				if err := needSeat(); err != nil {
					return err
				}
			}
			if res.Signup, err = scanSignup(tx.QueryRow(ctx, `
				UPDATE session_signups SET status = 'attended', updated_at = now()
				 WHERE id = $1 RETURNING `+signupCols, existing.ID)); err != nil {
				return err
			}
		case hasExisting:
			// Booked on another time but came to this one.
			if err := needSeat(); err != nil {
				return err
			}
			if res.Signup, err = scanSignup(tx.QueryRow(ctx, `
				UPDATE session_signups SET slot_id = $2, status = 'attended', walk_in = TRUE, updated_at = now()
				 WHERE id = $1 RETURNING `+signupCols, existing.ID, sl.id)); err != nil {
				return err
			}
			if existing.Status == SignupBooked {
				old, err := lockSlot(ctx, tx, existing.SlotID)
				if err != nil {
					return err
				}
				if _, err := promoteWaitlist(ctx, tx, old); err != nil {
					return err
				}
			}
		default:
			if err := needSeat(); err != nil {
				return err
			}
			if res.Signup, err = scanSignup(tx.QueryRow(ctx, `
				INSERT INTO session_signups (slot_id, activity_id, user_id, status, walk_in)
				VALUES ($1, $2, $3, 'attended', TRUE)
				RETURNING `+signupCols, sl.id, sl.activityID, userID)); err != nil {
				return err
			}
		}
		return completeActivity(ctx, tx, sl.activityID, userID)
	})
	if err != nil {
		return CheckinResult{}, err
	}
	if res.Slot, err = getSessionSlot(ctx, s.pool, res.Signup.SlotID); err != nil {
		return res, err
	}
	err = s.pool.QueryRow(ctx, `
		SELECT s.course_id, a.title FROM activities a JOIN sections s ON s.id = a.section_id WHERE a.id = $1`,
		res.Signup.ActivityID).Scan(&res.CourseID, &res.ActivityTitle)
	return res, err
}

// ----------------------------------------------------------------------------
// Roster (staff)
// ----------------------------------------------------------------------------

// Roster lists a slot's signups: booked, attended and no-shows by name,
// then the waitlist in order.
func (s *Service) Roster(ctx context.Context, slotID uuid.UUID) (Roster, error) {
	var r Roster
	var err error
	if r.Slot, err = getSessionSlot(ctx, s.pool, slotID); err != nil {
		return r, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT s.course_id, a.title FROM activities a JOIN sections s ON s.id = a.section_id WHERE a.id = $1`,
		r.Slot.ActivityID).Scan(&r.CourseID, &r.ActivityTitle); err != nil {
		return r, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT su.id, su.slot_id, su.activity_id, su.user_id, su.status, su.walk_in, su.created_at,
		       u.display_name, COALESCE(u.email, '')
		  FROM session_signups su JOIN users u ON u.id = su.user_id
		 WHERE su.slot_id = $1
		 ORDER BY (su.status = 'waitlist'),
		          CASE WHEN su.status = 'waitlist' THEN su.created_at END,
		          u.display_name, su.id`, slotID)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	r.Signups = []RosterEntry{}
	pos := 0
	for rows.Next() {
		var e RosterEntry
		if err := rows.Scan(&e.ID, &e.SlotID, &e.ActivityID, &e.UserID, &e.Status, &e.WalkIn, &e.CreatedAt,
			&e.DisplayName, &e.Email); err != nil {
			return r, err
		}
		if e.Status == SignupWaitlist {
			pos++
			p := pos
			e.WaitlistPosition = &p
		}
		r.Signups = append(r.Signups, e)
	}
	return r, rows.Err()
}

// SetSignupStatus records attendance by hand. Attended completes the
// activity; booked or no-show removes that completion again.
func (s *Service) SetSignupStatus(ctx context.Context, signupID uuid.UUID, status string) (SessionSignup, error) {
	switch status {
	case SignupAttended, SignupNoShow, SignupBooked:
	default:
		return SessionSignup{}, httperror.Validation("status must be attended, no_show or booked", nil)
	}
	var out SessionSignup
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var slotID uuid.UUID
		err := tx.QueryRow(ctx, `SELECT slot_id FROM session_signups WHERE id = $1`, signupID).Scan(&slotID)
		if errors.Is(err, pgx.ErrNoRows) {
			return httperror.NotFound("signup")
		}
		if err != nil {
			return err
		}
		if _, err := lockSlot(ctx, tx, slotID); err != nil {
			return err
		}
		if out, err = scanSignup(tx.QueryRow(ctx, `
			UPDATE session_signups SET status = $2, updated_at = now()
			 WHERE id = $1 RETURNING `+signupCols, signupID, status)); err != nil {
			return err
		}
		if status == SignupAttended {
			return completeActivity(ctx, tx, out.ActivityID, out.UserID)
		}
		return uncompleteActivity(ctx, tx, out.ActivityID, out.UserID)
	})
	return out, err
}
