package learn

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/learn/backend/internal/notify"
)

// Reminder kinds (reminders_sent.kind and notify.Notice.Kind).
const (
	ReminderDue7d           = "due_7d"
	ReminderDue1d           = "due_1d"
	ReminderOverdue         = "overdue"
	ReminderSessionTomorrow = "session_tomorrow"
	ReminderCertExpiring30d = "certificate_expiring_30d"
	ReminderCertExpired     = "certificate_expired"
	reminderInterval        = time.Hour
	reminderStartDelay      = 20 * time.Second
	reminderLockKey         = "learn-reminders"
)

// reminderNamespace derives dedupe subject ids that aren't a single row id
// (an enrolment's due date in one cycle).
var reminderNamespace = uuid.MustParse("6f0c3f5e-2a7b-4c1e-9a43-5d1f0b8e7c21")

// ReminderResult counts what one run did.
type ReminderResult struct {
	// Ran is false when another replica holds the reminder lock.
	Ran bool `json:"ran"`
	// Channels lists the delivery channels; empty = notifications are off.
	Channels    []string       `json:"channels"`
	Sent        map[string]int `json:"sent"`
	AlreadySent int            `json:"already_sent"`
	Failed      int            `json:"failed"`
}

// Reminders sends due-date, session and certificate reminders through the
// notifier, each at most once per person and subject (reminders_sent).
type Reminders struct {
	svc      *Service
	notifier *notify.Notifier
	baseURL  string
	orgName  func(context.Context) string
	log      zerolog.Logger
}

// NewReminders builds the reminder sender. baseURL is the SPA origin for
// links (PUBLIC_BASE_URL); orgName returns the organisation name for the
// texts ("" falls back to a neutral wording).
func NewReminders(svc *Service, notifier *notify.Notifier, baseURL string, orgName func(context.Context) string, logger zerolog.Logger) *Reminders {
	if orgName == nil {
		orgName = func(context.Context) string { return "" }
	}
	return &Reminders{
		svc: svc, notifier: notifier, baseURL: strings.TrimRight(baseURL, "/"), orgName: orgName,
		log: logger.With().Str("component", "reminders").Logger(),
	}
}

// Run sends reminders shortly after start and then every hour until ctx
// ends. Each reminder is deduped, so the hourly cadence never repeats one.
func (r *Reminders) Run(ctx context.Context) {
	if r == nil || len(r.notifier.Channels()) == 0 {
		return
	}
	t := time.NewTimer(reminderStartDelay)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			res, err := r.RunOnce(ctx)
			switch {
			case err != nil:
				r.log.Error().Err(err).Msg("reminder run failed")
			case res.Ran:
				r.log.Info().Interface("sent", res.Sent).Int("already_sent", res.AlreadySent).
					Int("failed", res.Failed).Msg("reminder run done")
			}
			t.Reset(reminderInterval)
		}
	}
}

type reminder struct {
	kind      string
	subjectID uuid.UUID
	userID    uuid.UUID
	email     string
	name      string
	subject   string
	body      string
	url       string
}

// RunOnce sends every reminder that is due now and not sent before.
func (r *Reminders) RunOnce(ctx context.Context) (ReminderResult, error) {
	res := ReminderResult{Channels: r.notifier.Channels(), Sent: map[string]int{}}
	if res.Channels == nil {
		res.Channels = []string{}
	}
	if len(res.Channels) == 0 {
		return res, nil
	}
	conn, err := r.svc.pool.Acquire(ctx)
	if err != nil {
		return res, err
	}
	defer conn.Release()
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, reminderLockKey).Scan(&got); err != nil {
		return res, err
	}
	if !got {
		return res, nil
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, reminderLockKey)
	}()
	res.Ran = true

	now := time.Now()
	org := strings.TrimSpace(r.orgName(ctx))
	var all []reminder
	for _, collect := range []func(context.Context, time.Time, string) ([]reminder, error){
		r.dueReminders, r.sessionReminders, r.certificateReminders,
	} {
		list, err := collect(ctx, now, org)
		if err != nil {
			return res, err
		}
		all = append(all, list...)
	}
	for _, rem := range all {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		sent, claimed, err := r.deliver(ctx, rem)
		switch {
		case err != nil:
			return res, err
		case !claimed:
			res.AlreadySent++
		case sent:
			res.Sent[rem.kind]++
		default:
			res.Failed++
		}
	}
	return res, nil
}

