package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/FancyFunction/homesink/backend/internal/core"
	"github.com/FancyFunction/homesink/backend/internal/store"
)

// TestEnqueueJobRejectsAnUnknownKind guards the jobs.kind CHECK: an unrecognised
// kind would sit in the queue forever because no worker claims it.
func TestEnqueueJobRejectsAnUnknownKind(t *testing.T) {
	s := newSQLiteStore(t)

	if _, err := s.EnqueueJob(context.Background(),
		store.NewJob{Kind: store.JobKind("reticulate"), Payload: "{}"}); err == nil {
		t.Fatal("enqueuing an unknown job kind succeeded, want the CHECK to reject it")
	}
}

// TestEnqueueJobHonoursAnExplicitPriority: the kind defaults exist for
// convenience (D-32), not to override a caller that has a reason.
func TestEnqueueJobHonoursAnExplicitPriority(t *testing.T) {
	ctx := context.Background()

	for _, impl := range implementations() {
		t.Run(impl.name, func(t *testing.T) {
			s := impl.open(t)
			id, err := s.EnqueueJob(ctx, store.NewJob{
				Kind: store.JobTranscode, Payload: "{}", Priority: 1,
			})
			if err != nil {
				t.Fatalf("enqueue: %v", err)
			}
			job, err := s.JobByID(ctx, id)
			if err != nil {
				t.Fatalf("job by id: %v", err)
			}
			if job.Priority != 1 {
				t.Fatalf("priority = %d, want 1", job.Priority)
			}
		})
	}
}

// TestConcurrentClaimGivesEachJobToExactlyOneWorker is the property the worker
// pool depends on (D-32): the select and the state change share one write
// transaction, so two workers can never both run the same transcode.
func TestConcurrentClaimGivesEachJobToExactlyOneWorker(t *testing.T) {
	const (
		jobs    = 40
		workers = 8
	)

	ctx := context.Background()
	s := newSQLiteStore(t)
	for range jobs {
		if _, err := s.EnqueueJob(ctx, store.NewJob{Kind: store.JobThumbnail, Payload: "{}"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		claims = map[int64]int{}
	)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				job, err := s.ClaimJob(ctx)
				if errors.Is(err, core.ErrNotFound()) {
					return
				}
				if err != nil {
					t.Errorf("claim: %v", err)
					return
				}
				mu.Lock()
				claims[job.JobID]++
				mu.Unlock()
				if err := s.CompleteJob(ctx, job.JobID); err != nil {
					t.Errorf("complete: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if len(claims) != jobs {
		t.Fatalf("%d of %d jobs were claimed", len(claims), jobs)
	}
	for id, n := range claims {
		if n != 1 {
			t.Errorf("job %d was claimed %d times, want once", id, n)
		}
	}
	done, err := s.JobsByState(ctx, store.JobDone, 0)
	if err != nil {
		t.Fatalf("jobs by state: %v", err)
	}
	if len(done) != jobs {
		t.Fatalf("%d jobs are done, want %d", len(done), jobs)
	}
}

// TestFailJobBacksOffToTheGivenInstant: the backoff schedule belongs to the
// worker pool; the store's job is to honour the instant it is handed and keep the
// job out of the queue until then.
func TestFailJobBacksOffToTheGivenInstant(t *testing.T) {
	ctx := context.Background()

	for _, impl := range implementations() {
		t.Run(impl.name, func(t *testing.T) {
			s := impl.open(t)
			id, err := s.EnqueueJob(ctx, store.NewJob{Kind: store.JobTranscode, Payload: "{}"})
			if err != nil {
				t.Fatalf("enqueue: %v", err)
			}
			if _, err := s.ClaimJob(ctx); err != nil {
				t.Fatalf("claim: %v", err)
			}
			if err := s.FailJob(ctx, id, "ffmpeg died", testNowMs+30_000, 3); err != nil {
				t.Fatalf("fail job: %v", err)
			}

			job, err := s.JobByID(ctx, id)
			if err != nil {
				t.Fatalf("job by id: %v", err)
			}
			if job.State != store.JobQueued {
				t.Fatalf("state = %s, want queued", job.State)
			}
			if job.NextAttemptAtMs != testNowMs+30_000 {
				t.Fatalf("next_attempt_at = %d, want %d", job.NextAttemptAtMs, testNowMs+30_000)
			}
			if _, err := s.ClaimJob(ctx); !errors.Is(err, core.ErrNotFound()) {
				t.Fatalf("a backed-off job must not be claimable yet, got %v", err)
			}
		})
	}
}
