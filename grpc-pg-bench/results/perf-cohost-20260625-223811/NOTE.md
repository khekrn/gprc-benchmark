# SUPERSEDED — do not cite these numbers

This is the **first** (contaminated) cache-miss / context-switch A/B run. It is
kept for the record only; **use [`../perf-cohost2-20260625-224525/`](../perf-cohost2-20260625-224525/FINDINGS.md)
instead** — that run supersedes this one.

Why this run is not trustworthy:
- 12s warmup (too short): the `base` cell logged **2,134 cold-start errors**.
- The `nohdr` cell ran at an anomalous **12,276 rps vs 19,149** for the others —
  a ~36% gap far too large for the object-header flag under test, i.e. background
  box noise, not the variable. Cells were therefore at very different operating
  points and not comparable.

The clean v2 re-run fixed this: 30s warmup (0 errors), cooldown-until-quiet
between cells (matched ~18k rps across all cells), 40s perf window, and a
`base`/`base2` repeat that agrees to 0.25%.
