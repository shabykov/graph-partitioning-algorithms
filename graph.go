package partitioning

import "sort"

// Edge is a weighted worker–task edge. Score is expected in [0, 1].
type Edge struct {
	Task   uint64
	Worker uint64
	Score  float32
}

// ---------------------------------------------------------------------------
// Graph (CSR over tasks 0..nT-1 and workers nT..nT+nW-1)

type graph struct {
	nT, nW    int
	taskIDs   []uint64
	workerIDs []uint64
	edges     []Edge // pruned, deduplicated, sorted by (Task, Worker)
	eTask     []int32
	eWorker   []int32
	off       []int32 // len nT+nW+1
	adj       []int32 // neighbor vertex index
	w         []float32
	eidx      []int32 // edge index for (vertex, neighbor)
}

func (g *graph) nbrs(v int32) (adj []int32, w []float32, eidx []int32) {
	a, b := g.off[v], g.off[v+1]
	return g.adj[a:b], g.w[a:b], g.eidx[a:b]
}

func (g *graph) deg(v int32) int { return int(g.off[v+1] - g.off[v]) }

func buildGraph(in []Edge, theta float32) (*graph, int) {
	es := make([]Edge, len(in))
	copy(es, in)
	sort.Slice(es, func(i, j int) bool {
		if es[i].Task != es[j].Task {
			return es[i].Task < es[j].Task
		}
		if es[i].Worker != es[j].Worker {
			return es[i].Worker < es[j].Worker
		}
		return es[i].Score > es[j].Score
	})
	// dedupe (keep max score, which sorts first) and prune
	out := es[:0]
	pruned := 0
	for i, e := range es {
		if i > 0 && e.Task == es[i-1].Task && e.Worker == es[i-1].Worker {
			pruned++
			continue
		}
		if e.Score < theta {
			pruned++
			continue
		}
		out = append(out, e)
	}
	es = out

	g := &graph{edges: es}
	tasks := make(map[uint64]int32, len(es))
	workers := make(map[uint64]int32, len(es))
	for _, e := range es {
		if _, ok := tasks[e.Task]; !ok {
			tasks[e.Task] = 0
			g.taskIDs = append(g.taskIDs, e.Task)
		}
		if _, ok := workers[e.Worker]; !ok {
			workers[e.Worker] = 0
			g.workerIDs = append(g.workerIDs, e.Worker)
		}
	}
	sort.Slice(g.workerIDs, func(i, j int) bool { return g.workerIDs[i] < g.workerIDs[j] })
	// taskIDs are already ascending because es is sorted by Task.
	for i, id := range g.taskIDs {
		tasks[id] = int32(i)
	}
	for i, id := range g.workerIDs {
		workers[id] = int32(i)
	}
	g.nT, g.nW = len(g.taskIDs), len(g.workerIDs)
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
	// worker by task index, because es is sorted by (Task, Worker) and
	// indices follow id order. This keeps BFS deterministic.
	return g, pruned
}