// deliver claims the reminder in reminders_sent, sends it, and releases
// the claim again when no channel delivered it (so the next run retries).
func (r *Reminders) deliver(ctx context.Context, rem reminder) (sent, claimed bool, err error) {
	tag, err := r.svc.pool.Exec(ctx, `
		INSERT INTO reminders_sent (kind, subject_id, user_id) VALUES ($1, $2, $3)
		ON CONFLICT (kind, subject_id, user_id) DO NOTHING`, rem.kind, rem.subjectID, rem.userID)
	if err != nil || tag.RowsAffected() == 0 {
		return false, false, err
	}
	recipient := notify.Recipient{UserID: rem.userID, Email: rem.email, DisplayName: rem.name, Channels: map[string]string{}}
	rows, err := r.svc.pool.Query(ctx, `SELECT channel, address FROM user_channels WHERE user_id = $1`, rem.userID)
	if err != nil {
		return false, true, err
	}
	for rows.Next() {
		var ch, addr string
		if err := rows.Scan(&ch, &addr); err != nil {
			rows.Close()
			return false, true, err
		}
		recipient.Channels[ch] = addr
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, true, err
	}
	n := r.notifier.Notify(ctx, notify.Notice{Subject: rem.subject, Body: rem.body, URL: rem.url, Kind: rem.kind}, recipient)
	if n > 0 {
		return true, true, nil
	}
	_, err = r.svc.pool.Exec(ctx, `
		DELETE FROM reminders_sent WHERE kind = $1 AND subject_id = $2 AND user_id = $3`,
		rem.kind, rem.subjectID, rem.userID)
	return false, true, err
}

func (r *Reminders) link(path string) string {
	return r.baseURL + path
}

func signature(org string) string {
	if org == "" {
		return "\n\n— Learn"
	}
	return "\n\n— " + org + " · Learn"
}

func formatDay(t time.Time) string     { return t.Local().Format("Mon 2 Jan 2006") }
func formatDayTime(t time.Time) string { return t.Local().Format("Mon 2 Jan 2006, 15:04 MST") }

