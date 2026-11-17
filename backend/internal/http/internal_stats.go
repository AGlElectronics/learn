package http

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

type aggregateMetric struct {
	Key   string  `json:"key"`
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

// internalStatsHandler serves GET /api/internal/stats for the Stats module.
// Authenticated by STATS_PULL_TOKEN; without a token the route 404s.
func internalStatsHandler(token string, pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token = strings.TrimSpace(token)
		if token == "" {
			http.NotFound(w, r)
			return
		}
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(token)) != 1 {
			httperror.Render(w, r, httperror.New(httperror.CodeUnauthorized, "invalid service token"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"schema_version": 1,
			"module":         "learn",
			"generated_at":   time.Now().UTC(),
			"metrics":        learnAggregateStats(r.Context(), pool),
		})
	}
}

func learnAggregateStats(ctx context.Context, pool *pgxpool.Pool) []aggregateMetric {
	metrics := make([]aggregateMetric, 0, 2)
	var courses, enrolments float64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*)::float8 FROM courses`).Scan(&courses); err != nil {
		courses = 0
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*)::float8 FROM enrolments`).Scan(&enrolments); err != nil {
		enrolments = 0
	}
	metrics = append(metrics,
		aggregateMetric{Key: "courses", Label: "Courses", Value: courses, Unit: "count"},
		aggregateMetric{Key: "enrolments", Label: "Enrolments", Value: enrolments, Unit: "count"},
	)
	return metrics
}
