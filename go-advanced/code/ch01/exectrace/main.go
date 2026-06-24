// Command exectrace produces a runtime/trace file you can open with
// `go tool trace` to *see* the scheduler: which goroutine ran on which P,
// work-stealing, GC, and syscall/network blocking.
//
//	go run ./ch01/exectrace      # writes trace.out
//	go tool trace trace.out      # opens the interactive web UI
//
// In the UI, explore "Goroutine analysis" and the per-proc timeline.
package main

import (
	"fmt"
	"os"
	"runtime/trace"
	"sync"
)

func main() {
	f, err := os.Create("trace.out")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create trace:", err)
		os.Exit(1)
	}
	defer f.Close()

	if err := trace.Start(f); err != nil {
		fmt.Fprintln(os.Stderr, "start trace:", err)
		os.Exit(1)
	}
	defer trace.Stop()

	// A mix of work: fan out CPU-bound tasks plus some that hand off via a
	// channel, giving the tracer interesting scheduling events to show.
	const workers = 24
	results := make(chan int, workers)

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(seed int) {
			defer wg.Done()
			sum := 0
			for i := 0; i < 5_000_000; i++ {
				sum += (i ^ seed) & 0xff
			}
			results <- sum
		}(w)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	total := 0
	for r := range results {
		total += r
	}

	fmt.Printf("total=%d\n", total)
	fmt.Println("wrote trace.out — open with: go tool trace trace.out")
}
