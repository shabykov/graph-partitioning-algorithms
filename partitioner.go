// Package partitioning Package partition splits a large bipartite worker–task graph into
// sub-graphs small enough for an independent matching run.
//
// Two modes are supported:
//
//   - EdgeCut: every worker and every task ends up in exactly one sub-graph.
//     Edges whose endpoints land in different sub-graphs are returned in
//     Result.Cut and are not part of any sub-graph.
//   - UniqueTasks: every task ends up in exactly one sub-graph; a worker is
//     copied into every sub-graph that holds at least one of its tasks.
//     No edge is lost.
//
// Both modes share one pipeline: prune thin edges → split into connected
// components → recursively bisect components that exceed MaxEdges
// (BFS ordering → cut at half the edges → Fiduccia–Mattheyses refinement).
// The result is fully deterministic for a given input set of edges,
// regardless of input order.
package partitioning

// Part is one sub-graph ready for matching.
type Part struct {
	Edges   []Edge
	Tasks   int // distinct tasks in Edges
	Workers int // distinct workers in Edges
}

// Result of Partition.
type Result struct {
	// Parts are the sub-graphs. Every part has at least one edge.
	Parts []Part
	// Cut holds the dropped crossing edges (EdgeCut only; empty otherwise).
	Cut []Edge
	// CutCost is the sum of scores of Cut (EdgeCut) or the sum of scores of
	// worker edges that sit outside the worker's home part (UniqueTasks).
	// A worker's home part is the one with the largest sum of its scores.
	CutCost float64
	// Pruned is the number of edges removed by Theta (duplicates included).
	Pruned int
	// Oversized counts parts that exceed MaxEdges because they could not be
	// split further (a single task with more than MaxEdges edges, for example).
	Oversized int
}

// Partition splits edges into sub-graphs of at most cfg.MaxEdges edges.
// The input does not need to be connected; components are detected.
// Duplicate (Task, Worker) pairs are merged, keeping the highest score.
func Partition(cfg Config, edges []Edge) Result {
	cfg = cfg.withDefaults()
	if cfg.MaxEdges <= 0 {
		panic("partition: MaxEdges must be > 0")
	}
	g, pruned := buildGraph(edges, cfg.Theta)
	res := Result{Pruned: pruned}
	if len(g.edges) == 0 {
		return res
	}
	p := newPartitioner(g, cfg)
	all := make([]int32, 0, g.nT+g.nW)
	if cfg.Mode == UniqueTasks {
		for v := int32(0); v < int32(g.nT); v++ {
			all = append(all, v)
		}
	} else {
		for v := int32(0); v < int32(g.nT+g.nW); v++ {
			all = append(all, v)
		}
	}
	p.process(all, p.newSet(all))
	res.Oversized = p.oversized
	p.assemble(&res)
	return res
}

// ---------------------------------------------------------------------------
// Partitioner state

type partitioner struct {
	g   *graph
	cfg Config

	setID   []int32 // current working set of a vertex (-1 = none)
	region  []int32 // final part of a vertex (-1 = none)
	nextSet int32
	nRegion int32

	oversized int

	// scratch
	mark  []int32 // stamp per vertex
	stamp int32
	side  []int8
	lock  []bool
	gain  []float64
	ver   []uint32
	queue []int32
	// UniqueTasks: per-worker score sums on side A / side B
	sA, sB []float64
	// dirty-task batching inside one FM move
	dirtyMark []uint32
	dirtyCnt  uint32
	dirty     []int32
}

func newPartitioner(g *graph, cfg Config) *partitioner {
	n := g.nT + g.nW
	p := &partitioner{g: g, cfg: cfg}
	p.setID = make([]int32, n)
	p.region = make([]int32, n)
	for i := range p.setID {
		p.setID[i], p.region[i] = -1, -1
	}
	p.mark = make([]int32, n)
	p.side = make([]int8, n)
	p.lock = make([]bool, n)
	p.gain = make([]float64, n)
	p.ver = make([]uint32, n)
	p.queue = make([]int32, 0, n)
	if cfg.Mode == UniqueTasks {
		p.sA = make([]float64, g.nW)
		p.sB = make([]float64, g.nW)
		p.dirtyMark = make([]uint32, g.nT)
	}
	return p
}

// nonImprovingLimit bounds how many consecutive moves without a new best
// cut an FM pass may make before it stops (classic FM would move every
// vertex once; the tail almost never helps and dominates the run time).
func nonImprovingLimit(n int) int { return max(32, n/20) }

