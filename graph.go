package partitioning

import (
	"sort"
	"time"
)

type Properties struct {
	Name      string
	CreatedAt time.Time
}

type Task struct {
	ID         uint64
	Properties Properties
}

type Worker struct {
	ID         uint64
	Properties Properties
}

// Edge is a weighted worker–task edge. Score is expected in [0, 1].
type Edge struct {
	Task   *Task
	Worker *Worker
	Score  float32
}

// ---------------------------------------------------------------------------
// Graph (CSR over tasks 0..nT-1 and workers nT..nT+nW-1)

// Graph is an immutable, deduplicated worker–task graph ready for Partition.
// Build it with NewGraph; one Graph may be partitioned any number of times,
// including concurrently.
type Graph struct {
	nT, nW     int
	tasks      []*Task
	workers    []*Worker
	edges      []*Edge // deduplicated, sorted by (Task.ID, Worker.ID)
	duplicates int     // edges merged away by NewGraph
	eTask      []int32
	eWorker    []int32
	off        []int32 // len nT+nW+1
	adj        []int32 // neighbor vertex index
	w          []float32
	eidx       []int32 // edge index for (vertex, neighbor)
}

// NewGraph builds a graph from edges. Vertices are identified by pointer:
// every edge of a task must carry the same *Task, and likewise for workers.
// Edges with the same (Task, Worker) pointers are merged, keeping the highest
// score. The edges are referenced, not copied. It panics on an edge with a nil
// Task or Worker.
func NewGraph(edges []*Edge) *Graph { return buildGraph(edges) }

// Len returns the number of distinct edges.
func (g *Graph) Len() int { return len(g.edges) }

// Duplicates returns how many input edges NewGraph merged into another edge.
func (g *Graph) Duplicates() int { return g.duplicates }

func (g *Graph) nbrs(v int32) (adj []int32, w []float32, eidx []int32) {
	a, b := g.off[v], g.off[v+1]
	return g.adj[a:b], g.w[a:b], g.eidx[a:b]
}

func (g *Graph) deg(v int32) int { return int(g.off[v+1] - g.off[v]) }

func buildGraph(in []*Edge) *Graph {
	es := make([]*Edge, len(in))
	copy(es, in)
	for _, e := range es {
		if e == nil || e.Task == nil || e.Worker == nil {
			panic("partition: edge with nil Task or Worker")
		}
	}
	// Vertex identity is the pointer: edges sharing a Task (or Worker) must
	// share the same *Task (*Worker). Ordering is by ID, never by address,
	// so the result depends only on the edge set and not on the allocator.
	sort.SliceStable(es, func(i, j int) bool {
		a, b := es[i], es[j]
		if a.Task.ID != b.Task.ID {
			return a.Task.ID < b.Task.ID
		}
		if a.Worker.ID != b.Worker.ID {
			return a.Worker.ID < b.Worker.ID
		}
		return a.Score > b.Score
	})
	// dedupe: duplicates are edges with the same Task and Worker pointers;
	// the max score sorts first within an equal-ID run, so it is the one kept
	out := es[:0]
	dups := 0
	seen := make(map[[2]any]struct{}, len(es))
	for _, e := range es {
		key := [2]any{e.Task, e.Worker}
		if _, dup := seen[key]; dup {
			dups++
			continue
		}
		seen[key] = struct{}{}
		out = append(out, e)
	}
	es = out

	g := &Graph{edges: es, duplicates: dups}
	tasks := make(map[*Task]int32, len(es))
	workers := make(map[*Worker]int32, len(es))
	for _, e := range es {
		if _, ok := tasks[e.Task]; !ok {
			tasks[e.Task] = 0
			g.tasks = append(g.tasks, e.Task)
		}
		if _, ok := workers[e.Worker]; !ok {
			workers[e.Worker] = 0
			g.workers = append(g.workers, e.Worker)
		}
	}
	sort.SliceStable(g.workers, func(i, j int) bool { return g.workers[i].ID < g.workers[j].ID })
	// tasks are already ascending by ID because es is sorted by Task.ID.
	for i, t := range g.tasks {
		tasks[t] = int32(i)
	}
	for i, w := range g.workers {
		workers[w] = int32(i)
	}
	g.nT, g.nW = len(g.tasks), len(g.workers)
	n := g.nT + g.nW

	g.eTask = make([]int32, len(es))
	g.eWorker = make([]int32, len(es))
	g.off = make([]int32, n+1)
	for i, e := range es {
		t, w := tasks[e.Task], int32(g.nT)+workers[e.Worker]
		g.eTask[i], g.eWorker[i] = t, w
		g.off[t+1]++
		g.off[w+1]++
	}
	for v := 0; v < n; v++ {
		g.off[v+1] += g.off[v]
	}
	g.adj = make([]int32, 2*len(es))
	g.w = make([]float32, 2*len(es))
	g.eidx = make([]int32, 2*len(es))
	fill := make([]int32, n)
	copy(fill, g.off[:n])
	for i, e := range es {
		t, w := g.eTask[i], g.eWorker[i]
		pt, pw := fill[t], fill[w]
		g.adj[pt], g.w[pt], g.eidx[pt] = w, e.Score, int32(i)
		g.adj[pw], g.w[pw], g.eidx[pw] = t, e.Score, int32(i)
		fill[t]++
		fill[w]++
	}
	// Adjacency of a task is sorted by worker index and adjacency of a
	// worker by task index, because es is sorted by (Task.ID, Worker.ID) and
	// indices follow ID order. This keeps BFS deterministic.
	return g
}
