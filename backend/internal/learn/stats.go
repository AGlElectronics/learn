package learn

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/AGlElectronics/learn/backend/internal/domain"
)

// OptionStat is how often one option was picked.
type OptionStat struct {
	OptionID uuid.UUID `json:"option_id"`
	Label    string    `json:"label"`
	Correct  bool      `json:"correct"`
	Picks    int       `json:"picks"`
}

// RegionStat is how often a hotspot region got at least one tap.
type RegionStat struct {
	RegionID uuid.UUID `json:"region_id"`
	Correct  bool      `json:"correct"`
	Hits     int       `json:"hits"`
	// HitRate is the percentage of answers with a tap in the region (nil
	// without answers).
	HitRate *float64 `json:"hit_rate"`
}

// QuestionStat summarizes one bank question over every submitted attempt
// (all cycles).
type QuestionStat struct {
	QuestionID uuid.UUID `json:"question_id"`
	CategoryID uuid.UUID `json:"category_id"`
	Kind       string    `json:"kind"`
	Attempts   int       `json:"attempts"`
	Correct    int       `json:"correct"`
	// PercentCorrect is nil while nobody has answered the question.
	PercentCorrect *float64     `json:"percent_correct"`
	Options        []OptionStat `json:"options"`
	// Hotspot only: per-region hits, all taps, and taps outside every
	// correct region.
	Regions  []RegionStat `json:"regions"`
	Taps     int          `json:"taps"`
	MissTaps int          `json:"miss_taps"`
}

func percent(n, of int) *float64 {
	if of == 0 {
		return nil
	}
	p := float64(n) * 100 / float64(of)
	return &p
}

// QuestionStats computes per-question statistics for a course's bank from
// submitted attempts: attempts, % correct, option picks and hotspot hits.
func (s *Service) QuestionStats(ctx context.Context, courseID uuid.UUID) ([]QuestionStat, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT q.id, q.category_id, q.kind
		  FROM questions q JOIN question_categories c ON c.id = q.category_id
		 WHERE c.course_id = $1
		 ORDER BY c.position, q.position, q.created_at`, courseID)
	if err != nil {
		return nil, err
	}
	out := []QuestionStat{}
	index := map[uuid.UUID]int{}
	for rows.Next() {
		st := QuestionStat{Options: []OptionStat{}, Regions: []RegionStat{}}
		if err := rows.Scan(&st.QuestionID, &st.CategoryID, &st.Kind); err != nil {
			rows.Close()
			return nil, err
		}
		index[st.QuestionID] = len(out)
		out = append(out, st)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, len(out))
	for i, st := range out {
		ids[i] = st.QuestionID
	}

	// Options in display order, so unpicked options show up with 0.
	rows, err = s.pool.Query(ctx, `
		SELECT id, question_id, label, correct FROM question_options
		 WHERE question_id = ANY($1) ORDER BY position, id`, ids)
	if err != nil {
		return nil, err
	}
	optIndex := map[uuid.UUID][2]int{}
	for rows.Next() {
		var o OptionStat
		var qid uuid.UUID
		if err := rows.Scan(&o.OptionID, &qid, &o.Label, &o.Correct); err != nil {
			rows.Close()
			return nil, err
		}
		i := index[qid]
		optIndex[o.OptionID] = [2]int{i, len(out[i].Options)}
		out[i].Options = append(out[i].Options, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	regions, err := s.regionsFor(ctx, s.pool, ids)
	if err != nil {
		return nil, err
	}
	for qid, rs := range regions {
		i := index[qid]
		for _, r := range rs {
			out[i].Regions = append(out[i].Regions, RegionStat{RegionID: r.ID, Correct: r.Correct})
		}
	}

	rows, err = s.pool.Query(ctx, `
		SELECT aq.question_id, COALESCE(aq.correct, FALSE), aq.chosen_option_ids, aq.taps
		  FROM quiz_attempt_questions aq
		  JOIN quiz_attempts a ON a.id = aq.attempt_id
		 WHERE aq.question_id = ANY($1) AND a.submitted_at IS NOT NULL`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var qid uuid.UUID
		var correct bool
		var chosen []uuid.UUID
		var rawTaps []byte
		if err := rows.Scan(&qid, &correct, &chosen, &rawTaps); err != nil {
			return nil, err
		}
		i := index[qid]
		st := &out[i]
		st.Attempts++
		if correct {
			st.Correct++
		}
		for _, oid := range chosen {
			if at, ok := optIndex[oid]; ok && at[0] == i {
				st.Options[at[1]].Picks++
			}
		}
		if st.Kind != domain.QuestionHotspot {
			continue
		}
		var taps []Point
		if len(rawTaps) > 0 {
			if err := json.Unmarshal(rawTaps, &taps); err != nil {
				return nil, err
			}
		}
		qRegions := regions[qid]
		for j, r := range qRegions {
			for _, p := range taps {
				if regionContains(r.Shape, r.Coords, p) {
					st.Regions[j].Hits++
					break
				}
			}
		}
		for _, p := range taps {
			st.Taps++
			inCorrect := false
			for _, r := range qRegions {
				if r.Correct && regionContains(r.Shape, r.Coords, p) {
					inCorrect = true
					break
				}
			}
			if !inCorrect {
				st.MissTaps++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].PercentCorrect = percent(out[i].Correct, out[i].Attempts)
		for j := range out[i].Regions {
			out[i].Regions[j].HitRate = percent(out[i].Regions[j].Hits, out[i].Attempts)
		}
	}
	return out, nil
}