func (p *partitioner) newSet(vs []int32) int32 {
	id := p.nextSet
	p.nextSet++
	for _, v := range vs {
		p.setID[v] = id
	}
	return id
}

func (p *partitioner) nextStamp() int32 {
	p.stamp++
	return p.stamp
}

// inSet reports whether vertex u belongs to the working set id.
// In UniqueTasks only tasks carry set ids.
func (p *partitioner) inSet(u, id int32) bool { return p.setID[u] == id }

// process recursively splits the vertex set until every piece fits.
func (p *partitioner) process(set []int32, id int32) {
	comps := p.components(set, id)
	for _, comp := range comps {
		cid := id
		if len(comps) > 1 {
			cid = p.newSet(comp)
		}
		edges := p.countEdges(comp, cid)
		if edges == 0 {
			continue // isolated vertices after a cut: nothing to match
		}
		if edges <= p.cfg.MaxEdges {
			p.emit(comp)
			continue
		}
		a, b := p.bisect(comp, cid, edges)
		if len(a) == 0 || len(b) == 0 {
			p.oversized++
			p.emit(comp)
			continue
		}
		ia := p.newSet(a)
		ib := p.newSet(b)
		p.process(a, ia)
		p.process(b, ib)
	}
}

func (p *partitioner) emit(vs []int32) {
	r := p.nRegion
	p.nRegion++
	for _, v := range vs {
		p.region[v] = r
	}
}

// countEdges returns the number of edges the set would bring to a part.
func (p *partitioner) countEdges(set []int32, id int32) int {
	g := p.g
	n := 0
	if p.cfg.Mode == UniqueTasks {
		for _, t := range set {
			n += g.deg(t)
		}
		return n
	}
	for _, v := range set {
		if int(v) >= g.nT {
			continue // count from the task side only
		}
		adj, _, _ := g.nbrs(v)
		for _, u := range adj {
			if p.inSet(u, id) {
				n++
			}
		}
	}
	return n
}

// components splits the set into connected components (BFS, deterministic).
func (p *partitioner) components(set []int32, id int32) [][]int32 {
	st := p.nextStamp()
	var comps [][]int32
	for _, s := range set {
		if p.mark[s] == st {
			continue
		}
		comp := p.bfs(s, id, st, nil)
		comps = append(comps, comp)
	}
	return comps
}

// weightedDeg is the sum of scores of a vertex within the working set.
func (p *partitioner) weightedDeg(v, id int32) float64 {
	g := p.g
	adj, w, _ := g.nbrs(v)
	s := 0.0
	if p.cfg.Mode == UniqueTasks {
		for _, x := range w {
			s += float64(x)
		}
		return s
	}
	for i, u := range adj {
		if p.inSet(u, id) {
			s += float64(w[i])
		}
	}
	return s
}

// bisect splits a connected set into two halves balanced by edge count,
// then refines the cut. Returns the two vertex lists.
func (p *partitioner) bisect(comp []int32, id int32, total int) ([]int32, []int32) {
	g := p.g
	// 1. start vertex: max weighted degree, ties → lowest index
	start := comp[0]
	best := -1.0
	for _, v := range comp {
		d := p.weightedDeg(v, id)
		if d > best || (d == best && v < start) {
			best, start = d, v
		}
	}
	// 2. BFS order
	order := p.bfs(start, id, p.nextStamp(), make([]int32, 0, len(comp)))

	// 3. initial cut: prefix of the order holding ~half of the edges
	for _, v := range comp {
		p.side[v] = 1
	}
	eA := 0
	cut := len(order)
	for i, v := range order {
		if p.cfg.Mode == UniqueTasks {
			eA += g.deg(v)
		} else {
			adj, _, _ := g.nbrs(v)
			for _, u := range adj {
				if p.inSet(u, id) && p.side[u] == 0 {
					eA++
				}
			}
		}
		p.side[v] = 0
		if 2*eA >= total {
			cut = i + 1
			break
		}
	}
	if cut >= len(order) { // degenerate: fall back to splitting by count
		cut = len(order) / 2
		for _, v := range order[cut:] {
			p.side[v] = 1
		}
	}

	// 4. refinement
	if p.cfg.FMPasses > 0 {
		if p.cfg.Mode == UniqueTasks {
			p.refineUniqueTasks(comp, id, total)
		} else {
			p.refineEdgeCut(comp, id, total)
		}
	}

	a := make([]int32, 0, cut)
	b := make([]int32, 0, len(comp)-cut)
	for _, v := range order { // BFS order keeps output deterministic
		if p.side[v] == 0 {
			a = append(a, v)
		} else {
			b = append(b, v)
		}
	}
	return a, b
}
