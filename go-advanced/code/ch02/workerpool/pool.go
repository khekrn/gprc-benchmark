// Package workerpool implements the bounded worker-pool pattern from §2.6:
// a fixed set of N workers pulling jobs from a channel, with every blocking
// operation paired against ctx.Done() so no worker can ever leak.
package workerpool

import (
	"context"
	"sync"
)

// Job is a unit of work. Result carries its outcome.
type Job struct {
	ID    int
	Input int
}

type Result struct {
	JobID  int
	Output int
}

// Run starts `workers` goroutines that consume jobs and emit results. It
// returns the results channel, which is closed exactly once after all workers
// have exited (drained jobs or cancelled). The caller must range over the
// returned channel to completion (or cancel ctx) to avoid blocking workers.
func Run(ctx context.Context, jobs <-chan Job, workers int, process func(Job) Result) <-chan Result {
	results := make(chan Result)

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return // cancellation — clean exit
				case job, ok := <-jobs:
					if !ok {
						return // jobs drained — clean exit
					}
					// Pair the result send with ctx.Done() so a stalled or
					// gone consumer can never block this worker forever.
					select {
					case results <- process(job):
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}

	// A single owner closes results once every worker has returned.
	go func() {
		wg.Wait()
		close(results)
	}()

	return results
}
