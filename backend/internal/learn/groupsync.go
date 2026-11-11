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

// NormalizeGroup is the comparison form of a group: trimmed, one leading
// "/" dropped, lower-case. Links, the user_groups cache, team boards and
// the token's groups claim are all stored and compared in this form, and a
// link matches only the full path ("learn/cohort-a" never matches a plain
// "cohort-a" claim).
// TODO(shared/groupmatch): replace with the shared groupmatch package.
func NormalizeGroup(g string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(g), "/"))
}

// GroupLinkable reports whether a trainer may see and link the group at
// path under LEARN_LINKABLE_GROUPS patterns: "*" matches every group,
// "/a/*" every group below /a (not /a itself), anything else that exact
// path. Comparison uses NormalizeGroup.
func GroupLinkable(path string, patterns []string) bool {
	key := NormalizeGroup(path)
	if key == "" {
		return false
	}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		switch {
		case p == "*":
			return true
		case strings.HasSuffix(p, "/*"):
			if parent := NormalizeGroup(strings.TrimSuffix(p, "/*")); parent != "" && strings.HasPrefix(key, parent+"/") {
				return true
			}
		case NormalizeGroup(p) == key:
			return true
		}
	}
	return false
}

// displayPath is how a linked group is shown: its path with one leading "/".
func displayPath(g string) string {
	g = strings.TrimSpace(g)
	if !strings.HasPrefix(g, "/") {
		g = "/" + g
	}
	return g
}

