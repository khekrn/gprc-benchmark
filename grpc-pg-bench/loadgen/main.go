// loadgen is a closed-loop gRPC load generator shared by both server stacks.
//
// It keeps N concurrent workers, each firing RPCs back-to-back for a fixed
// duration, then reports throughput and latency percentiles. Using one
// client for every server means we measure the *servers*, not two clients.
//
// Modes:
//   execute  (default) — single autocommit INSERT per call
//   exectx             — multi-statement transaction per call (3 INSERTs)
//   mixed              — per-iteration coin flip: ExecuteTx vs GetState
//                        with `-read-pct` reads (default 20%)
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
	mode := flag.String("mode", "execute", "workload: execute | exectx | mixed")
	readPct := flag.Int("read-pct", 20, "percent reads in mixed mode (0..100)")
	keyspace := flag.Int("keyspace", 10000, "per-worker workflow_id pool size")
	flag.Parse()

	switch *mode {
	case "execute", "exectx", "mixed":
	default:
		fmt.Fprintf(os.Stderr, "invalid -mode %q (use execute|exectx|mixed)\n", *mode)
		os.Exit(2)
	}
	if *readPct < 0 || *readPct > 100 {
		fmt.Fprintf(os.Stderr, "invalid -read-pct %d\n", *readPct)
		os.Exit(2)
	}

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

	cfg := phaseConfig{
		clients:     clients,
		conc:        *conc,
		payloadSize: *payloadSize,
		mode:        *mode,
		readPct:     *readPct,
		keyspace:    *keyspace,
	}

	fmt.Fprintf(os.Stderr, "[%s] warmup %s @ concurrency %d mode=%s ...\n",
		*label, *warmup, *conc, *mode)
	cfg.dur = *warmup
	cfg.record = false
	runPhase(cfg)

	fmt.Fprintf(os.Stderr, "[%s] measuring %s @ concurrency %d mode=%s ...\n",
		*label, *dur, *conc, *mode)
	cfg.dur = *dur
	cfg.record = true
	res := runPhase(cfg)
	res.Label = *label
	res.Addr = *addr
	res.Concurrency = *conc
	res.Connections = *conns
	res.PayloadBytes = *payloadSize
	res.Mode = *mode
	res.ReadPct = *readPct
	res.Keyspace = *keyspace

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
// Top-level p50/p99/etc are combined across all ops (writes + reads).
// Per-op breakdown lives in the Write* / Read* fields.
type Result struct {
	Label        string  `json:"label"`
	Addr         string  `json:"addr"`
	Concurrency  int     `json:"concurrency"`
	Connections  int     `json:"connections"`
	PayloadBytes int     `json:"payload_bytes"`
	Mode         string  `json:"mode"`
	ReadPct      int     `json:"read_pct"`
	Keyspace     int     `json:"keyspace"`
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

	// Per-op breakdown. In mode=execute or mode=exectx every op is a write,
	// so Write* mirrors top-level and Read* is zero.
	TotalOKWrite int64   `json:"total_ok_write"`
	TotalOKRead  int64   `json:"total_ok_read"`
	WriteRPS     float64 `json:"write_rps"`
	ReadRPS      float64 `json:"read_rps"`
	WriteP50Ms   float64 `json:"write_p50_ms"`
	WriteP90Ms   float64 `json:"write_p90_ms"`
	WriteP99Ms   float64 `json:"write_p99_ms"`
	WriteP999Ms  float64 `json:"write_p999_ms"`
	WriteMaxMs   float64 `json:"write_max_ms"`
	ReadP50Ms    float64 `json:"read_p50_ms"`
	ReadP90Ms    float64 `json:"read_p90_ms"`
	ReadP99Ms    float64 `json:"read_p99_ms"`
	ReadP999Ms   float64 `json:"read_p999_ms"`
	ReadMaxMs    float64 `json:"read_max_ms"`
}

type phaseConfig struct {
	clients     []benchv1.CommandServiceClient
	conc        int
	dur         time.Duration
	payloadSize int
	mode        string
	readPct     int
	keyspace    int
	record      bool
}

