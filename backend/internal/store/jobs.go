package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// JobKind mirrors the jobs.kind CHECK constraint.
type JobKind string

// Job kinds (D-31, D-32).
const (
	JobThumbnail JobKind = "thumbnail"
	JobTranscode JobKind = "transcode"
	JobProbe     JobKind = "probe"
)

// JobState mirrors the jobs.state CHECK constraint.
type JobState string

// Job states. A job is running only while a worker holds it; RequeueRunningJobs
// resets the ones a crash left behind (D-32).
const (
	JobQueued  JobState = "queued"
	JobRunning JobState = "running"
	JobDone    JobState = "done"
	JobFailed  JobState = "failed"
)

// Default priorities: lower runs first, so thumbnails outrank transcodes and the
// browse screen fills while a 20-minute video encode is still going (D-32).
const (
	PriorityThumbnail = 10
	PriorityTranscode = 100
)

// NewJob is the enqueue payload. Priority 0 means "use the default for this
// kind"; Payload is JSON owned by the job's handler.
type NewJob struct {
	Kind     JobKind
	Payload  string
	Priority int
	// NextAttemptAtMs delays the first attempt; 0 means immediately.
	NextAttemptAtMs int64
}

// Job is one queue row.
type Job struct {
	JobID           int64
	Kind            JobKind
	Payload         string
	Priority        int
	State           JobState
	Attempts        int
	NextAttemptAtMs int64
	LastError       string
	CreatedAt       int64
	UpdatedAt       int64
}

// jobColumns is the read projection, kept in one place so every scanner agrees.
const jobColumns = `job_id, kind, payload, priority, state, attempts, next_attempt_at,
	last_error, created_at, updated_at`

// EnqueueJob appends a queued job and returns its id.
func (s *SQLite) EnqueueJob(ctx context.Context, j NewJob) (int64, error) {
	now := s.now()
	priority := j.Priority
	if priority == 0 {
		priority = defaultPriority(j.Kind)
	}

	var jobID int64
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO jobs (kind, payload, priority, state, attempts, next_attempt_at,
				created_at, updated_at)
			 VALUES (?, ?, ?, ?, 0, ?, ?, ?)`,
			string(j.Kind), j.Payload, priority, string(JobQueued), j.NextAttemptAtMs, now, now)
		if err != nil {
			return fmt.Errorf("store: enqueue job: %w", err)
		}
		jobID, err = res.LastInsertId()
		if err != nil {
			return fmt.Errorf("store: enqueue job: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return jobID, nil
}

// ClaimJob atomically takes the highest-priority due job, marks it running and
// counts the attempt. It returns core.ErrNotFound when nothing is due, which is
// the worker pool's idle signal rather than a failure.
//
// Selection and update share one write transaction, so two workers can never
// claim the same row.
func (s *SQLite) ClaimJob(ctx context.Context) (*Job, error) {
	now := s.now()
	var job *Job
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx,
			`SELECT `+jobColumns+` FROM jobs
			 WHERE state = ? AND next_attempt_at <= ?
			 ORDER BY priority, next_attempt_at, job_id
			 LIMIT 1`, string(JobQueued), now)
		claimed, err := scanJob(row)
		if err != nil {
			return notFound(err)
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE jobs SET state = ?, attempts = attempts + 1, updated_at = ? WHERE job_id = ?`,
			string(JobRunning), now, claimed.JobID); err != nil {
			return fmt.Errorf("store: claim job: %w", err)
		}
		claimed.State = JobRunning
		claimed.Attempts++
		claimed.UpdatedAt = now
		job = claimed
		return nil
	})
	if err != nil {
		return nil, err
	}
	return job, nil
}

// CompleteJob marks a job done and clears any error from an earlier attempt.
func (s *SQLite) CompleteJob(ctx context.Context, jobID int64) error {
	now := s.now()
	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE jobs SET state = ?, last_error = NULL, updated_at = ? WHERE job_id = ?`,
			string(JobDone), now, jobID)
		if err != nil {
			return fmt.Errorf("store: complete job: %w", err)
		}
		return requireOneRow(res, "job")
	})
}

// FailJob records an attempt's failure. The job goes back to queued with
// next_attempt_at set by the caller's backoff while attempts are left, and
// becomes failed once maxAttempts is reached — three, per D-32, after which it
// stays visible on the admin endpoint instead of retrying forever.
func (s *SQLite) FailJob(ctx context.Context, jobID int64, cause string,
	nextAttemptAtMs int64, maxAttempts int) error {
	now := s.now()
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var attempts int
		if err := tx.QueryRowContext(ctx, `SELECT attempts FROM jobs WHERE job_id = ?`, jobID).
			Scan(&attempts); err != nil {
			return notFound(err)
		}

		state := JobQueued
		next := nextAttemptAtMs
		if attempts >= maxAttempts {
			state = JobFailed
			next = 0
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE jobs SET state = ?, last_error = ?, next_attempt_at = ?, updated_at = ?
			 WHERE job_id = ?`,
			string(state), cause, next, now, jobID); err != nil {
			return fmt.Errorf("store: fail job: %w", err)
		}
		return nil
	})
}

// JobByID returns one job, or core.ErrNotFound.
func (s *SQLite) JobByID(ctx context.Context, jobID int64) (*Job, error) {
	row := s.read.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE job_id = ?`, jobID)
	job, err := scanJob(row)
	if err != nil {
		return nil, notFound(err)
	}
	return job, nil
}

// JobsByState lists jobs in one state, oldest first. limit ≤ 0 means no limit.
// It is how the admin endpoint shows what has given up (D-32).
func (s *SQLite) JobsByState(ctx context.Context, state JobState, limit int) ([]Job, error) {
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE state = ? ORDER BY job_id`
	args := []any{string(state)}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list jobs: %w", err)
	}
	return out, nil
}

// RequeueRunningJobs resets every job left running by a crash back to queued and
// returns how many it moved. The daemon calls it once at startup: a running row
// with no worker behind it would otherwise never be picked up again (D-32).
//
// The attempt count is deliberately left alone, so a job that reliably kills the
// process still reaches maxAttempts instead of looping forever.
func (s *SQLite) RequeueRunningJobs(ctx context.Context) (int, error) {
	now := s.now()
	var moved int
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE jobs SET state = ?, updated_at = ? WHERE state = ?`,
			string(JobQueued), now, string(JobRunning))
		if err != nil {
			return fmt.Errorf("store: requeue running jobs: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: requeue running jobs: %w", err)
		}
		moved = int(n)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return moved, nil
}

func defaultPriority(kind JobKind) int {
	if kind == JobThumbnail {
		return PriorityThumbnail
	}
	return PriorityTranscode
}

func scanJob(sc scanner) (*Job, error) {
	var (
		job       Job
		kind      string
		state     string
		lastError sql.NullString
	)
	if err := sc.Scan(&job.JobID, &kind, &job.Payload, &job.Priority, &state, &job.Attempts,
		&job.NextAttemptAtMs, &lastError, &job.CreatedAt, &job.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("store: scan job: %w", err)
	}
	job.Kind = JobKind(kind)
	job.State = JobState(state)
	job.LastError = lastError.String
	return &job, nil
}