// GroupLink is one Keycloak group linked to a course. GroupName is the
// normalised key (use it to unlink); GroupPath is the full path as the
// directory spells it. Members counts the people Learn knows to be in the
// group; Active counts their active group enrolments on this course.
// PathResolved is false for a link made by plain name before links used
// paths, until the start-up lookup found its path.
type GroupLink struct {
	CourseID      uuid.UUID  `json:"course_id"`
	GroupName     string     `json:"group_name"`
	GroupPath     string     `json:"group_path"`
	PathResolved  bool       `json:"path_resolved"`
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
		SELECT l.course_id, l.group_name, l.group_path, l.path_resolved, l.created_at, l.last_synced_at, l.last_sync_error,
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
		if err := rows.Scan(&l.CourseID, &l.GroupName, &l.GroupPath, &l.PathResolved, &l.CreatedAt, &l.LastSyncedAt, &l.LastSyncError,
			&l.Members, &l.Active); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// GetGroupLink returns one linked group of a course (group: key or path).
func (s *Service) GetGroupLink(ctx context.Context, courseID uuid.UUID, group string) (GroupLink, error) {
	links, err := s.ListGroupLinks(ctx, courseID)
	if err != nil {
		return GroupLink{}, err
	}
	key := NormalizeGroup(group)
	for _, l := range links {
		if l.GroupName == key {
			return l, nil
		}
	}
	return GroupLink{}, httperror.NotFound("group link")
}

// AddGroupLink links a group (by full path) to a course and enrols the
// members Learn already knows about (from earlier syncs and logins). The
// handler runs a Keycloak sync afterwards when one is configured.
func (s *Service) AddGroupLink(ctx context.Context, courseID uuid.UUID, path string, createdBy uuid.UUID) (GroupLink, error) {
	group := NormalizeGroup(path)
	if group == "" {
		return GroupLink{}, httperror.Validation("group_path is required", nil)
	}
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO course_group_links (course_id, group_name, group_path, path_resolved, created_by)
			VALUES ($1, $2, $3, TRUE, $4)`,
			courseID, group, displayPath(path), createdBy); err != nil {
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
	group = NormalizeGroup(group)
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
		if g = NormalizeGroup(g); g != "" {
			out = append(out, g)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// ----------------------------------------------------------------------------
// Keycloak sync worker
// ----------------------------------------------------------------------------

// GroupSync enrols the members of linked Keycloak groups. Synced users are
// stored under the OIDC issuer with their Keycloak id in idp_user_id and,
// until they first sign in, as their subject too (Keycloak's default `sub`).
// When the IdP's `sub` differs, the first sign-in adopts the synced row by
// username instead of creating a second person (see auth.Resolver).
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

// FindGroup returns the directory group at path (compared like links), or
// ok = false.
func (g *GroupSync) FindGroup(ctx context.Context, path string) (keycloakadmin.Group, bool, error) {
	groups, err := g.dir.ListGroups(ctx)
	if err != nil {
		return keycloakadmin.Group{}, false, err
	}
	key := NormalizeGroup(path)
	for _, grp := range groups {
		if NormalizeGroup(grp.Path) == key {
			return grp, true, nil
		}
	}
	return keycloakadmin.Group{}, false, nil
}

// Run resolves legacy links, then syncs every linked course shortly after
// start and every five minutes until ctx ends.
func (g *GroupSync) Run(ctx context.Context) {
	if g == nil {
		return
	}
	t := time.NewTimer(30 * time.Second)
	defer t.Stop()
	resolved := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !resolved {
				if n, err := g.ResolveLegacyLinks(ctx); err != nil {
					g.log.Error().Err(err).Msg("resolving legacy group links failed; retrying next round")
				} else {
					resolved = true
					if n > 0 {
						g.log.Info().Int("links", n).Msg("legacy group links resolved to full paths")
					}
				}
			}
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

// ResolveLegacyLinks looks up, once, the full path of links made by plain
// group name. A name that matches exactly one directory group (ignoring
// case) gets that group's path; a missing or ambiguous name keeps its link
// and records why in last_sync_error. Returns how many links it resolved.
func (g *GroupSync) ResolveLegacyLinks(ctx context.Context) (int, error) {
	type legacy struct {
		table, owner string
		ownerID      uuid.UUID
		key          string
	}
	var todo []legacy
	for _, t := range []struct{ table, owner string }{{"course_group_links", "course_id"}, {"path_group_links", "path_id"}} {
		rows, err := g.svc.pool.Query(ctx, `SELECT `+t.owner+`, group_name FROM `+t.table+` WHERE NOT path_resolved`)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			l := legacy{table: t.table, owner: t.owner}
			if err := rows.Scan(&l.ownerID, &l.key); err != nil {
				rows.Close()
				return 0, err
			}
			todo = append(todo, l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, err
		}
	}
	if len(todo) == 0 {
		return 0, nil
	}
	groups, err := g.dir.ListGroups(ctx)
	if err != nil {
		return 0, err
	}
	byName := map[string][]keycloakadmin.Group{}
	for _, grp := range groups {
		byName[strings.ToLower(grp.Name)] = append(byName[strings.ToLower(grp.Name)], grp)
	}
	resolved := 0
	for _, l := range todo {
		matches := byName[l.key]
		if len(matches) != 1 {
			msg := "no directory group is called " + l.key + "; link it again by its full path"
			if len(matches) > 1 {
				msg = "several directory groups are called " + l.key + "; link the right one again by its full path"
			}
			if _, err := g.svc.pool.Exec(ctx, `UPDATE `+l.table+` SET last_sync_error = $3 WHERE `+l.owner+` = $1 AND group_name = $2`,
				l.ownerID, l.key, msg); err != nil {
				return resolved, err
			}
			continue
		}
		path := matches[0].Path
		tag, err := g.svc.pool.Exec(ctx, `
			UPDATE `+l.table+` SET group_name = $3, group_path = $4, path_resolved = TRUE
			 WHERE `+l.owner+` = $1 AND group_name = $2
			   AND NOT EXISTS (SELECT 1 FROM `+l.table+` o WHERE o.`+l.owner+` = $1 AND o.group_name = $3 AND $3 <> $2)`,
			l.ownerID, l.key, NormalizeGroup(path), displayPath(path))
		if err != nil {
			return resolved, err
		}
		if tag.RowsAffected() > 0 {
			resolved++
		}
	}
	return resolved, nil
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

// linkedGroup is one link to sync: the normalised key and the path to look
// up in the directory.
type linkedGroup struct {
	key, path string
}

func collectLinkedGroups(rows pgx.Rows) ([]linkedGroup, error) {
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (linkedGroup, error) {
		var l linkedGroup
		err := row.Scan(&l.key, &l.path)
		return l, err
	})
}

// members fetches a group's members once per run and upserts them as users.
func (g *GroupSync) members(ctx context.Context, group linkedGroup, cache map[string]memberLookup) memberLookup {
	if m, ok := cache[group.key]; ok {
		return memberLookup{ids: m.ids, err: m.err} // users were counted on first lookup
	}
	var m memberLookup
	members, err := g.dir.GroupMembers(ctx, group.path)
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
	cache[group.key] = m
	return m
}

// upsertUser finds the person by their directory id (idp_user_id), else by
// (issuer, subject = directory id), else creates them with that subject.
// Their directory id and username are recorded either way.
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
	err := g.svc.pool.QueryRow(ctx, `
		UPDATE users SET email = COALESCE(users.email, $3), idp_username = $4
		 WHERE issuer = $1 AND idp_user_id = $2 AND erased_at IS NULL
		 RETURNING id`, g.issuer, m.ID, email, m.Username).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, err
	}
	if m.Username != "" {
		// Signed in before the first sync, with a `sub` other than the
		// directory id: the resolver recorded their username.
		err = g.svc.pool.QueryRow(ctx, `
			UPDATE users SET idp_user_id = $2, email = COALESCE(users.email, $3)
			 WHERE id = (SELECT id FROM users
			              WHERE issuer = $1 AND idp_user_id IS NULL AND erased_at IS NULL
			                AND lower(idp_username) = lower($4)
			              LIMIT 1)
			 RETURNING id`, g.issuer, m.ID, email, m.Username).Scan(&id)
		if err == nil {
			return id, false, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, err
		}
	}
	var created bool
	err = g.svc.pool.QueryRow(ctx, `
		INSERT INTO users (issuer, subject, display_name, email, idp_user_id, idp_username)
		VALUES ($1, $2, $3, $4, $2, $5)
		ON CONFLICT (issuer, subject) DO UPDATE
		   SET email = COALESCE(users.email, EXCLUDED.email),
		       idp_user_id = COALESCE(users.idp_user_id, EXCLUDED.idp_user_id),
		       idp_username = EXCLUDED.idp_username
		RETURNING id, (xmax = 0)`, g.issuer, m.ID, display, email, m.Username).Scan(&id, &created)
	return id, created, err
}

// syncCourse enrols the members of every group linked to the course and
// deactivates group enrolments of people in none of them. If any group
// could not be read, nobody is deactivated this round.
func (g *GroupSync) syncCourse(ctx context.Context, courseID uuid.UUID, cache map[string]memberLookup) (GroupSyncResult, error) {
	res := GroupSyncResult{Courses: 1, Errors: []string{}}
	rows, err := g.svc.pool.Query(ctx, `
		SELECT group_name, group_path FROM course_group_links WHERE course_id = $1 ORDER BY group_name`, courseID)
	if err != nil {
		return res, err
	}
	groups, err := collectLinkedGroups(rows)
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
			linkErr[group.key] = m.err.Error()
			res.Errors = append(res.Errors, group.path+": "+m.err.Error())
			continue
		}
		res.Groups++
		res.UsersCreated += m.created
		perGroup[group.key] = m.ids
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
		if err := refreshGroupCache(ctx, tx, perGroup); err != nil {
			return err
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
				 WHERE course_id = $1 AND group_name = $2`, courseID, group.key, linkErr[group.key]); err != nil {
				return err
			}
		}
		return nil
	})
	return res, err
}

