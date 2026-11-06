package learn

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// Team board targets.
const (
	TeamBoardCourse        = "course"
	TeamBoardQualification = "qualification"
)

const maxTeamBoardGroups = 50

// teamBoardTarget maps a target kind to its table and team_board_groups
// column. Only these fixed strings are ever spliced into SQL.
var teamBoardTarget = map[string]struct{ table, column string }{
	TeamBoardCourse:        {"courses", "course_id"},
	TeamBoardQualification: {"qualifications", "qualification_id"},
}

// TeamBoardSettings is what staff configure: the toggle and the groups.
type TeamBoardSettings struct {
	Enabled bool     `json:"enabled"`
	Groups  []string `json:"groups"`
}

// minTeamBoardMembers: smaller groups show no numbers at all, since a
// percentage of one or two people tells who is done.
const minTeamBoardMembers = 3

// TeamHiddenTooSmall is the HiddenReason of groups below minTeamBoardMembers.
const TeamHiddenTooSmall = "too_small"

// TeamGroupStat is one team on a board. Counts only, never people; the
// counts are null (with HiddenReason) for groups too small to show.
type TeamGroupStat struct {
	GroupName     string `json:"group_name"`
	MembersCount  *int   `json:"members_count"`
	CompleteCount *int   `json:"complete_count"`
	Percent       *int   `json:"percent"`
	HiddenReason  string `json:"hidden_reason,omitempty"`
}

// TeamBoard is a course or qualification board.
type TeamBoard struct {
	TargetType string          `json:"target_type"`
	TargetID   uuid.UUID       `json:"target_id"`
	Title      string          `json:"title"`
	Enabled    bool            `json:"enabled"`
	Groups     []TeamGroupStat `json:"groups"`
}

func teamPercent(done, members int) int {
	if members <= 0 {
		return 0
	}
	return (done*100 + members/2) / members
}

// TeamBoardSettings returns a course's or qualification's board settings.
func (s *Service) TeamBoardSettings(ctx context.Context, kind string, id uuid.UUID) (TeamBoardSettings, error) {
	t, ok := teamBoardTarget[kind]
	if !ok {
		return TeamBoardSettings{}, httperror.BadRequest("unknown team board target")
	}
	out := TeamBoardSettings{Groups: []string{}}
	err := s.pool.QueryRow(ctx, `
		SELECT x.team_board_enabled,
		       COALESCE((SELECT array_agg(group_name ORDER BY group_name) FROM team_board_groups WHERE `+t.column+` = x.id), '{}')
		  FROM `+t.table+` x WHERE x.id = $1`, id).Scan(&out.Enabled, &out.Groups)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, httperror.NotFound(kind)
	}
	return out, err
}

