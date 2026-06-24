// Package atomicvsmutex compares the three ways to maintain a shared counter
// under contention (§3.6): a mutex, a raw sync/atomic call, and the Go 1.19+
// typed atomic.Int64. All use GOMAXPROCS goroutines via RunParallel.
//
//	go test -bench=. -benchmem ./ch03/atomicvsmutex
//
// Takeaway: for a single shared counter the atomic is markedly cheaper than the
// mutex (no parking, one locked instruction) — but BOTH bottleneck on the same
// cache line, which is exactly why §3.4 (false sharing) and Ch 4 (lock-free
// sharding) matter when you need to scale past one hot word.
package atomicvsmutex

import (
	"sync"
	"sync/atomic"
	"testing"
)

func BenchmarkMutexCounter(b *testing.B) {
	var mu sync.Mutex
	var n int64
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			mu.Lock()
			n++
			mu.Unlock()
		}
	})
	_ = n
}

func BenchmarkAtomicRaw(b *testing.B) {
	var n int64
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			atomic.AddInt64(&n, 1)
		}
	})
	_ = n
}

func BenchmarkAtomicTyped(b *testing.B) {
	var n atomic.Int64
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n.Add(1)
		}
	})
	_ = n.Load()
}
