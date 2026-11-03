package partitioning

import (
	"container/heap"
	"math"
)

// ---------------------------------------------------------------------------
// Fiduccia–Mattheyses refinement

type heapItem struct {
	gain float64
	v    int32
	ver  uint32
}

type gainHeap []heapItem

func (h gainHeap) Len() int { return len(h) }
func (h gainHeap) Less(i, j int) bool {
	if h[i].gain != h[j].gain {
		return h[i].gain > h[j].gain
	}
	return h[i].v < h[j].v
}
func (h gainHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *gainHeap) Push(x interface{}) { *h = append(*h, x.(heapItem)) }
func (h *gainHeap) Pop() interface{} {
	old := *h
	it := old[len(old)-1]
	*h = old[:len(old)-1]
	return it
}

func (p *partitioner) balanceLimit(total, eA, eB int) int {
	bound := int(math.Ceil(float64(total) * (0.5 + p.cfg.Epsilon)))
	if m := max(eA, eB); m > bound {
		return m // never make an already unbalanced split worse
	}
	return bound
}

// refineEdgeCut moves vertices across the cut to minimise the sum of
// crossing scores while keeping edge counts within the balance limit.
func (p *partitioner) refineEdgeCut(comp []int32, id int32, total int) {
	g := p.g
	type move struct {
		v    int32
		dA   int // change of eA
		dB   int
		gain float64
	}
	for pass := 0; pass < p.cfg.FMPasses; pass++ {
		eA, eB := 0, 0
		cutCost := 0.0
		for _, v := range comp {
			p.lock[v] = false
			adj, w, _ := g.nbrs(v)
			gv := 0.0
			for i, u := range adj {
				if !p.inSet(u, id) {
					continue
				}
				if p.side[u] != p.side[v] {
					gv += float64(w[i])
					if int(v) < g.nT {
						cutCost += float64(w[i])
					}
				} else {
					gv -= float64(w[i])
					if int(v) < g.nT {
						if p.side[v] == 0 {
							eA++
						} else {
							eB++
						}
					}
				}
			}
			p.gain[v] = gv
		}
		h := make(gainHeap, 0, len(comp))
		for _, v := range comp {
			p.ver[v]++
			h = append(h, heapItem{p.gain[v], v, p.ver[v]})
		}
		heap.Init(&h)

		var moves []move
		bestCost, bestStep := cutCost, 0
		stale, staleLimit := 0, nonImprovingLimit(len(comp))
		for h.Len() > 0 && stale < staleLimit {
			it := heap.Pop(&h).(heapItem)
			v := it.v
			if p.lock[v] || it.ver != p.ver[v] {
				continue
			}
			// edge count change
			adj, w, _ := g.nbrs(v)
			same, other := 0, 0
			for _, u := range adj {
				if !p.inSet(u, id) {
					continue
				}
				if p.side[u] == p.side[v] {
					same++
				} else {
					other++
				}
			}
			var nA, nB int
			if p.side[v] == 0 {
				nA, nB = eA-same, eB+other
			} else {
				nA, nB = eA+other, eB-same
			}
			if max(nA, nB) > p.balanceLimit(total, eA, eB) {
				p.lock[v] = true // infeasible in this pass
				continue
			}
			// apply
			p.lock[v] = true
			from := p.side[v]
			p.side[v] = 1 - from
			cutCost -= it.gain
			moves = append(moves, move{v, nA - eA, nB - eB, it.gain})
			eA, eB = nA, nB
			for i, u := range adj {
				if !p.inSet(u, id) || p.lock[u] {
					continue
				}
				if p.side[u] == from {
					p.gain[u] += 2 * float64(w[i])
				} else {
					p.gain[u] -= 2 * float64(w[i])
				}
				p.ver[u]++
				heap.Push(&h, heapItem{p.gain[u], u, p.ver[u]})
			}
			if cutCost < bestCost-1e-9 {
				bestCost, bestStep, stale = cutCost, len(moves), 0
			} else {
				stale++
			}
		}
		// roll back moves after the best prefix
		for i := len(moves) - 1; i >= bestStep; i-- {
			p.side[moves[i].v] = 1 - p.side[moves[i].v]
		}
		if bestStep == 0 {
			break
		}
	}
}

// uniqueTerm is the replication cost change caused by moving a task with
// score s away from side (a = sum on A, b = sum on B) of one worker.
// Returns min(after) - min(before).
func uniqueTerm(a, b, s float64, fromA bool) float64 {
	before := math.Min(a, b)
	if fromA {
		return math.Min(a-s, b+s) - before
	}
	return math.Min(a+s, b-s) - before
}