// SetTeamBoardSettings changes the toggle and/or replaces the group list
// (nil = unchanged).
func (s *Service) SetTeamBoardSettings(ctx context.Context, kind string, id uuid.UUID, enabled *bool, groups *[]string) (TeamBoardSettings, error) {
	t, ok := teamBoardTarget[kind]
	if !ok {
		return TeamBoardSettings{}, httperror.BadRequest("unknown team board target")
	}
	var norm []string
	if groups != nil {
		norm = normalizeGroups(*groups)
		if len(norm) > maxTeamBoardGroups {
			return TeamBoardSettings{}, httperror.Validation("a team board can show at most 50 groups", nil)
		}
	}
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if enabled != nil {
			tag, err := tx.Exec(ctx, `UPDATE `+t.table+` SET team_board_enabled = $2 WHERE id = $1`, id, *enabled)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return httperror.NotFound(kind)
			}
		}
		if groups != nil {
			if _, err := tx.Exec(ctx, `
				DELETE FROM team_board_groups WHERE `+t.column+` = $1 AND NOT (group_name = ANY($2))`, id, norm); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO team_board_groups (`+t.column+`, group_name)
				SELECT $1, g FROM unnest($2::text[]) AS g
				ON CONFLICT DO NOTHING`, id, norm); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return TeamBoardSettings{}, err
	}
	return s.TeamBoardSettings(ctx, kind, id)
}

// TeamBoardStats counts, per selected group, its members (user_groups
// cache) and how many of them are done: every activity complete in their
// current cycle (course) or holding a valid certificate (qualification).
// only restricts the result to those groups (nil = all selected groups).
func (s *Service) TeamBoardStats(ctx context.Context, kind string, id uuid.UUID, only []string) ([]TeamGroupStat, error) {
	var done string
	switch kind {
	case TeamBoardCourse:
		done = `(SELECT count(*) FROM activities a JOIN sections s ON s.id = a.section_id WHERE s.course_id = $1) > 0
		    AND (SELECT count(*) FROM completions cp
		           JOIN activities a ON a.id = cp.activity_id
		           JOIN sections s ON s.id = a.section_id
		           JOIN enrolments e ON e.course_id = s.course_id AND e.user_id = cp.user_id
		          WHERE s.course_id = $1 AND cp.user_id = ug.user_id AND cp.cycle = e.cycle)
		      >= (SELECT count(*) FROM activities a JOIN sections s ON s.id = a.section_id WHERE s.course_id = $1)`
	case TeamBoardQualification:
		done = `EXISTS (SELECT 1 FROM certificates ct
		                 WHERE ct.user_id = ug.user_id AND ct.qualification_id = $1 AND ct.revoked_at IS NULL
		                   AND (ct.expires_at IS NULL OR ct.expires_at > now()))`
	default:
		return nil, httperror.BadRequest("unknown team board target")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT g.group_name, count(m.user_id), count(m.user_id) FILTER (WHERE m.done)
		  FROM team_board_groups g
		  LEFT JOIN LATERAL (
		        SELECT ug.user_id, (`+done+`) AS done
		          FROM user_groups ug WHERE ug.group_name = g.group_name) m ON true
		 WHERE g.`+teamBoardTarget[kind].column+` = $1 AND ($2::text[] IS NULL OR g.group_name = ANY($2))
		 GROUP BY g.group_name
		 ORDER BY g.group_name`, id, only)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TeamGroupStat{}
	for rows.Next() {
		var st TeamGroupStat
		var members, done int
		if err := rows.Scan(&st.GroupName, &members, &done); err != nil {
			return nil, err
		}
		if members < minTeamBoardMembers {
			st.HiddenReason = TeamHiddenTooSmall
		} else {
			pct := teamPercent(done, members)
			st.MembersCount, st.CompleteCount, st.Percent = &members, &done, &pct
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// CanSeeQualificationBoard: people holding a certificate of the
// qualification, or actively enrolled on a published course awarding it.
func (s *Service) CanSeeQualificationBoard(ctx context.Context, qualificationID, userID uuid.UUID) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM certificates WHERE qualification_id = $1 AND user_id = $2 AND revoked_at IS NULL)
		    OR EXISTS (SELECT 1 FROM courses c JOIN enrolments e ON e.course_id = c.id
		                WHERE c.awards_qualification_id = $1 AND c.published
		                  AND e.user_id = $2 AND e.status = 'active')`, qualificationID, userID).Scan(&ok)
	return ok, err
}

// MyTeams returns, for every enabled board the person may see, the stats
// of the selected groups they belong to ("Your team: 87% trained").
func (s *Service) MyTeams(ctx context.Context, userID uuid.UUID) ([]TeamBoard, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT 'course', c.id, c.title, array_agg(DISTINCT tb.group_name)
		  FROM team_board_groups tb
		  JOIN courses c ON c.id = tb.course_id
		  JOIN user_groups ug ON ug.group_name = tb.group_name AND ug.user_id = $1
		 WHERE c.team_board_enabled AND c.published
		   AND EXISTS (SELECT 1 FROM enrolments e WHERE e.course_id = c.id AND e.user_id = $1 AND e.status = 'active')
		 GROUP BY c.id, c.title
		UNION ALL
		SELECT 'qualification', q.id, q.name, array_agg(DISTINCT tb.group_name)
		  FROM team_board_groups tb
		  JOIN qualifications q ON q.id = tb.qualification_id
		  JOIN user_groups ug ON ug.group_name = tb.group_name AND ug.user_id = $1
		 WHERE q.team_board_enabled
		   AND (EXISTS (SELECT 1 FROM certificates ct WHERE ct.qualification_id = q.id AND ct.user_id = $1 AND ct.revoked_at IS NULL)
		        OR EXISTS (SELECT 1 FROM courses c JOIN enrolments e ON e.course_id = c.id
		                    WHERE c.awards_qualification_id = q.id AND c.published
		                      AND e.user_id = $1 AND e.status = 'active'))
		 GROUP BY q.id, q.name
		 ORDER BY 3`, userID)
	if err != nil {
		return nil, err
	}
	type board struct {
		TeamBoard
		groups []string
	}
	var boards []board
	for rows.Next() {
		var b board
		if err := rows.Scan(&b.TargetType, &b.TargetID, &b.Title, &b.groups); err != nil {
			rows.Close()
			return nil, err
		}
		b.Enabled = true
		boards = append(boards, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []TeamBoard{}
	for _, b := range boards {
		stats, err := s.TeamBoardStats(ctx, b.TargetType, b.TargetID, b.groups)
		if err != nil {
			return nil, err
		}
		b.Groups = stats
		out = append(out, b.TeamBoard)
	}
	return out, nil
}

// TeamBoardFor builds a full board (all selected groups).
func (s *Service) TeamBoardFor(ctx context.Context, kind string, id uuid.UUID, title string) (TeamBoard, error) {
	settings, err := s.TeamBoardSettings(ctx, kind, id)
	if err != nil {
		return TeamBoard{}, err
	}
	stats, err := s.TeamBoardStats(ctx, kind, id, nil)
	if err != nil {
		return TeamBoard{}, err
	}
	return TeamBoard{TargetType: kind, TargetID: id, Title: strings.TrimSpace(title), Enabled: settings.Enabled, Groups: stats}, nil
}
