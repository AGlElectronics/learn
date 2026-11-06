package learn

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// Region shapes for hotspot questions.
const (
	ShapeRect   = "rect"
	ShapeCircle = "circle"
)

// Point is one tap, normalized to 0..1 of the image (origin top-left).
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// QuestionRegion is one area drawn on a hotspot image. Coords are
// normalized 0..1: rect {x, y, w, h}; circle {cx, cy, rx, ry} (an ellipse in
// normalized space, i.e. a circle at the image's aspect ratio).
type QuestionRegion struct {
	ID         uuid.UUID          `json:"id"`
	QuestionID uuid.UUID          `json:"question_id"`
	Shape      string             `json:"shape"`
	Coords     map[string]float64 `json:"coords"`
	Correct    bool               `json:"correct"`
	Position   int                `json:"position"`
}

// RegionInput is one region in a create or replace payload.
type RegionInput struct {
	Shape   string
	Coords  map[string]float64
	Correct bool
}

const coordEps = 1e-6

func in01(v float64) bool { return v >= -coordEps && v <= 1+coordEps && !math.IsNaN(v) }

// normalizeRegion validates a region and keeps only its shape's keys. A
// circle may be given as {cx, cy, r}.
func normalizeRegion(in RegionInput) (RegionInput, error) {
	get := func(k string) (float64, bool) {
		v, ok := in.Coords[k]
		return v, ok
	}
	bad := func(msg string) (RegionInput, error) {
		return RegionInput{}, httperror.Validation("region: "+msg, nil)
	}
	switch in.Shape {
	case ShapeRect:
		x, okX := get("x")
		y, okY := get("y")
		w, okW := get("w")
		h, okH := get("h")
		if !okX || !okY || !okW || !okH {
			return bad("a rectangle needs x, y, w and h")
		}
		if !in01(x) || !in01(y) || w <= 0 || h <= 0 || !in01(x+w) || !in01(y+h) {
			return bad("rectangle coordinates must lie within the image (0 to 1)")
		}
		return RegionInput{Shape: ShapeRect, Coords: map[string]float64{"x": x, "y": y, "w": w, "h": h}, Correct: in.Correct}, nil
	case ShapeCircle:
		cx, okX := get("cx")
		cy, okY := get("cy")
		rx, okRX := get("rx")
		ry, okRY := get("ry")
		if r, ok := get("r"); ok && !okRX && !okRY {
			rx, ry, okRX, okRY = r, r, true, true
		}
		if !okX || !okY || !okRX || !okRY {
			return bad("a circle needs cx, cy and rx, ry (or r)")
		}
		if !in01(cx) || !in01(cy) || rx <= 0 || ry <= 0 || rx > 1 || ry > 1 {
			return bad("circle coordinates must lie within the image (0 to 1)")
		}
		return RegionInput{Shape: ShapeCircle, Coords: map[string]float64{"cx": cx, "cy": cy, "rx": rx, "ry": ry}, Correct: in.Correct}, nil
	default:
		return bad("shape must be rect or circle")
	}
}

// regionContains reports whether a tap lands inside a region (edges count).
func regionContains(shape string, c map[string]float64, p Point) bool {
	switch shape {
	case ShapeRect:
		return p.X >= c["x"]-coordEps && p.X <= c["x"]+c["w"]+coordEps &&
			p.Y >= c["y"]-coordEps && p.Y <= c["y"]+c["h"]+coordEps
	case ShapeCircle:
		if c["rx"] <= 0 || c["ry"] <= 0 {
			return false
		}
		dx := (p.X - c["cx"]) / c["rx"]
		dy := (p.Y - c["cy"]) / c["ry"]
		return dx*dx+dy*dy <= 1+coordEps
	}
	return false
}

