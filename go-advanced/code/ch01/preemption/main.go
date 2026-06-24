// Command preemption demonstrates Go 1.14+ asynchronous (signal-based)
// preemption. With a single P and a goroutine spinning in a tight loop that
// never blocks, allocates, or calls a function, the only way `main` can run is
// if the runtime forcibly preempts the spinner via a SIGURG signal.
//
//	go run ./ch01/preemption
//	  -> prints "preempted: main ran"
//
// Disable async preemption to prove it is load-bearing — this HANGS forever,
// because a function-call-free loop has no cooperative preemption point
// (Ctrl-C, or it is killed by a timeout):
//
//	GODEBUG=asyncpreemptoff=1 go run ./ch01/preemption
package main

import (
	"fmt"
	"runtime"
	"time"
)

// sink is a package-level variable so the compiler cannot optimize the spin
// loop away, yet incrementing it involves NO function call — so the loop has no
// cooperative preemption point. Only async (signal-based) preemption can stop it.
var sink uint64

func main() {
	// One P: at most one goroutine runs Go code at a time. The spinner and main
	// must share it, so main only runs if the spinner is preempted.
	runtime.GOMAXPROCS(1)

	go func() {
		// A truly tight loop: no blocking, no allocation, no function call.
		// Cooperative preemption (function prologues) can never fire here.
		for {
			sink++
		}
	}()

	// Give the spinner a head start so it's definitely occupying the only P.
	time.Sleep(10 * time.Millisecond)

	// If we reach here, the runtime async-preempted the spinner to let us run.
	// (With GODEBUG=asyncpreemptoff=1 this line is never reached.)
	fmt.Println("preempted: main ran")

	// main returning exits the process, which tears down the still-spinning
	// goroutine — no clean shutdown needed for a demonstration.
}