// dueReminders: active learner enrolments on published courses with a due
// date and activities left in the current cycle. One reminder 7 days
// before, one the day before, and one once overdue. Changing the due date
// or renewing (new cycle) makes them due again.
func (r *Reminders) dueReminders(ctx context.Context, now time.Time, org string) ([]reminder, error) {
	rows, err := r.svc.pool.Query(ctx, `
		SELECT e.id, e.cycle, e.due_at, u.id, u.email, u.display_name, c.id, c.title,
		       (SELECT count(*) FROM activities a JOIN sections s ON s.id = a.section_id WHERE s.course_id = c.id),
		       (SELECT count(*) FROM completions cp
		          JOIN activities a ON a.id = cp.activity_id JOIN sections s ON s.id = a.section_id
		         WHERE s.course_id = c.id AND cp.user_id = e.user_id AND cp.cycle = e.cycle)
		  FROM enrolments e
		  JOIN users u ON u.id = e.user_id
		  JOIN courses c ON c.id = e.course_id
		 WHERE e.role = 'learner' AND e.status = 'active' AND c.published
		   AND e.due_at IS NOT NULL AND e.due_at <= $1::timestamptz + interval '7 days'
		   AND COALESCE(u.email, '') <> ''
		 ORDER BY e.due_at`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []reminder{}
	for rows.Next() {
		var enrolmentID, userID, courseID uuid.UUID
		var cycle, total, done int
		var dueAt time.Time
		var email, name, title string
		if err := rows.Scan(&enrolmentID, &cycle, &dueAt, &userID, &email, &name, &courseID, &title, &total, &done); err != nil {
			return nil, err
		}
		if total == 0 || done >= total {
			continue
		}
		rem := reminder{
			userID: userID, email: email, name: name, url: r.link("/courses/" + courseID.String()),
			subjectID: uuid.NewSHA1(reminderNamespace,
				[]byte(enrolmentID.String()+"/"+strconv.Itoa(cycle)+"/"+strconv.FormatInt(dueAt.Unix(), 10))),
		}
		left := fmt.Sprintf("You have finished %d of %d activities.", done, total)
		switch {
		case !dueAt.After(now):
			rem.kind = ReminderOverdue
			rem.subject = fmt.Sprintf("Overdue: %s", title)
			rem.body = fmt.Sprintf("Your course \"%s\" was due on %s and isn't finished yet. %s Please complete it as soon as you can.",
				title, formatDay(dueAt), left)
		case dueAt.Sub(now) <= 24*time.Hour:
			rem.kind = ReminderDue1d
			rem.subject = fmt.Sprintf("Due tomorrow: %s", title)
			rem.body = fmt.Sprintf("Your course \"%s\" is due on %s. %s", title, formatDayTime(dueAt), left)
		default:
			rem.kind = ReminderDue7d
			rem.subject = fmt.Sprintf("Due within a week: %s", title)
			rem.body = fmt.Sprintf("Your course \"%s\" is due on %s. %s", title, formatDay(dueAt), left)
		}
		rem.body += signature(org)
		out = append(out, rem)
	}
	return out, rows.Err()
}

// sessionReminders: booked seats on sessions starting within 24 hours.
func (r *Reminders) sessionReminders(ctx context.Context, now time.Time, org string) ([]reminder, error) {
	rows, err := r.svc.pool.Query(ctx, `
		SELECT sl.id, sl.starts_at, sl.ends_at, sl.location, a.title, c.id, c.title, u.id, u.email, u.display_name
		  FROM session_signups su
		  JOIN session_slots sl ON sl.id = su.slot_id
		  JOIN activities a ON a.id = su.activity_id
		  JOIN sections s ON s.id = a.section_id
		  JOIN courses c ON c.id = s.course_id
		  JOIN enrolments e ON e.course_id = c.id AND e.user_id = su.user_id AND e.status = 'active'
		  JOIN users u ON u.id = su.user_id
		 WHERE su.status = 'booked' AND c.published
		   AND sl.starts_at > $1::timestamptz AND sl.starts_at <= $1::timestamptz + interval '24 hours'
		   AND COALESCE(u.email, '') <> ''
		 ORDER BY sl.starts_at`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []reminder{}
	for rows.Next() {
		var slotID, courseID, userID uuid.UUID
		var startsAt, endsAt time.Time
		var location, activity, course, email, name string
		if err := rows.Scan(&slotID, &startsAt, &endsAt, &location, &activity, &courseID, &course, &userID, &email, &name); err != nil {
			return nil, err
		}
		where := ""
		if strings.TrimSpace(location) != "" {
			where = "\nWhere: " + location
		}
		out = append(out, reminder{
			kind: ReminderSessionTomorrow, subjectID: slotID, userID: userID, email: email, name: name,
			url:     r.link("/courses/" + courseID.String()),
			subject: fmt.Sprintf("Tomorrow: %s", activity),
			body: fmt.Sprintf("You're booked on \"%s\" (course \"%s\").\nWhen: %s – %s%s\n\nCan't make it? Cancel your booking in Learn so someone on the waitlist gets your seat.",
				activity, course, formatDayTime(startsAt), endsAt.Local().Format("15:04"), where) + signature(org),
		})
	}
	return out, rows.Err()
}

// certificateReminders: the latest unrevoked certificate per person and
// qualification, 30 days before it expires and once when it has expired
// (within the last 30 days, so old history doesn't trigger mail), for
// people still actively enrolled on the course that awarded it.
func (r *Reminders) certificateReminders(ctx context.Context, now time.Time, org string) ([]reminder, error) {
	rows, err := r.svc.pool.Query(ctx, `
		SELECT ct.id, ct.expires_at, ql.name, c.title, u.id, u.email, u.display_name
		  FROM certificates ct
		  JOIN qualifications ql ON ql.id = ct.qualification_id
		  JOIN courses c ON c.id = ct.course_id
		  JOIN enrolments e ON e.course_id = ct.course_id AND e.user_id = ct.user_id
		                   AND e.role = 'learner' AND e.status = 'active'
		  JOIN users u ON u.id = ct.user_id
		 WHERE ct.revoked_at IS NULL AND ct.expires_at IS NOT NULL
		   AND ct.expires_at <= $1::timestamptz + interval '30 days'
		   AND ct.expires_at > $1::timestamptz - interval '30 days'
		   AND NOT EXISTS (SELECT 1 FROM certificates newer
		                    WHERE newer.user_id = ct.user_id AND newer.qualification_id = ct.qualification_id
		                      AND newer.issued_at > ct.issued_at AND newer.revoked_at IS NULL)
		   AND COALESCE(u.email, '') <> ''
		 ORDER BY ct.expires_at`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []reminder{}
	for rows.Next() {
		var certID, userID uuid.UUID
		var expiresAt time.Time
		var qualification, course, email, name string
		if err := rows.Scan(&certID, &expiresAt, &qualification, &course, &userID, &email, &name); err != nil {
			return nil, err
		}
		rem := reminder{subjectID: certID, userID: userID, email: email, name: name, url: r.link("/courses")}
		if !expiresAt.After(now) {
			rem.kind = ReminderCertExpired
			rem.subject = fmt.Sprintf("Expired: %s", qualification)
			rem.body = fmt.Sprintf("Your %s qualification expired on %s. Renew it by taking \"%s\" again: open My courses and choose Renew.",
				qualification, formatDay(expiresAt), course)
		} else {
			rem.kind = ReminderCertExpiring30d
			rem.subject = fmt.Sprintf("%s expires on %s", qualification, formatDay(expiresAt))
			rem.body = fmt.Sprintf("Your %s qualification expires on %s. You can renew it now by taking \"%s\" again: open My courses and choose Renew.",
				qualification, formatDay(expiresAt), course)
		}
		rem.body += signature(org)
		out = append(out, rem)
	}
	return out, rows.Err()
}
