// Package falsesharing demonstrates the cost of false sharing (§3.4): several
// goroutines updating *distinct* counters that happen to live on the *same*
// 64-byte cache line, forcing the cores' caches to ping-pong ownership.
//
// Measure it:
//
//	go test -bench=. -benchmem ./ch03/falsesharing
//
// BenchmarkFalseSharing packs the counters together (false sharing).
// BenchmarkPadded gives each counter its own cache line (no sharing).
// The only difference is padding, yet the padded version is several times
// faster — that gap IS false sharing.
package falsesharing

import (
	"sync"
	"sync/atomic"
	"testing"
)

const (
	workers    = 8
	iterations = 2_000_000
)

// packed: 8 int64 counters sit in 64 bytes — i.e. ONE cache line. Eight cores
// hammering this one line invalidate each other's caches constantly.
type packed struct {
	counts [workers]int64
}

func BenchmarkFalseSharing(b *testing.B) {
	for i := 0; i < b.N; i++ {
		var c packed
		var wg sync.WaitGroup
		wg.Add(workers)
		for g := 0; g < workers; g++ {
			go func(idx int) {
				defer wg.Done()
				for j := 0; j < iterations; j++ {
					atomic.AddInt64(&c.counts[idx], 1)
				}
			}(g)
		}
		wg.Wait()
	}
}

// padded: each counter is padded to a full 64-byte cache line, so no two
// counters share a line and the cores never contend.
type padded struct {
	count int64
	_     [56]byte // 8 (int64) + 56 = 64 bytes = one cache line
}

func BenchmarkPadded(b *testing.B) {
	for i := 0; i < b.N; i++ {
		c := make([]padded, workers)
		var wg sync.WaitGroup
		wg.Add(workers)
		for g := 0; g < workers; g++ {
			go func(idx int) {
				defer wg.Done()
				for j := 0; j < iterations; j++ {
					atomic.AddInt64(&c[idx].count, 1)
				}
			}(g)
		}
		wg.Wait()
	}
}
