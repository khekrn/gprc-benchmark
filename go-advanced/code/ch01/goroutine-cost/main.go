// Command goroutine-cost measures the steady-state memory cost of a goroutine
// by spawning a large number of them and reading the heap delta.
//
//	go run ./ch01/goroutine-cost
//
// Each goroutine parks on a channel (costing memory, not a thread — see §1.7),
// so this also demonstrates that a million *blocked* goroutines is fine.
package main

import (
	"fmt"
	"runtime"
	"sync"
)

const n = 1_000_000

func main() {
	var before, after runtime.MemStats

	runtime.GC()
	runtime.ReadMemStats(&before)

	// A channel every goroutine blocks on. Closing it at the end releases them.
	// Only the main goroutine ("the sender", here via close) touches release —
	// the worker goroutines are pure receivers, matching the house rule that
	// only the owner closes a channel.
	release := make(chan struct{})

	var started sync.WaitGroup
	started.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			started.Done()
			<-release // park here: _Gwaiting, no OS thread consumed
		}()
	}
	started.Wait() // ensure all n goroutines actually exist before measuring

	runtime.GC()
	runtime.ReadMemStats(&after)

	// Goroutine memory has two parts:
	//   - HeapAlloc: the g structs, sudog wait records, closures, etc.
	//   - StackInuse: the goroutine STACKS (not counted in HeapAlloc).
	// Count both for an honest per-goroutine figure.
	heapDelta := after.HeapAlloc - before.HeapAlloc
	stackDelta := after.StackInuse - before.StackInuse
	total := heapDelta + stackDelta

	fmt.Printf("Spawned %d goroutines (NumGoroutine=%d)\n", n, runtime.NumGoroutine())
	fmt.Printf("Heap   : %6.1f MB  ->  %6.1f MB  (Δ %5.1f MB)\n",
		float64(before.HeapAlloc)/1e6, float64(after.HeapAlloc)/1e6, float64(heapDelta)/1e6)
	fmt.Printf("Stacks : %6.1f MB  ->  %6.1f MB  (Δ %5.1f MB)\n",
		float64(before.StackInuse)/1e6, float64(after.StackInuse)/1e6, float64(stackDelta)/1e6)
	fmt.Printf("Total per goroutine: ~%.2f KB  (heap+stack)\n", float64(total)/float64(n)/1024)

	close(release) // owner releases all receivers
}
