package learn

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// withJobLock runs fn while holding the session advisory lock name, so a
// background job runs on one replica at a time. When another replica holds
// the lock it returns (0, nil) without running fn.
func withJobLock(ctx context.Context, pool *pgxpool.Pool, name string, fn func() (int, error)) (int, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, name).Scan(&got); err != nil {
		return 0, err
	}
	if !got {
		return 0, nil
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, name)
	}()
	return fn()
}
