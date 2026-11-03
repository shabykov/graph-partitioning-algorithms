package partitioning

// bfs visits the working set reachable from start and returns the visit
// order. In UniqueTasks mode the traversal is task → worker → task.
// Visited vertices are marked with st; workers in UniqueTasks mode are
// marked once expanded so a hub worker is scanned a single time.
func (p *partitioner) bfs(start, id, st int32, order []int32) []int32 {
	g := p.g
	q := p.queue[:0]
	q = append(q, start)
	p.mark[start] = st
	order = append(order, start)
	for head := 0; head < len(q); head++ {
		v := q[head]
		adj, _, _ := g.nbrs(v)
		if p.cfg.Mode == UniqueTasks {
			for _, w := range adj {
				if p.mark[w] == st {
					continue
				}
				p.mark[w] = st
				wadj, _, _ := g.nbrs(w)
				for _, u := range wadj {
					if p.mark[u] != st && p.inSet(u, id) {
						p.mark[u] = st
						q = append(q, u)
						order = append(order, u)
					}
				}
			}
			continue
		}
		for _, u := range adj {
			if p.mark[u] != st && p.inSet(u, id) {
				p.mark[u] = st
				q = append(q, u)
				order = append(order, u)
			}
		}
	}
	p.queue = q[:0]
	return order
}