// refineUniqueTasks moves tasks across the cut to minimise the summed
// scores of guest worker copies, keeping task-degree sums balanced.
func (p *partitioner) refineUniqueTasks(comp []int32, id int32, total int) {
	g := p.g
	nT := int32(g.nT)
	type move struct{ t int32 }
	taskGain := func(t int32) float64 {
		adj, w, _ := g.nbrs(t)
		gv := 0.0
		fromA := p.side[t] == 0
		for i, wv := range adj {
			k := wv - nT
			gv -= uniqueTerm(p.sA[k], p.sB[k], float64(w[i]), fromA)
		}
		return gv
	}
	for pass := 0; pass < p.cfg.FMPasses; pass++ {
		// worker sums and side edge counts
		st := p.nextStamp()
		eA, eB := 0, 0
		for _, t := range comp {
			p.lock[t] = false
			if p.side[t] == 0 {
				eA += g.deg(t)
			} else {
				eB += g.deg(t)
			}
			adj, w, _ := g.nbrs(t)
			for i, wv := range adj {
				k := wv - nT
				if p.mark[wv] != st {
					p.mark[wv] = st
					p.sA[k], p.sB[k] = 0, 0
				}
				if p.side[t] == 0 {
					p.sA[k] += float64(w[i])
				} else {
					p.sB[k] += float64(w[i])
				}
			}
		}
		cost := 0.0
		for _, t := range comp { // each worker once via stamp
			adj, _, _ := g.nbrs(t)
			for _, wv := range adj {
				if p.mark[wv] == st {
					p.mark[wv] = -st
					k := wv - nT
					cost += math.Min(p.sA[k], p.sB[k])
				}
			}
		}
		h := make(gainHeap, 0, len(comp))
		for _, t := range comp {
			p.gain[t] = taskGain(t)
			p.ver[t]++
			h = append(h, heapItem{p.gain[t], t, p.ver[t]})
		}
		heap.Init(&h)

		var moves []move
		bestCost, bestStep := cost, 0
		stale, staleLimit := 0, nonImprovingLimit(len(comp))
		for h.Len() > 0 && stale < staleLimit {
			it := heap.Pop(&h).(heapItem)
			t := it.v
			if p.lock[t] || it.ver != p.ver[t] {
				continue
			}
			d := g.deg(t)
			var nA, nB int
			if p.side[t] == 0 {
				nA, nB = eA-d, eB+d
			} else {
				nA, nB = eA+d, eB-d
			}
			if max(nA, nB) > p.balanceLimit(total, eA, eB) {
				p.lock[t] = true
				continue
			}
			p.lock[t] = true
			fromA := p.side[t] == 0
			adj, w, _ := g.nbrs(t)
			p.dirtyCnt++
			p.dirty = p.dirty[:0]
			for i, wv := range adj {
				k := wv - nT
				s := float64(w[i])
				oa, ob := p.sA[k], p.sB[k]
				if fromA {
					p.sA[k], p.sB[k] = oa-s, ob+s
				} else {
					p.sA[k], p.sB[k] = oa+s, ob-s
				}
				na, nb := p.sA[k], p.sB[k]
				// update gains of the other tasks of this worker incrementally
				wadj, ww, _ := g.nbrs(wv)
				for j, u := range wadj {
					if u == t || !p.inSet(u, id) || p.lock[u] {
						continue
					}
					su := float64(ww[j])
					ufromA := p.side[u] == 0
					p.gain[u] += uniqueTerm(oa, ob, su, ufromA) - uniqueTerm(na, nb, su, ufromA)
					if p.dirtyMark[u] != p.dirtyCnt {
						p.dirtyMark[u] = p.dirtyCnt
						p.dirty = append(p.dirty, u)
					}
				}
			}
			for _, u := range p.dirty { // one heap entry per affected task
				p.ver[u]++
				heap.Push(&h, heapItem{p.gain[u], u, p.ver[u]})
			}
			p.side[t] = 1 - p.side[t]
			cost -= it.gain
			eA, eB = nA, nB
			moves = append(moves, move{t})
			if cost < bestCost-1e-9 {
				bestCost, bestStep, stale = cost, len(moves), 0
			} else {
				stale++
			}
		}
		for i := len(moves) - 1; i >= bestStep; i-- {
			p.side[moves[i].t] = 1 - p.side[moves[i].t]
		}
		if bestStep == 0 {
			break
		}
	}
}
