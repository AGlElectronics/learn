package learn

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/learn/backend/internal/config"
	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/keycloakadmin"
)

// Enrolment sources and statuses.
const (
	EnrolmentSourceManual   = "manual"
	EnrolmentSourceGroup    = "group"
	EnrolmentStatusActive   = "active"
	EnrolmentStatusInactive = "inactive"
)

const groupSyncInterval = 5 * time.Minute

// GroupLink is one Keycloak group linked to a course. Members counts the
// people Learn knows to be in the group; Active counts their active
// group enrolments on this course.
type GroupLink struct {
	CourseID      uuid.UUID  `json:"course_id"`
	GroupName     string     `json:"group_name"`
	CreatedAt     time.Time  `json:"created_at"`
	LastSyncedAt  *time.Time `json:"last_synced_at"`
	LastSyncError string     `json:"last_sync_error"`
	Members       int        `json:"members"`
	Active        int        `json:"active"`
}

// GroupSyncResult counts what one sync changed.
type GroupSyncResult struct {
	Courses      int      `json:"courses"`
	Groups       int      `json:"groups"`
	Members      int      `json:"members"`
	UsersCreated int      `json:"users_created"`
	Enrolled     int      `json:"enrolled"`
	Reactivated  int      `json:"reactivated"`
	Deactivated  int      `json:"deactivated"`
	Errors       []string `json:"errors"`
}

func (r *GroupSyncResult) add(o GroupSyncResult) {
	r.Courses += o.Courses
	r.Groups += o.Groups
	r.Members += o.Members
	r.UsersCreated += o.UsersCreated
	r.Enrolled += o.Enrolled
	r.Reactivated += o.Reactivated
	r.Deactivated += o.Deactivated
	r.Errors = append(r.Errors, o.Errors...)
}

// ----------------------------------------------------------------------------
// Group links (staff)
// ----------------------------------------------------------------------------

// ListGroupLinks returns the groups linked to a course.
func (s *Service) ListGroupLinks(ctx context.Context, courseID uuid.UUID) ([]GroupLink, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT l.course_id, l.group_name, l.created_at, l.last_synced_at, l.last_sync_error,
		       (SELECT count(*) FROM user_groups ug WHERE ug.group_name = l.group_name),
		       (SELECT count(*) FROM enrolments e
		          JOIN user_groups ug ON ug.user_id = e.user_id AND ug.group_name = l.group_name
		         WHERE e.course_id = l.course_id AND e.source = 'group' AND e.status = 'active')
		  FROM course_group_links l
		 WHERE l.course_id = $1
		 ORDER BY l.group_name`, courseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GroupLink{}
	for rows.Next() {
		var l GroupLink
		if err := rows.Scan(&l.CourseID, &l.GroupName, &l.CreatedAt, &l.LastSyncedAt, &l.LastSyncError,
			&l.Members, &l.Active); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// GetGroupLink returns one linked group of a course.
func (s *Service) GetGroupLink(ctx context.Context, courseID uuid.UUID, group string) (GroupLink, error) {
	links, err := s.ListGroupLinks(ctx, courseID)
	if err != nil {
		return GroupLink{}, err
	}
	for _, l := range links {
		if l.GroupName == group {
			return l, nil
		}
	}
	return GroupLink{}, httperror.NotFound("group link")
}

// AddGroupLink links a group to a course and enrols the members Learn
// already knows about (from earlier syncs and logins). The handler runs a
// Keycloak sync afterwards when one is configured.
func (s *Service) AddGroupLink(ctx context.Context, courseID uuid.UUID, group string, createdBy uuid.UUID) (GroupLink, error) {
	group = strings.TrimSpace(group)
	if group == "" {
		return GroupLink{}, httperror.Validation("group_name is required", nil)
	}
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO course_group_links (course_id, group_name, created_by) VALUES ($1, $2, $3)`,
			courseID, group, createdBy); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return httperror.Conflict("this group is already linked to the course")
			}
			return err
		}
		_, _, err := enrolGroupMembers(ctx, tx, courseID, `
			SELECT user_id FROM user_groups WHERE group_name = $2`, group)
		return err
	})
	if err != nil {
		return GroupLink{}, err
	}
	return s.GetGroupLink(ctx, courseID, group)
}

