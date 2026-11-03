# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

Single Go package `partitioning` (module `github.com/shabykov/graph-partitioning-algorithms`), no external dependencies.

```
go test ./...                              # full suite, ~0.3 s
go test -run TestExampleEdgeCut ./...      # single test
go test -run '^$' -bench . -benchmem ./... # benchmarks only (~97k edges, both modes)
go vet ./...
```

## What this is

Splits a large weighted bipartite graph (tasks ↔ workers, edge score in `[0, 1]`) into sub-graphs of at most `Config.MaxEdges` edges, each of which is sent to matchmaking independently on a scheduler tick. `README.md` (in Russian) is the design spec: algorithm, guarantees, worked example (§8) and tuning guidance. When the code changes, keep it in sync: API (§2), guarantees (§6), test table (§9) and file layout (§10).

Two modes share one pipeline and differ only in which vertices refinement moves and how cut cost is computed:
- `EdgeCut` — tasks and workers each land in exactly one part; crossing edges are dropped into `Result.Cut`.
- `UniqueTasks` — only tasks are partitioned; a worker is replicated into every part holding one of its tasks. Cost = Σ scores of a worker's edges outside its "home" part (the part with the largest Σ of its scores).

## Architecture

Pipeline: `NewGraph` (dedupe by pointer → CSR, built once and immutable, reusable across `Partition` calls) → connected components → each component either emitted (≤ `MaxEdges`) or bisected and recursed on. The number of parts `k` is never configured; it falls out of `MaxEdges`.

- `graph.go` — `Task`/`Worker`/`Edge`, exported `Graph` and `NewGraph`/`buildGraph`: sort by ID, dedupe edges with the same `(*Task, *Worker)` pair (keep max score), build CSR. Vertex identity is the pointer, so callers must share one `*Task`/`*Worker` per entity; ordering is by ID only (never by address) to stay deterministic. Vertex indexing: tasks are `0..nT-1`, workers `nT..nT+nW-1`, in sorted-ID order. There is no score pruning inside the package.
- `config.go` — `Mode`, `Config`, `withDefaults` (Epsilon 0.1, FMPasses 3; `FMPasses < 0` disables refinement).
- `partitioner.go` — `Partition` entry point and `partitioner` state. `process` recurses: `components` → `emit` or `bisect`. Working sets are tracked by `setID[v]` (each bisection half gets a fresh id via `newSet`), final parts by `region[v]`. Scratch arrays are allocated once and reused across recursion; `mark`/`stamp` (`nextStamp`) avoid clearing visited arrays.
- `bfs.go` — deterministic BFS within a working set. In `UniqueTasks` the set contains only tasks and workers are transit vertices, each expanded once per traversal (stamp-marked) so hub workers aren't rescanned.
- `bisect` — start at max weighted degree (ties → lower index), BFS order, cut the prefix once it holds half the edges, then FM refinement.
- `refinement.go` — Fiduccia–Mattheyses with a lazy max-heap (stale entries invalidated via `ver[v]`), locking, best-prefix rollback, early stop after `nonImprovingLimit` non-improving moves, balance bound in `balanceLimit`. `refineEdgeCut` uses the classic ±2w gain update. `refineUniqueTasks` keeps per-worker side sums `sA/sB` (cost = Σ min(sA, sB)) and batches affected tasks through the `dirty` list so each re-enters the heap once per move.
- `assembly.go` — `assemble` builds `Parts`, `Cut`, `CutCost` from `region`.

## Invariants to preserve

- **Determinism**: the same edge set in any input order must give a byte-identical result (tasks must not jump between parts across ticks). Everything relies on sorted IDs → sorted adjacency → index-based tie-breaking. Don't introduce map iteration or unstable ordering into anything that affects output.
- Each task in exactly one part (both modes); each worker in exactly one part in `EdgeCut`.
- Edge conservation: `EdgeCut`: `Σ|Parts| + |Cut| = Graph.Len()`; `UniqueTasks`: `Cut` empty and `Σ|Parts| = Graph.Len()`.
- Every part ≤ `MaxEdges` unless counted in `Oversized` (e.g. a single task with degree > `MaxEdges` in `UniqueTasks`).

`checkInvariants` in `partitioner_test.go` asserts these; `TestExampleEdgeCut`/`TestExampleUniqueTasks` pin the README §8 worked example, so a change to tie-breaking or the initial cut will show up there. The `clustered(...)` generator builds graphs with a known natural cut for quality tests.
