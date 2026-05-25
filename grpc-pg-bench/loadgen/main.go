// loadgen is a closed-loop gRPC load generator shared by both server stacks.
//
// It keeps N concurrent workers, each firing Execute calls back-to-back for a
// fixed duration, then reports throughput and latency percentiles. Using one
// client for both servers means we measure the *servers*, not two clients.
//
// Flags let you point it at either server and control concurrency/duration so
// the same binary drives every run in the benchmark matrix.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	benchv1 "github.com/beam/grpc-pg-bench/gen/benchv1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:50051", "gRPC server address")
	conc := flag.Int("c", 64, "concurrent workers (in-flight requests)")
	dur := flag.Duration("d", 30*time.Second, "measured duration")
	warmup := flag.Duration("warmup", 5*time.Second, "warmup duration (not measured)")
	payloadSize := flag.Int("payload", 256, "payload size in bytes")
	conns := flag.Int("conns", 4, "number of gRPC client connections (channels)")
	label := flag.String("label", "", "label for the result (e.g. go-pgx, kotlin-vertx)")
	out := flag.String("out", "", "optional path to write JSON result")
	flag.Parse()

	// Build a small pool of connections; gRPC multiplexes many streams per
	// connection but multiple channels avoid a single-conn bottleneck.
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	channels := make([]*grpc.ClientConn, *conns)
	for i := range channels {
		cc, err := grpc.NewClient(*addr, dialOpts...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dial: %v\n", err)
			os.Exit(1)
		}
		defer cc.Close()
		channels[i] = cc
	}
	clients := make([]benchv1.CommandServiceClient, *conns)
	for i, cc := range channels {
		clients[i] = benchv1.NewCommandServiceClient(cc)
	}

	// ---- Warmup (also primes prepared-statement caches and pools) ----
	fmt.Fprintf(os.Stderr, "[%s] warmup %s @ concurrency %d ...\n", *label, *warmup, *conc)
	runPhase(clients, *conc, *warmup, *payloadSize, false)

	// ---- Measured phase ----
	fmt.Fprintf(os.Stderr, "[%s] measuring %s @ concurrency %d ...\n", *label, *dur, *conc)
	res := runPhase(clients, *conc, *dur, *payloadSize, true)
	res.Label = *label
	res.Addr = *addr
	res.Concurrency = *conc
	res.Connections = *conns
	res.PayloadBytes = *payloadSize

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(res)

	if *out != "" {
		f, err := os.Create(*out)
		if err == nil {
			je := json.NewEncoder(f)
			je.SetIndent("", "  ")
			_ = je.Encode(res)
			_ = f.Close()
		}
	}
}

// Result is the JSON-serialized outcome of a measured phase.
type Result struct {
	Label        string  `json:"label"`
	Addr         string  `json:"addr"`
	Concurrency  int     `json:"concurrency"`
	Connections  int     `json:"connections"`
	PayloadBytes int     `json:"payload_bytes"`
	DurationSec  float64 `json:"duration_sec"`
	TotalOK      int64   `json:"total_ok"`
	TotalErr     int64   `json:"total_err"`
	RPS          float64 `json:"rps"`
	LatMeanMs    float64 `json:"lat_mean_ms"`
	LatP50Ms     float64 `json:"lat_p50_ms"`
	LatP90Ms     float64 `json:"lat_p90_ms"`
	LatP99Ms     float64 `json:"lat_p99_ms"`
	LatP999Ms    float64 `json:"lat_p999_ms"`
	LatMaxMs     float64 `json:"lat_max_ms"`
}

func runPhase(clients []benchv1.CommandServiceClient, conc int, dur time.Duration, payloadSize int, record bool) Result {
	ctx, cancel := context.WithTimeout(context.Background(), dur)
	defer cancel()

	var okCount, errCount int64
	// Per-worker latency slices avoid lock contention; merged at the end.
	perWorker := make([][]float64, conc)

	// Realistic command-type mix so the server's hot path sees variety.
	cmdTypes := []string{"approve", "reject", "submit", "review", "execute", "cancel", "retry", "complete"}

	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < conc; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cl := clients[id%len(clients)]
			rng := rand.New(rand.NewSource(int64(id)*1_000_003 + time.Now().UnixNano()))
			// Per-worker scratch buffer reused across requests — avoids
			// allocating a fresh []byte every call, while still rewriting
			// the bytes per request so CPU/PG caches can't help the server.
			buf := make([]byte, payloadSize)
			var lat []float64
			if record {
				lat = make([]float64, 0, 1<<16)
			}
			var seq int64
			for {
				select {
				case <-ctx.Done():
					perWorker[id] = lat
					return
				default:
				}
				seq++
				// Fresh per-request payload. The proto field is `string`,
				// which proto3 requires to be valid UTF-8 — random bytes
				// would fail marshal validation. So we map random bits onto
				// 64 printable ASCII characters (6 bits/byte), still enough
				// entropy to defeat the CPU/L3 + PG buffer caches.
				const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
				for i := 0; i < len(buf); i += 10 {
					u := rng.Uint64()
					end := i + 10
					if end > len(buf) {
						end = len(buf)
					}
					for j := i; j < end; j++ {
						buf[j] = alphabet[u&0x3f]
						u >>= 6
					}
				}
				// Workflow IDs from a wide keyspace (~2B) so the PG btree
				// index doesn't reduce to a hot-page cache, and so the
				// server's checksum + insert see varied input.
				req := &benchv1.CommandRequest{
					WorkflowId:  fmt.Sprintf("wf-%d-%010d", id, rng.Uint32()),
					CommandType: cmdTypes[rng.Intn(len(cmdTypes))],
					Payload:     string(buf),
					Seq:         seq,
				}
				t0 := time.Now()
				_, err := cl.Execute(ctx, req)
				elapsedMs := float64(time.Since(t0).Microseconds()) / 1000.0
				if err != nil {
					// Ignore the inevitable deadline-exceeded at phase end.
					if ctx.Err() == nil {
						atomic.AddInt64(&errCount, 1)
					}
					continue
				}
				atomic.AddInt64(&okCount, 1)
				if record {
					lat = append(lat, elapsedMs)
				}
			}
		}(w)
	}
	wg.Wait()
	elapsed := time.Since(start)

	res := Result{
		DurationSec: elapsed.Seconds(),
		TotalOK:     atomic.LoadInt64(&okCount),
		TotalErr:    atomic.LoadInt64(&errCount),
	}
	res.RPS = float64(res.TotalOK) / elapsed.Seconds()

	if record {
		var all []float64
		for _, s := range perWorker {
			all = append(all, s...)
		}
		if len(all) > 0 {
			sort.Float64s(all)
			var sum float64
			for _, v := range all {
				sum += v
			}
			res.LatMeanMs = sum / float64(len(all))
			res.LatP50Ms = pct(all, 50)
			res.LatP90Ms = pct(all, 90)
			res.LatP99Ms = pct(all, 99)
			res.LatP999Ms = pct(all, 99.9)
			res.LatMaxMs = all[len(all)-1]
		}
	}
	return res
}

// pct returns the p-th percentile from a pre-sorted slice (nearest-rank).
func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(p/100*float64(len(sorted)+1)) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}
