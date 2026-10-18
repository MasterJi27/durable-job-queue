package postgres

import (
	"context"
	"durablequeue/queue"
	"errors"
	"github.com/jackc/pgx/v5"
)

func (s *Store) Result(ctx context.Context, id string) (queue.Result, error) {
	var r queue.Result
	err := s.pool.QueryRow(ctx, "SELECT job_id,value,committed_at FROM dq_results WHERE job_id=$1", id).Scan(&r.JobID, &r.Value, &r.CommittedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = queue.ErrNotFound
	}
	return r, err
}

func (s *Store) Inspect(ctx context.Context, q string, status queue.Status, search string, offset, limit int) (queue.Inspection, error) {
	out := queue.Inspection{Queue: q, Offset: offset, Limit: limit, Jobs: []queue.Job{}}
	if offset < 0 || limit < 1 || limit > 100 {
		return out, errors.New("offset must be nonnegative and limit 1..100")
	}
	// Keep statistics, filtered count, and the page in one read snapshot while
	// workers continue to commit. No row locks or write transactions are needed.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer rollback(tx)
	err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE status='pending'),count(*) FILTER(WHERE status='running'),count(*) FILTER(WHERE status='succeeded'),count(*) FILTER(WHERE status='dead'),clock_timestamp() FROM dq_jobs WHERE queue=$1`, q).Scan(&out.Stats.Total, &out.Stats.Pending, &out.Stats.Running, &out.Stats.Succeeded, &out.Stats.Dead, &out.ServerTime)
	if err != nil {
		return out, err
	}
	const where = ` FROM dq_jobs WHERE queue=$1 AND ($2='' OR status=$2) AND ($3='' OR strpos(lower(id||' '||kind||' '||idempotency_key),lower($3))>0)`
	if err = tx.QueryRow(ctx, "SELECT count(*)"+where, q, string(status), search).Scan(&out.Filtered); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, "SELECT "+columns+where+" ORDER BY created_at DESC,id DESC LIMIT $4 OFFSET $5", q, string(status), search, limit, offset)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		j, e := scan(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Jobs = append(out.Jobs, j)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
