package leak

import (
	"context"
	"testing"
	"time"

	"go.uber.org/goleak"
)

// TestMain wraps the whole package's tests with goleak: if any non-test
// goroutine is still running when the suite finishes, the run FAILS. This is
// how you assert leak-freedom in CI instead of eyeballing it.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// TestFixedDoesNotLeak consumes the result normally; the goroutine exits, so
// goleak is satisfied.
func TestFixedDoesNotLeak(t *testing.T) {
	ctx := context.Background()
	if got := <-Fixed(ctx); got != 42 {
		t.Fatalf("got %d, want 42", got)
	}
}

// TestFixedCancelDoesNotLeak abandons the result (never receives) but cancels
// the context. The goroutine takes the ctx.Done() branch and exits — no leak.
func TestFixedCancelDoesNotLeak(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	_ = Fixed(ctx) // intentionally do NOT receive
	cancel()
	// Give the goroutine a moment to observe cancellation and return before
	// goleak's end-of-suite check runs.
	time.Sleep(50 * time.Millisecond)
}

// TestLeakyLeaks is intentionally NOT included as a passing test: calling
// Leaky() and abandoning it would make goleak fail the whole suite (which is
// the point). To see goleak catch it, temporarily add:
//
//	func TestSeeTheLeak(t *testing.T) { _ = Leaky() }
//
// and run `go test ./ch02/leak/...` — the suite will fail with a goroutine
// stuck on `ch <- expensive()`.
