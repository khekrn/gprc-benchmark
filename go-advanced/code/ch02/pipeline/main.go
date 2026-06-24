// Command pipeline demonstrates a classic Go pipeline with three concurrent
// stages plus a cancellation-aware merge, illustrating two key ideas from §2.4:
//
//   - each stage owns and closes its own output channel, so closes cascade;
//   - setting a drained input channel to nil disables its select case.
//
//	go run ./ch02/pipeline
package main

import (
	"context"
	"fmt"
)

// send writes v to out unless ctx is cancelled first. Returns false on cancel
// so callers can stop cleanly instead of blocking forever on a dead consumer.
func send(ctx context.Context, out chan<- int, v int) bool {
	select {
	case out <- v:
		return true
	case <-ctx.Done():
		return false
	}
}

// generate emits 1..n then closes its output.
func generate(ctx context.Context, n int) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out) // owner closes
		for i := 1; i <= n; i++ {
			if !send(ctx, out, i) {
				return
			}
		}
	}()
	return out
}

// square squares each input value.
func square(ctx context.Context, in <-chan int) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out)
		for v := range in { // exits when `in` is closed upstream
			if !send(ctx, out, v*v) {
				return
			}
		}
	}()
	return out
}

// merge fans two streams into one, honoring cancellation and draining both.
func merge(ctx context.Context, a, b <-chan int) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out)
		for a != nil || b != nil {
			select {
			case v, ok := <-a:
				if !ok {
					a = nil // drained: disable this case (nil channel blocks forever)
					continue
				}
				if !send(ctx, out, v) {
					return
				}
			case v, ok := <-b:
				if !ok {
					b = nil
					continue
				}
				if !send(ctx, out, v) {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Two independent squaring pipelines, merged.
	s1 := square(ctx, generate(ctx, 5))   // 1,4,9,16,25
	s2 := square(ctx, generate(ctx, 3))   // 1,4,9

	sum := 0
	for v := range merge(ctx, s1, s2) {
		fmt.Printf("%d ", v)
		sum += v
	}
	fmt.Printf("\nsum = %d\n", sum) // 55 + 14 = 69
}
