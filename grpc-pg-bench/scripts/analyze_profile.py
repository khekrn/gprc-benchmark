#!/usr/bin/env python3
"""Summarize an async-profiler *collapsed* (folded-stack) file.

Collapsed format (one line per unique stack):
    frame1;frame2;...;leafFrame <sampleCount>
The leaf frame is where the sample was actually taken, so summing sample
counts grouped by leaf gives **self time**. Summing by a substring match over
the whole stack gives a coarse **subsystem** breakdown.

This tool is deliberately simple and transparent so the numbers can be audited:
the only "magic" is the SUBSYS substring table below — adjust it if a frame is
miscategorised.

Usage: analyze_profile.py <file.collapsed> [topN]
"""
import sys
import collections

path = sys.argv[1]
TOP = int(sys.argv[2]) if len(sys.argv) > 2 else 25

# (label, substrings) — first matching label wins, evaluated top-to-bottom.
SUBSYS = [
    ("GC",            ("ZHeap", "ZAddress", "ZPage", "ZBarrier", "ZMark", "ZRelocate",
                       "zgc", "/gc/", "G1", "ParallelGC", "CollectedHeap", "GCTaskThread")),
    ("Netty",         ("io.netty", "io/netty", "epoll", "EventLoop", "NioEvent")),
    ("protobuf",      ("protobuf", "CodedOutput", "CodedInput")),
    ("gRPC",          ("io.grpc", "io/grpc")),
    ("pgjdbc",        ("org.postgresql", "postgresql")),
    ("HikariCP",      ("com.zaxxer.hikari", "ConcurrentBag", "HikariPool", "HikariProxy")),
    ("VT/scheduler",  ("VirtualThread", "jdk.internal.vm", "Continuation", "ForkJoinPool",
                       "Poller", "java.lang.VirtualThread")),
    ("Spring/JDBC",   ("springframework", "java.sql", "jdbc")),
    ("bench/FNV",     ("com.beam.bench", "Fnv")),
    ("JVM runtime",   ("[J", "libjvm", "Interpreter", "CompilerThread", "OptoRuntime",
                       "SharedRuntime", "JavaCalls", "C2Compiler", "C1_")),
]

self_t = collections.Counter()
subtot = collections.Counter()
total = 0

with open(path) as f:
    for line in f:
        line = line.rstrip("\n")
        if not line:
            continue
        try:
            stack, cnt = line.rsplit(" ", 1)
            cnt = int(cnt)
        except ValueError:
            continue
        total += cnt
        self_t[stack.split(";")[-1]] += cnt
        label = "other"
        for name, subs in SUBSYS:
            if any(s in stack for s in subs):
                label = name
                break
        subtot[label] += cnt

if total == 0:
    print("no samples")
    sys.exit(0)

print(f"total samples: {total:,}\n")
print("== subsystem breakdown (share of samples whose stack touches that subsystem) ==")
for label, c in subtot.most_common():
    print(f"  {100*c/total:5.1f}%  {label}")
print(f"\n== top {TOP} self-time leaf frames ==")
for leaf, c in self_t.most_common(TOP):
    print(f"  {100*c/total:5.1f}%  {leaf[:96]}")
