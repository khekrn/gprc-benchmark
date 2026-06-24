// Package leak contains a canonical goroutine leak (§2.7) and its fix, so the
// accompanying test can demonstrate go.uber.org/goleak catching the leak and
// passing on the fix.
package leak

import (
	"context"
	"time"
)

// expensive simulates a slow computation.
func expensive() int {
	time.Sleep(20 * time.Millisecond)
	return 42
}

// Leaky LEAKS a goroutine: it launches a goroutine that blocks on an unbuffered
// send. If the caller stops receiving (e.g. it timed out and returned), nobody
// ever reads from ch, so the goroutine is stuck on `ch <- ...` forever.
func Leaky() <-chan int {
	ch := make(chan int) // unbuffered
	go func() {
		ch <- expensive() // blocks forever if no receiver
	}()
	return ch
}

// Fixed never leaks. Two defenses combined:
//   - a buffer of 1 so the send completes even with no reader, and
//   - a select on ctx.Done() so cancellation always provides an exit.
func Fixed(ctx context.Context) <-chan int {
	ch := make(chan int, 1)
	go func() {
		select {
		case ch <- expensive():
		case <-ctx.Done(): // caller gave up — exit cleanly instead of blocking
		}
	}()
	return ch
}
