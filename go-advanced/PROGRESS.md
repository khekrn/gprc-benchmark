# Progress & Checkpoint Log

This file is the **single source of truth** for where the book stands across sessions.
Update it at the end of every working session. Newest session at the top.

## Status board

| # | Chapter | Status |
|---|---------|--------|
| 1 | The Go Runtime & Scheduler (GMP) | ✅ Done |
| 2 | Channels, Synchronization & Concurrency Patterns | ✅ Done |
| 3 | The Memory Model & AMD64 Hardware | ✅ Done |
| 4 | Lock-Free Programming in Go | ⬜ Not started |
| 5 | Mastering `context` | ⬜ Not started |
| 6 | Profiling with pprof & Flame Graphs | ⬜ Not started |
| 7 | The 1 Billion Row Challenge | ⬜ Not started |
| 8 | Embedding Assembly in Go | ⬜ Not started |
| 9 | I/O and Binary Encoding | ⬜ Not started |
| 10 | Building Bitcask | ⬜ Not started |
| 11 | `net/http` Internals & Production REST with Fiber | ⬜ Not started |
| 12 | gRPC End-to-End with buf.build | ⬜ Not started |
| 13 | Fiber + gRPC Together | ⬜ Not started |
| 14 | PostgreSQL with `jackc/pgx` | ⬜ Not started |
| 15 | SOLID & Practical Production Design Patterns | ⬜ Not started |
| 16 | Building a Production Service | ⬜ Not started |
| 17 | Capstone: Single-Node Message Queue | ⬜ Not started |

Legend: ✅ done · 🚧 in progress · ⬜ not started

---

## Session log

### Session 1 — 2026-06-16
- Set up book skeleton: `README.md` (full TOC + conventions), this `PROGRESS.md`, `chapters/`, runnable `code/` module.
- Wrote **Chapter 1 — The Go Runtime & Scheduler (GMP)** + runnable code in `code/ch01/`.
- Wrote **Chapter 2 — Channels, Synchronization & Concurrency Patterns** + runnable code in `code/ch02/`.
- Wrote **Chapter 3 — The Memory Model & AMD64 Hardware** + runnable code in `code/ch03/`
  (false-sharing bench → measured ~32× slowdown; atomic-vs-mutex bench; struct alignment; `-race` publication demo).
  Cache numbers read live from this machine's sysfs (64B lines; 32K L1/512K L2 per core shared by SMT siblings; 16M L3).
- Verified the whole `code/` module builds, vets, and tests pass on Go 1.26 / linux-amd64; every prose number is from an actual run.

**Next session — start here:**
- Write **Chapter 4 — Lock-Free Programming in Go**. Foundation is fully in place (Ch 3 delivered
  CAS, the publication guarantee, cache-line/false-sharing awareness, happens-before). Cover: CAS &
  the ABA problem, `atomic.Pointer[T]`, a Treiber stack, an SPSC ring buffer, a sharded counter
  (false-sharing avoidance on purpose), MPSC/MPMC queues — AND a clear "when a plain mutex still wins"
  section with a benchmark. Ch 3's closing paragraph already promises exactly these structures.
- After Ch 4, **Chapter 5 — Mastering `context`** closes Part I.

---

## Design decisions log

Record cross-cutting choices here so future sessions stay consistent.

- **D1 — Diagrams:** Mermaid for architecture/flow/state-machines; ASCII for byte-level memory layouts. Both render on GitHub.
- **D2 — One code module:** all runnable code lives under `code/` as a single Go module (`goadvanced`), one package per chapter subdir. Keeps imports/tooling simple.
- **D3 — Concrete hardware:** all measurements target the AMD Ryzen 5 7535HS (6c/12t, 1 NUMA node), Go 1.26, Linux amd64. Numbers are illustrative; the *shape* is the lesson.
- **D4 — Build order is strictly bottom-up.** Storage (Bitcask, Ch 10) precedes services (Ch 11–14) because the capstone queue (Ch 17) needs both. I/O/binary (Ch 9) comes before storage.
- **D5 — House style:** follow `.agents/skills/golang-*` conventions (structured concurrency, ctx-first, explicit channel ownership). Reference them from prose where relevant.
- **D6 — Scope per session:** ~2 deep chapters per session, each with runnable + verified code. Don't rush breadth at the cost of depth.
