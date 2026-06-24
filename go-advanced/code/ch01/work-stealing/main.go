// Command work-stealing creates a deliberately lopsided burst of CPU-bound
// goroutines so you can watch the scheduler spread them across all P's.
//
// Run it with the scheduler trace on to see per-P local run-queue lengths
// even out over time as idle P's steal work from busy ones:
//
//	GODEBUG=schedtrace=1000 go run ./ch01/work-stealing
//
// In the trace, the bracketed list "[..]" is each P's local queue length and
// "runqueue=" is the global queue length. Watch them balance.
package main

import (
	"fmt"
	"sync"
	"time"
)

// spin burns CPU for roughly the given duration without ever blocking,
// allocating, or (after inlining) calling a function in the inner loop —
// exactly the kind of work the scheduler must actively balance and preempt.
func spin(d time.Duration) {
	deadline := time.Now().Add(d)
	x := 0
	for time.Now().Before(deadline) {
		// A small amount of arithmetic so the loop isn't optimized away.
		for i := 0; i < 1_000; i++ {
			x ^= i * i
		}
	}
	_ = x
}

func main() {
	const tasks = 2_000

	var wg sync.WaitGroup
	wg.Add(tasks)

	start := time.Now()

	// Spawn the whole burst from a single goroutine. Every `go` lands on the
	// *current* P's local queue first, so initially one P is overloaded and the
	// other 11 are empty — the worst case for load balancing, which the
	// work-stealer must fix. Watch idle P's steal half-queues from the busy one.
	for i := 0; i < tasks; i++ {
		go func() {
			defer wg.Done()
			spin(2 * time.Millisecond)
		}()
	}

	wg.Wait()
	fmt.Printf("Completed %d CPU-bound tasks in %v\n", tasks, time.Since(start))
	fmt.Println("Re-run with: GODEBUG=schedtrace=1000 go run ./ch01/work-stealing")
}