// markHotspot is the hotspot rule: right only if every correct region gets
// exactly one tap and no tap lands outside all correct regions.
func markHotspot(regions []QuestionRegion, taps []Point) bool {
	correct := []QuestionRegion{}
	for _, r := range regions {
		if r.Correct {
			correct = append(correct, r)
		}
	}
	if len(correct) == 0 {
		return false
	}
	for _, r := range correct {
		n := 0
		for _, p := range taps {
			if regionContains(r.Shape, r.Coords, p) {
				n++
			}
		}
		if n != 1 {
			return false
		}
	}
	for _, p := range taps {
		inside := false
		for _, r := range correct {
			if regionContains(r.Shape, r.Coords, p) {
				inside = true
				break
			}
		}
		if !inside {
			return false
		}
	}
	return true
}

func correctRegionCount(regions []QuestionRegion) int {
	n := 0
	for _, r := range regions {
		if r.Correct {
			n++
		}
	}
	return n
}

// maxTaps is how many taps a hotspot question allows.
func maxTaps(clicks *int, regions []QuestionRegion) int {
	if clicks != nil {
		return *clicks
	}
	return correctRegionCount(regions)
}

// validateTaps checks a learner's taps against the image bounds and limit.
func validateTaps(taps []Point, limit int) error {
	if len(taps) > limit {
		return httperror.Validation(fmt.Sprintf("a hotspot question allows at most %d tap(s)", limit), nil)
	}
	for _, p := range taps {
		if !in01(p.X) || !in01(p.Y) {
			return httperror.Validation("taps must lie within the image (0 to 1)", nil)
		}
	}
	return nil
}

// regionsFor loads regions for a set of questions, keyed by question id.
func (s *Service) regionsFor(ctx context.Context, q interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}, ids []uuid.UUID) (map[uuid.UUID][]QuestionRegion, error) {
	out := map[uuid.UUID][]QuestionRegion{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `
		SELECT id, question_id, shape, coords, correct, position
		  FROM question_regions WHERE question_id = ANY($1)
		 ORDER BY position, id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r QuestionRegion
		var raw []byte
		if err := rows.Scan(&r.ID, &r.QuestionID, &r.Shape, &raw, &r.Correct, &r.Position); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &r.Coords); err != nil {
			return nil, err
		}
		out[r.QuestionID] = append(out[r.QuestionID], r)
	}
	return out, rows.Err()
}

func insertRegions(ctx context.Context, tx pgx.Tx, questionID uuid.UUID, regions []RegionInput) error {
	for i, r := range regions {
		raw, err := json.Marshal(r.Coords)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO question_regions (question_id, shape, coords, correct, position)
			VALUES ($1, $2, $3, $4, $5)`, questionID, r.Shape, raw, r.Correct, i); err != nil {
			return err
		}
	}
	return nil
}

func normalizeRegions(in []RegionInput) ([]RegionInput, error) {
	out := make([]RegionInput, len(in))
	for i, r := range in {
		n, err := normalizeRegion(r)
		if err != nil {
			return nil, err
		}
		out[i] = n
	}
	return out, nil
}

func regionsFromInput(in []RegionInput) []QuestionRegion {
	out := make([]QuestionRegion, len(in))
	for i, r := range in {
		out[i] = QuestionRegion{Shape: r.Shape, Coords: r.Coords, Correct: r.Correct, Position: i}
	}
	return out
}

// ReplaceRegions swaps a hotspot question's whole region set. Like options,
// regions lock once the question appears in a submitted attempt.
func (s *Service) ReplaceRegions(ctx context.Context, questionID uuid.UUID, in []RegionInput) (Question, error) {
	q, err := s.GetQuestion(ctx, questionID)
	if err != nil {
		return Question{}, err
	}
	if q.Kind != domain.QuestionHotspot {
		return Question{}, httperror.Validation("only hotspot questions have regions", nil)
	}
	if q.Locked {
		return Question{}, httperror.Conflict("this question has been answered in a submitted attempt; reset the quiz to change its regions")
	}
	regions, err := normalizeRegions(in)
	if err != nil {
		return Question{}, err
	}
	next := q
	next.Regions = regionsFromInput(regions)
	if err := validateQuestion(next); err != nil {
		return Question{}, err
	}
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM question_regions WHERE question_id = $1`, questionID); err != nil {
			return err
		}
		return insertRegions(ctx, tx, questionID, regions)
	})
	if err != nil {
		return Question{}, err
	}
	return s.GetQuestion(ctx, questionID)
}
