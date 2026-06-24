// Command errgroup-fetch demonstrates structured concurrency with
// golang.org/x/sync/errgroup (§2.6, Pattern 4): a bounded group of tasks where
// the FIRST error cancels the shared context so siblings stop early.
//
// To stay runnable offline, "fetch" is simulated work rather than real HTTP.
//
//	go run ./ch02/errgroup-fetch
package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	"golang.org/x/sync/errgroup"
)

// fetch simulates a request that takes some time and may fail. It respects
// ctx: if the group is cancelled (because a sibling failed), it returns early.
func fetch(ctx context.Context, id int) (string, error) {
	work := time.Duration(20*(id+1)) * time.Millisecond

	select {
	case <-time.After(work):
		// Pretend resource #3 is broken to trigger group cancellation.
		if id == 3 {
			return "", fmt.Errorf("resource %d: %w", id, errors.New("503 unavailable"))
		}
		return fmt.Sprintf("payload-%d (%v)", id, work), nil
	case <-ctx.Done():
		return "", ctx.Err() // a sibling failed, or deadline hit — bail out
	}
}

func main() {
	parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	g, ctx := errgroup.WithContext(parent)
	g.SetLimit(runtime.GOMAXPROCS(0)) // bound concurrency to CPU count

	results := make([]string, 8)
	for id := 0; id < len(results); id++ {
		id := id
		g.Go(func() error {
			payload, err := fetch(ctx, id)
			if err != nil {
				return err // cancels ctx -> siblings observe ctx.Done()
			}
			results[id] = payload // each goroutine writes a disjoint index (no race)
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		fmt.Printf("group failed with first error: %v\n", err)
	} else {
		fmt.Println("all fetches succeeded")
	}

	for id, r := range results {
		if r == "" {
			fmt.Printf("  #%d: <cancelled or failed>\n", id)
			continue
		}
		fmt.Printf("  #%d: %s\n", id, r)
	}
}