// RemoveGroupLink unlinks a group. Group enrolments of people who are in
// no remaining linked group (as far as Learn knows) turn inactive; nothing
// is deleted.
func (s *Service) RemoveGroupLink(ctx context.Context, courseID uuid.UUID, group string) (int, error) {
	var deactivated int
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM course_group_links WHERE course_id = $1 AND group_name = $2`, courseID, group)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httperror.NotFound("group link")
		}
		tag, err = tx.Exec(ctx, `
			UPDATE enrolments e SET status = 'inactive', left_group_at = now()
			 WHERE e.course_id = $1 AND e.source = 'group' AND e.status = 'active'
			   AND NOT EXISTS (
			        SELECT 1 FROM course_group_links l
			          JOIN user_groups ug ON ug.group_name = l.group_name AND ug.user_id = e.user_id
			         WHERE l.course_id = e.course_id)
			   AND NOT `+pathCoveredSQL("e.course_id", "e.user_id"), courseID)
		deactivated = int(tag.RowsAffected())
		return err
	})
	return deactivated, err
}

// enrolGroupMembers enrols the users selected by membersSQL (a SELECT of
// user_id with arg as $2) as group learners on a course, and reactivates
// their inactive group enrolments. Manual enrolments are left alone.
func enrolGroupMembers(ctx context.Context, tx pgx.Tx, courseID uuid.UUID, membersSQL string, arg any) (enrolled, reactivated int, err error) {
	rows, err := tx.Query(ctx, `
		INSERT INTO enrolments (course_id, user_id, role, source)
		SELECT DISTINCT $1::uuid, m.user_id, 'learner', 'group'
		  FROM (`+membersSQL+`) m
		ON CONFLICT (course_id, user_id) DO UPDATE
		   SET status = 'active', left_group_at = NULL
		 WHERE enrolments.source = 'group' AND enrolments.status = 'inactive'
		RETURNING (xmax = 0)`, courseID, arg)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var inserted bool
		if err := rows.Scan(&inserted); err != nil {
			return 0, 0, err
		}
		if inserted {
			enrolled++
		} else {
			reactivated++
		}
	}
	return enrolled, reactivated, rows.Err()
}

// ----------------------------------------------------------------------------
// Login refresh
// ----------------------------------------------------------------------------

// ApplyLoginGroups refreshes a user's group cache from their token's groups
// claim and applies it to linked courses right away: joining a linked
// group enrols (or reactivates), leaving every linked group of a course
// makes the group enrolment inactive. A no-op when nothing changed.
func (s *Service) ApplyLoginGroups(ctx context.Context, userID uuid.UUID, groups []string) error {
	norm := normalizeGroups(groups)
	var current []string
	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(array_agg(group_name ORDER BY group_name), '{}') FROM user_groups WHERE user_id = $1`,
		userID).Scan(&current); err != nil {
		return err
	}
	if slices.Equal(current, norm) {
		return nil
	}
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			"learn-user-groups:"+userID.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM user_groups WHERE user_id = $1 AND NOT (group_name = ANY($2))`, userID, norm); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_groups (user_id, group_name)
			SELECT $1, g FROM unnest($2::text[]) AS g
			ON CONFLICT (user_id, group_name) DO UPDATE SET synced_at = now()`, userID, norm); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO enrolments (course_id, user_id, role, source)
			SELECT DISTINCT l.course_id, $1::uuid, 'learner', 'group'
			  FROM course_group_links l WHERE l.group_name = ANY($2)
			ON CONFLICT (course_id, user_id) DO UPDATE
			   SET status = 'active', left_group_at = NULL
			 WHERE enrolments.source = 'group' AND enrolments.status = 'inactive'`, userID, norm); err != nil {
			return err
		}
		if err := applyLoginPathGroups(ctx, tx, userID, norm); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			UPDATE enrolments e SET status = 'inactive', left_group_at = now()
			 WHERE e.user_id = $1 AND e.source = 'group' AND e.status = 'active'
			   AND NOT EXISTS (SELECT 1 FROM course_group_links l
			                    WHERE l.course_id = e.course_id AND l.group_name = ANY($2))
			   AND NOT `+pathCoveredSQL("e.course_id", "e.user_id"), userID, norm)
		return err
	})
}