// refreshGroupCache replaces the user_groups rows of each synced group
// (keyed by normalised group) with its current members.
func refreshGroupCache(ctx context.Context, tx pgx.Tx, perGroup map[string][]uuid.UUID) error {
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
	return nil
}

// ----------------------------------------------------------------------------
// People from the directory
// ----------------------------------------------------------------------------

// SearchDirectory finds people in the identity provider's directory.
func (g *GroupSync) SearchDirectory(ctx context.Context, q string, max int) ([]keycloakadmin.Member, error) {
	return g.dir.SearchUsers(ctx, q, max)
}

// KnownDirectoryIDs reports which directory ids Learn already has a users
// row for (by idp_user_id, or by subject for rows from before it existed).
func (g *GroupSync) KnownDirectoryIDs(ctx context.Context, ids []string) (map[string]bool, error) {
	rows, err := g.svc.pool.Query(ctx, `
		SELECT COALESCE(idp_user_id, subject) FROM users
		 WHERE issuer = $1 AND (idp_user_id = ANY($2) OR subject = ANY($2))`, g.issuer, ids)
	if err != nil {
		return nil, err
	}
	found, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, id := range found {
		out[id] = true
	}
	return out, nil
}

// EnsureDirectoryUser returns the users row for a directory person,
// creating it from the directory when Learn has never seen them, and notes
// that they belong to orgID.
func (g *GroupSync) EnsureDirectoryUser(ctx context.Context, directoryID string, orgID uuid.UUID) (uuid.UUID, error) {
	m, err := g.dir.GetUser(ctx, directoryID)
	if errors.Is(err, keycloakadmin.ErrUserNotFound) {
		return uuid.Nil, httperror.NotFound("person in the directory")
	}
	if err != nil {
		return uuid.Nil, err
	}
	id, _, err := g.upsertUser(ctx, m)
	if err != nil {
		return uuid.Nil, err
	}
	_, err = g.svc.pool.Exec(ctx, `
		INSERT INTO user_organizations (user_id, organization_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, orgID)
	return id, err
}
