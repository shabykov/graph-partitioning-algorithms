package partitioning

import "math"

// ---------------------------------------------------------------------------
// Assembly

func (p *partitioner) assemble(res *Result) {
	g := p.g
	nR := int(p.nRegion)
	parts := make([]Part, nR)
	nT := int32(g.nT)

	if p.cfg.Mode == UniqueTasks {
		for i, e := range g.edges {
			r := p.region[g.eTask[i]]
			parts[r].Edges = append(parts[r].Edges, e)
		}
		// replication cost: per worker, total − max over regions
		acc := make([]float64, nR)
		touched := make([]int32, 0, 8)
		for wv := nT; wv < nT+int32(g.nW); wv++ {
			adj, w, _ := g.nbrs(wv)
			touched = touched[:0]
			sum, mx := 0.0, 0.0
			for i, t := range adj {
				r := p.region[t]
				if acc[r] == 0 {
					touched = append(touched, r)
				}
				acc[r] += float64(w[i])
				sum += float64(w[i])
			}
			for _, r := range touched {
				mx = math.Max(mx, acc[r])
				acc[r] = 0
			}
			res.CutCost += sum - mx
		}
	} else {
		for i, e := range g.edges {
			rt, rw := p.region[g.eTask[i]], p.region[g.eWorker[i]]
			if rt >= 0 && rt == rw {
				parts[rt].Edges = append(parts[rt].Edges, e)
			} else {
				res.Cut = append(res.Cut, e)
				res.CutCost += float64(e.Score)
			}
		}
	}

	// distinct counts and drop empty parts
	seen := make(map[uint64]struct{}, 64)
	for r := range parts {
		pt := &parts[r]
		if len(pt.Edges) == 0 {
			continue
		}
		clear(seen)
		for _, e := range pt.Edges {
			seen[e.Task] = struct{}{}
		}
		pt.Tasks = len(seen)
		clear(seen)
		for _, e := range pt.Edges {
			seen[e.Worker] = struct{}{}
		}
		pt.Workers = len(seen)
		res.Parts = append(res.Parts, *pt)
	}
}
