package workerpool

import (
	"context"
	"runtime"
	"testing"
)

func TestRunProcessesAllJobs(t *testing.T) {
	ctx := context.Background()

	const n = 1000
	jobs := make(chan Job)
	go func() {
		defer close(jobs) // producer owns jobs and closes it
		for i := 0; i < n; i++ {
			jobs <- Job{ID: i, Input: i}
		}
	}()

	square := func(j Job) Result {
		return Result{JobID: j.ID, Output: j.Input * j.Input}
	}

	got := make(map[int]int, n)
	for r := range Run(ctx, jobs, runtime.GOMAXPROCS(0), square) {
		got[r.JobID] = r.Output
	}

	if len(got) != n {
		t.Fatalf("got %d results, want %d", len(got), n)
	}
	for i := 0; i < n; i++ {
		if got[i] != i*i {
			t.Fatalf("job %d: got %d, want %d", i, got[i], i*i)
		}
	}
}

func TestRunHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	jobs := make(chan Job) // never closed; only cancellation stops the pool
	go func() {
		for i := 0; ; i++ {
			select {
			case jobs <- Job{ID: i, Input: i}:
			case <-ctx.Done():
				return
			}
		}
	}()

	results := Run(ctx, jobs, 4, func(j Job) Result {
		return Result{JobID: j.ID, Output: j.Input}
	})

	// Consume a few, then cancel and confirm the channel drains and closes
	// (workers exited rather than leaking).
	for i := 0; i < 10; i++ {
		<-results
	}
	cancel()

	for range results {
		// drain remaining in-flight results until close
	}
}