func normalizeGroups(groups []string) []string {
	out := []string{}
	for _, g := range groups {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, g)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// ----------------------------------------------------------------------------
// Keycloak sync worker
// ----------------------------------------------------------------------------

// GroupSync enrols the members of linked Keycloak groups. Users are created
// with issuer = the OIDC issuer and subject = the Keycloak user id, which is
// the token's sub, so they merge with the row their first login would make.
type GroupSync struct {
	svc    *Service
	dir    *keycloakadmin.Client
	issuer string
	log    zerolog.Logger
}

// GroupSyncIssuer is the issuer synced users are stored under: the
// configured OIDC issuer, else the Keycloak realm URL.
func GroupSyncIssuer(cfg *config.Config) string {
	if cfg.OIDCIssuerURL != "" {
		return cfg.OIDCIssuerURL
	}
	return strings.TrimRight(cfg.KeycloakBaseURL, "/") + "/realms/" + cfg.KeycloakRealm
}

// NewGroupSync returns nil when there is no Keycloak admin client; every
// method is then unavailable and Run returns at once.
func NewGroupSync(svc *Service, dir *keycloakadmin.Client, issuer string, logger zerolog.Logger) *GroupSync {
	if dir == nil {
		return nil
	}
	return &GroupSync{svc: svc, dir: dir, issuer: issuer, log: logger.With().Str("component", "groupsync").Logger()}
}

// ListGroups returns every Keycloak group.
func (g *GroupSync) ListGroups(ctx context.Context) ([]keycloakadmin.Group, error) {
	return g.dir.ListGroups(ctx)
}

// Run syncs every linked course shortly after start and then every five
// minutes until ctx ends.
func (g *GroupSync) Run(ctx context.Context) {
	if g == nil {
		return
	}
	t := time.NewTimer(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ran, res, err := g.SyncAll(ctx)
			switch {
			case err != nil:
				g.log.Error().Err(err).Msg("group sync failed")
			case ran:
				g.log.Info().Int("courses", res.Courses).Int("users_created", res.UsersCreated).
					Int("enrolled", res.Enrolled).Int("reactivated", res.Reactivated).
					Int("deactivated", res.Deactivated).Strs("errors", res.Errors).Msg("group sync done")
			}
			t.Reset(groupSyncInterval)
		}
	}
}

// SyncAll syncs every course with linked groups. ran is false when another
// replica holds the sync lock.
func (g *GroupSync) SyncAll(ctx context.Context) (bool, GroupSyncResult, error) {
	total := GroupSyncResult{Errors: []string{}}
	conn, err := g.svc.pool.Acquire(ctx)
	if err != nil {
		return false, total, err
	}
	defer conn.Release()
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended('learn-group-sync', 0))`).Scan(&got); err != nil {
		return false, total, err
	}
	if !got {
		return false, total, nil
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended('learn-group-sync', 0))`)
	}()

	rows, err := g.svc.pool.Query(ctx, `SELECT DISTINCT course_id FROM course_group_links`)
	if err != nil {
		return true, total, err
	}
	courseIDs, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return true, total, err
	}
	cache := map[string]memberLookup{}
	for _, id := range courseIDs {
		res, err := g.syncCourse(ctx, id, cache)
		if err != nil {
			return true, total, err
		}
		total.add(res)
	}
	paths, err := g.syncPathsAll(ctx, cache)
	if err != nil {
		return true, total, err
	}
	total.add(paths)
	return true, total, nil
}

// SyncCourse syncs one course now ("Sync now").
func (g *GroupSync) SyncCourse(ctx context.Context, courseID uuid.UUID) (GroupSyncResult, error) {
	return g.syncCourse(ctx, courseID, map[string]memberLookup{})
}

type memberLookup struct {
	ids     []uuid.UUID
	created int
	err     error
}

// members fetches a group's members once per run and upserts them as users.
func (g *GroupSync) members(ctx context.Context, group string, cache map[string]memberLookup) memberLookup {
	if m, ok := cache[group]; ok {
		return memberLookup{ids: m.ids, err: m.err} // users were counted on first lookup
	}
	var m memberLookup
	members, err := g.dir.GroupMembers(ctx, group)
	if err != nil {
		m.err = err
	} else {
		m.ids = make([]uuid.UUID, 0, len(members))
		for _, mem := range members {
			id, created, err := g.upsertUser(ctx, mem)
			if err != nil {
				m = memberLookup{err: err}
				break
			}
			m.ids = append(m.ids, id)
			if created {
				m.created++
			}
		}
	}
	cache[group] = m
	return m
}

