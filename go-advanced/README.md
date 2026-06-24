# Go, Deep: Building Production Systems from the Runtime Up

> An advanced Go book that goes from the scheduler and CPU cache lines all the way
> up to a production message queue — every concept paired with a solid diagram and
> runnable, production-quality code.

This is not a "learn the syntax" book. It assumes you can already write Go and want
to understand **how it actually works underneath**, and how to use that understanding
to build fast, correct, production-grade systems.

---

## Who this is for

You know Go. You can write a goroutine, a channel, an HTTP handler. Now you want to:

- Reason about the **GMP scheduler**, the **memory model**, and **AMD64 hardware** the way the runtime authors do.
- Write **lock-free** data structures and know when *not* to.
- Find and kill bottlenecks with **pprof**, **flame graphs**, and **`perf`** — proven on the 1 Billion Row Challenge.
- Drop into **Plan 9 assembly** when the compiler isn't enough.
- Build **production services**: `net/http`/Fiber, gRPC with buf, PostgreSQL with `pgx`.
- Build real **storage and messaging engines** from first principles: a Bitcask KV store and a single-node message queue.

## Target environment

Everything is written and measured against a concrete machine so the numbers mean something:

| | |
|---|---|
| **CPU** | AMD Ryzen 5 7535HS (Zen 3+, "Rembrandt-R") |
| **Topology** | 6 physical cores · 2 SMT threads/core · **12 logical CPUs** · single NUMA node |
| **Caches** | per-core L1 + L2, shared L3 |
| **Go** | 1.26.x |
| **OS** | Linux, x86-64 |

When a chapter says "run this," it was run here. Your numbers will differ; the *shape* of the results won't.

---

## How the book is organized

The book builds strictly bottom-up. Each part assumes the ones before it.

### Part I — Foundations: Runtime, Concurrency & Hardware
1. **The Go Runtime & Scheduler (GMP)** — goroutines, M:N scheduling, work-stealing, preemption, the netpoller, `sysmon`. *(✅ written)*
2. **Channels, Synchronization & Concurrency Patterns** — `hchan` internals, `select`, the `sync` package, pipelines, worker pools, `errgroup`, leak prevention. *(✅ written)*
3. **The Memory Model & AMD64 Hardware** — cache hierarchy, cache lines, false sharing, x86-TSO memory ordering, `sync/atomic`, happens-before. *(✅ written)*
4. **Lock-Free Programming in Go** — CAS, ABA, atomic pointers, lock-free stacks/queues/ring buffers, and when locks win.
5. **Mastering `context`** — propagation, cancellation, deadlines, values done right, `WithoutCancel`, leak debugging.

### Part II — Performance Engineering
6. **Profiling with pprof & Flame Graphs** — CPU/heap/block/mutex/goroutine profiles, `fgprof`, reading flame graphs, continuous profiling.
7. **The 1 Billion Row Challenge** — from a naive solution to a great one, with `perf`, hardware counters, and every optimization measured.
8. **Embedding Assembly in Go** — Plan 9 asm, the calling convention, `go:noescape`, SIMD, and avoiding it correctly.

### Part III — I/O, Binary & Storage
9. **I/O and Binary Encoding** — `io` interfaces, buffering, `encoding/binary`, zero-copy, `mmap` (and when to avoid it).
10. **Building Bitcask** — a production log-structured key/value store from scratch.

### Part IV — Networking & Services
11. **`net/http` Internals & Production REST with Fiber.**
12. **gRPC End-to-End with buf.build** — unary, server/client/bidi streaming, codegen, interceptors.
13. **Fiber + gRPC Together** — one service exposing both protocols cleanly.
14. **PostgreSQL with `jackc/pgx`** for production — pooling, prepared statements, `COPY`, `LISTEN/NOTIFY`, transactions.

### Part V — Production Systems & Design
15. **SOLID & Practical Production Design Patterns** (the ones that actually pay off in Go).
16. **Building a Production Service** — Fiber + gRPC + `pgx`, structured logging, config, context, graceful shutdown.
17. **Capstone: A Single-Node High-Performance Message Queue** — a RESP/NATS-style broker tying together storage, concurrency, and networking.

---

## How to read a chapter

Every chapter follows the same rhythm:

1. **The mental model** — a diagram and the one-paragraph "what's really happening."
2. **The mechanism** — how the runtime/library/hardware implements it, with source-level detail.
3. **The code** — runnable programs under [`code/`](./code), not just snippets.
4. **Measure it** — we run it and look at the numbers.
5. **Pitfalls & production notes** — what breaks at 3am.
6. **Summary + what's next.**

### Running the code

All runnable code lives in one module under [`code/`](./code):

```bash
cd go-advanced/code
go run ./ch01/scheduler-trace      # example
go test -bench=. ./ch02/...
```

Each chapter's prose links to the exact programs it discusses.

---

## Conventions

- **Mermaid** diagrams for architecture, flows, and state machines (render natively on GitHub).
- **ASCII** diagrams for memory layouts, struct fields, and cache lines (where precise byte alignment matters).
- Code follows the `samber/cc-skills-golang` house style installed in this repo (`.agents/skills/`): structured concurrency, explicit channel ownership, `ctx context.Context` first, errors wrapped, no naked `go func()` without a defined exit.
- ⚠️ marks a footgun. 🔬 marks a "measure it yourself" experiment. 📐 marks a design decision with tradeoffs.

See **[PROGRESS.md](./PROGRESS.md)** for the running checkpoint: what's done, what's next, and the design decisions log across sessions.