func runPhase(cfg phaseConfig) Result {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.dur)
	defer cancel()

	var okWrite, okRead, errCount int64
	// Per-worker latency slices avoid lock contention; merged at the end.
	writeLat := make([][]float64, cfg.conc)
	readLat := make([][]float64, cfg.conc)

	// Realistic command-type mix so the server's hot path sees variety.
	cmdTypes := []string{"approve", "reject", "submit", "review", "execute", "cancel", "retry", "complete"}

	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < cfg.conc; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cl := cfg.clients[id%len(cfg.clients)]
			rng := rand.New(rand.NewSource(int64(id)*1_000_003 + time.Now().UnixNano()))
			// Per-worker scratch buffer reused across requests — avoids
			// allocating a fresh []byte every call, while still rewriting
			// the bytes per request so CPU/PG caches can't help the server.
			buf := make([]byte, cfg.payloadSize)
			var wLat, rLat []float64
			if cfg.record {
				wLat = make([]float64, 0, 1<<16)
				rLat = make([]float64, 0, 1<<14)
			}
			var seq int64
			for {
				select {
				case <-ctx.Done():
					writeLat[id] = wLat
					readLat[id] = rLat
					return
				default:
				}
				seq++

				// Decide op for this iteration. In mixed mode, treat -read-pct
				// as the percentage of reads; everything else is a write.
				isRead := cfg.mode == "mixed" && rng.Intn(100) < cfg.readPct

				// Workflow id: bounded per-worker keyspace so:
				//  - ExecuteTx's UPSERT exercises both INSERT and UPDATE paths
				//    once each worker has wrapped around its keyspace
				//  - GetState in mixed mode hits known keys after warmup
				wfID := fmt.Sprintf("wf-%d-%d", id, rng.Intn(cfg.keyspace))

				t0 := time.Now()
				var err error
				if isRead {
					_, err = cl.GetState(ctx, &benchv1.GetStateRequest{WorkflowId: wfID})
				} else {
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
					req := &benchv1.CommandRequest{
						WorkflowId:  wfID,
						CommandType: cmdTypes[rng.Intn(len(cmdTypes))],
						Payload:     string(buf),
						Seq:         seq,
					}
					if cfg.mode == "exectx" || cfg.mode == "mixed" {
						_, err = cl.ExecuteTx(ctx, req)
					} else {
						_, err = cl.Execute(ctx, req)
					}
				}
				elapsedMs := float64(time.Since(t0).Microseconds()) / 1000.0

				if err != nil {
					// Ignore the inevitable deadline-exceeded at phase end.
					if ctx.Err() == nil {
						atomic.AddInt64(&errCount, 1)
					}
					continue
				}
				if isRead {
					atomic.AddInt64(&okRead, 1)
					if cfg.record {
						rLat = append(rLat, elapsedMs)
					}
				} else {
					atomic.AddInt64(&okWrite, 1)
					if cfg.record {
						wLat = append(wLat, elapsedMs)
					}
				}
			}
		}(w)
	}
	wg.Wait()
	elapsed := time.Since(start)

	totalOK := atomic.LoadInt64(&okWrite) + atomic.LoadInt64(&okRead)
	res := Result{
		DurationSec:  elapsed.Seconds(),
		TotalOK:      totalOK,
		TotalOKWrite: atomic.LoadInt64(&okWrite),
		TotalOKRead:  atomic.LoadInt64(&okRead),
		TotalErr:     atomic.LoadInt64(&errCount),
	}
	res.RPS = float64(res.TotalOK) / elapsed.Seconds()
	res.WriteRPS = float64(res.TotalOKWrite) / elapsed.Seconds()
	res.ReadRPS = float64(res.TotalOKRead) / elapsed.Seconds()

	if cfg.record {
		// Combined (all ops) percentiles for top-level fields.
		var all []float64
		for _, s := range writeLat {
			all = append(all, s...)
		}
		for _, s := range readLat {
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

		// Per-op percentiles.
		var allWrites []float64
		for _, s := range writeLat {
			allWrites = append(allWrites, s...)
		}
		if len(allWrites) > 0 {
			sort.Float64s(allWrites)
			res.WriteP50Ms = pct(allWrites, 50)
			res.WriteP90Ms = pct(allWrites, 90)
			res.WriteP99Ms = pct(allWrites, 99)
			res.WriteP999Ms = pct(allWrites, 99.9)
			res.WriteMaxMs = allWrites[len(allWrites)-1]
		}
		var allReads []float64
		for _, s := range readLat {
			allReads = append(allReads, s...)
		}
		if len(allReads) > 0 {
			sort.Float64s(allReads)
			res.ReadP50Ms = pct(allReads, 50)
			res.ReadP90Ms = pct(allReads, 90)
			res.ReadP99Ms = pct(allReads, 99)
			res.ReadP999Ms = pct(allReads, 99.9)
			res.ReadMaxMs = allReads[len(allReads)-1]
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