func (g *GroupSync) upsertUser(ctx context.Context, m keycloakadmin.Member) (uuid.UUID, bool, error) {
	display := strings.TrimSpace(strings.TrimSpace(m.FirstName) + " " + strings.TrimSpace(m.LastName))
	if display == "" {
		display = m.Username
	}
	if display == "" {
		display = m.ID
	}
	var email *string
	if e := strings.TrimSpace(m.Email); e != "" {
		email = &e
	}
	var id uuid.UUID
	var created bool
	err := g.svc.pool.QueryRow(ctx, `
		INSERT INTO users (issuer, subject, display_name, email)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (issuer, subject) DO UPDATE SET email = COALESCE(users.email, EXCLUDED.email)
		RETURNING id, (xmax = 0)`, g.issuer, m.ID, display, email).Scan(&id, &created)
	return id, created, err
}

// syncCourse enrols the members of every group linked to the course and
// deactivates group enrolments of people in none of them. If any group
// could not be read, nobody is deactivated this round.
func (g *GroupSync) syncCourse(ctx context.Context, courseID uuid.UUID, cache map[string]memberLookup) (GroupSyncResult, error) {
	res := GroupSyncResult{Courses: 1, Errors: []string{}}
	rows, err := g.svc.pool.Query(ctx, `
		SELECT group_name FROM course_group_links WHERE course_id = $1 ORDER BY group_name`, courseID)
	if err != nil {
		return res, err
	}
	groups, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return res, err
	}

	complete := true
	perGroup := map[string][]uuid.UUID{}
	linkErr := map[string]string{}
	all := map[uuid.UUID]bool{}
	for _, group := range groups {
		m := g.members(ctx, group, cache)
		if m.err != nil {
			complete = false
			linkErr[group] = m.err.Error()
			res.Errors = append(res.Errors, group+": "+m.err.Error())
			continue
		}
		res.Groups++
		res.UsersCreated += m.created
		perGroup[group] = m.ids
		for _, id := range m.ids {
			all[id] = true
		}
	}
	memberIDs := make([]uuid.UUID, 0, len(all))
	for id := range all {
		memberIDs = append(memberIDs, id)
	}
	res.Members = len(memberIDs)

	err = db.WithTx(ctx, g.svc.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			"learn-group-sync:"+courseID.String()); err != nil {
			return err
		}
		for group, ids := range perGroup {
			if _, err := tx.Exec(ctx, `
				DELETE FROM user_groups WHERE group_name = $1 AND NOT (user_id = ANY($2))`, group, ids); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO user_groups (user_id, group_name)
				SELECT u, $1 FROM unnest($2::uuid[]) AS u
				ON CONFLICT (user_id, group_name) DO UPDATE SET synced_at = now()`, group, ids); err != nil {
				return err
			}
		}
		var err error
		res.Enrolled, res.Reactivated, err = enrolGroupMembers(ctx, tx, courseID, `
			SELECT unnest($2::uuid[]) AS user_id`, memberIDs)
		if err != nil {
			return err
		}
		if complete {
			tag, err := tx.Exec(ctx, `
				UPDATE enrolments e SET status = 'inactive', left_group_at = now()
				 WHERE e.course_id = $1 AND e.source = 'group' AND e.status = 'active'
				   AND NOT (e.user_id = ANY($2))
				   AND NOT `+pathCoveredSQL("e.course_id", "e.user_id"), courseID, memberIDs)
			if err != nil {
				return err
			}
			res.Deactivated = int(tag.RowsAffected())
		}
		for _, group := range groups {
			if _, err := tx.Exec(ctx, `
				UPDATE course_group_links SET last_synced_at = now(), last_sync_error = $3
				 WHERE course_id = $1 AND group_name = $2`, courseID, group, linkErr[group]); err != nil {
				return err
			}
		}
		return nil
	})
	return res, err
}
