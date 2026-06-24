// Command racedemo shows what "publication" means in the Go memory model
// (§3.7): one goroutine writes data and then sets a flag; another waits on the
// flag and reads the data. Without a happens-before edge between the two
// goroutines, there is NO guarantee the reader sees the write — it's a data
// race (undefined behavior), even though on x86 it often "looks" fine.
//
// Run the BUGGY version under the race detector to see it caught:
//
//	go run -race ./ch03/racedemo            # default: buggy, -race reports it
//	go run -race ./ch03/racedemo -fixed     # atomic flag: no race, correct
//
// The fix uses atomic.Bool: a paired atomic Store (writer) and Load==true
// (reader) establishes happens-before, so the data write is guaranteed visible.
package main

import (
	"flag"
	"fmt"
	"sync/atomic"
	"time"
)

func main() {
	fixed := flag.Bool("fixed", false, "use the atomic (correct) version")
	flag.Parse()

	if *fixed {
		runFixed()
	} else {
		runBuggy()
	}
}

// runBuggy: `ready` and `data` are plain variables shared across goroutines
// with no synchronization. This is a data race; -race will flag both accesses.
func runBuggy() {
	var data int
	var ready bool

	go func() {
		data = 42    // (1) produce
		ready = true // (2) publish — but nothing orders (1) before the reader's view
	}()

	// Bounded spin so the demo always terminates even if visibility never
	// propagates (which it can't be relied upon to do — that's the point).
	deadline := time.Now().Add(time.Second)
	for !ready {
		if time.Now().After(deadline) {
			fmt.Println("buggy: timed out waiting for ready (no happens-before)")
			return
		}
	}
	fmt.Printf("buggy: saw data=%d (no guarantee this is 42)\n", data)
}

// runFixed: the flag is an atomic.Bool. The writer's atomic Store and the
// reader's atomic Load that observes true together create a happens-before
// edge, so the plain `data` write made BEFORE the Store is guaranteed visible
// AFTER the Load. No race, deterministically correct.
func runFixed() {
	var data int
	var ready atomic.Bool

	go func() {
		data = 42         // ordered-before the Store below
		ready.Store(true) // release: publishes everything sequenced before it
	}()

	for !ready.Load() { // acquire: once we see true, we see data=42
	}
	fmt.Printf("fixed: saw data=%d (guaranteed 42)\n", data)
}
