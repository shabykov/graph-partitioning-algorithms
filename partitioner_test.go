package partitioning

import (
	"math"
	"math/rand"
	"testing"
)

// ---------------------------------------------------------------------------
// Example from the design discussion: 5 workers, 6 tasks, 12 edges, limit 8.

// vertices interns Task and Worker values by ID, because the partitioner
// identifies vertices by pointer.
type vertices struct {
	tasks   map[uint64]*Task
	workers map[uint64]*Worker
}

func newVertices() *vertices {
	return &vertices{tasks: map[uint64]*Task{}, workers: map[uint64]*Worker{}}
}

func (vs *vertices) edge(task, worker uint64, score float32) *Edge {
	t, ok := vs.tasks[task]
	if !ok {
		t = &Task{ID: task}
		vs.tasks[task] = t
	}
	w, ok := vs.workers[worker]
	if !ok {
		w = &Worker{ID: worker}
		vs.workers[worker] = w
	}
	return &Edge{Task: t, Worker: w, Score: score}
}

func exampleEdges() []*Edge {
	edge := newVertices().edge
	return []*Edge{
		edge(1, 1, 0.9), edge(1, 2, 0.6),
		edge(2, 1, 0.7), edge(2, 2, 0.8),
		edge(3, 2, 0.3), edge(3, 3, 0.9),
		edge(4, 3, 0.8), edge(4, 4, 0.7),
		edge(5, 4, 0.9), edge(5, 5, 0.5),
		edge(6, 4, 0.4), edge(6, 5, 0.9),
	}
}

func TestExampleEdgeCut(t *testing.T) {
	res := Partition(Config{Mode: EdgeCut, MaxEdges: 8}, NewGraph(exampleEdges()))
	if len(res.Parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(res.Parts))
	}
	if len(res.Cut) != 1 || edgeKey(res.Cut[0]) != [2]uint64{3, 2} || res.Cut[0].Score != 0.3 {
		t.Fatalf("cut = %v, want [T3–W2 0.3]", res.Cut)
	}
	if math.Abs(res.CutCost-0.3) > 1e-6 {
		t.Fatalf("cut cost = %v, want 0.3", res.CutCost)
	}
	sizes := []int{len(res.Parts[0].Edges), len(res.Parts[1].Edges)}
	if !(sizes[0] == 7 && sizes[1] == 4 || sizes[0] == 4 && sizes[1] == 7) {
		t.Fatalf("part sizes = %v, want 7 and 4", sizes)
	}
}

func TestExampleUniqueTasks(t *testing.T) {
	res := Partition(Config{Mode: UniqueTasks, MaxEdges: 8}, NewGraph(exampleEdges()))
	if len(res.Parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(res.Parts))
	}
	if len(res.Cut) != 0 {
		t.Fatalf("cut must be empty, got %v", res.Cut)
	}
	if math.Abs(res.CutCost-0.3) > 1e-6 {
		t.Fatalf("replication cost = %v, want 0.3 (W2 guest copy)", res.CutCost)
	}
	var small, big *Part
	for i := range res.Parts {
		if res.Parts[i].Tasks == 2 {
			small = &res.Parts[i]
		} else {
			big = &res.Parts[i]
		}
	}
	if small == nil || big == nil || big.Tasks != 4 {
		t.Fatalf("expected parts with 2 and 4 tasks, got %+v", res.Parts)
	}
	if small.Workers != 2 || big.Workers != 4 {
		t.Fatalf("workers: small=%d big=%d, want 2 and 4 (W2 replicated)", small.Workers, big.Workers)
	}
	if len(small.Edges) != 4 || len(big.Edges) != 8 {
		t.Fatalf("edges: small=%d big=%d, want 4 and 8", len(small.Edges), len(big.Edges))
	}
}

// ---------------------------------------------------------------------------
// Synthetic clustered graphs

type synth struct {
	edges []*Edge
	inter float64 // sum of inter-cluster scores
	intra int     // number of intra-cluster edges
}

// clustered builds k clusters of nt tasks × nw workers with dense strong
// intra-cluster edges and a few weak inter-cluster edges.
func clustered(seed int64, k, nt, nw int, pIntra, pInter float64) synth {
	r := rand.New(rand.NewSource(seed))
	edge := newVertices().edge
	var s synth
	tid := func(c, i int) uint64 { return uint64(c*nt + i + 1) }
	wid := func(c, j int) uint64 { return uint64(c*nw + j + 1) }
	for c := 0; c < k; c++ {
		for i := 0; i < nt; i++ {
			for j := 0; j < nw; j++ {
				if r.Float64() < pIntra {
					s.edges = append(s.edges, edge(tid(c, i), wid(c, j), float32(0.5+0.5*r.Float64())))
					s.intra++
				}
			}
		}
	}
	for c := 0; c < k; c++ {
		for i := 0; i < nt; i++ {
			for c2 := 0; c2 < k; c2++ {
				if c2 == c {
					continue
				}
				for j := 0; j < nw; j++ {
					if r.Float64() < pInter {
						sc := float32(0.2 * r.Float64())
						s.edges = append(s.edges, edge(tid(c, i), wid(c2, j), sc))
						s.inter += float64(sc)
					}
				}
			}
		}
	}
	return s
}

func shuffled(r *rand.Rand, es []*Edge) []*Edge {
	out := make([]*Edge, len(es))
	copy(out, es)
	r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

func edgeKey(e *Edge) [2]uint64 { return [2]uint64{e.Task.ID, e.Worker.ID} }

// ---------------------------------------------------------------------------
// Invariants

func checkInvariants(t *testing.T, cfg Config, in []*Edge, res Result) {
	t.Helper()
	// edge limit
	for i, p := range res.Parts {
		if len(p.Edges) == 0 {
			t.Fatalf("part %d is empty", i)
		}
		if len(p.Edges) > cfg.MaxEdges && res.Oversized == 0 {
			t.Fatalf("part %d has %d edges > %d", i, len(p.Edges), cfg.MaxEdges)
		}
	}
	// every task in exactly one part
	taskPart := map[uint64]int{}
	for i, p := range res.Parts {
		seen := map[uint64]bool{}
		for _, e := range p.Edges {
			if seen[e.Task.ID] {
				continue
			}
			seen[e.Task.ID] = true
			if prev, ok := taskPart[e.Task.ID]; ok && prev != i {
				t.Fatalf("task %d in parts %d and %d", e.Task.ID, prev, i)
			}
			taskPart[e.Task.ID] = i
		}
	}
	// EdgeCut: every worker in exactly one part too
	if cfg.Mode == EdgeCut {
		workerPart := map[uint64]int{}
		for i, p := range res.Parts {
			for _, e := range p.Edges {
				if prev, ok := workerPart[e.Worker.ID]; ok && prev != i {
					t.Fatalf("worker %d in parts %d and %d", e.Worker.ID, prev, i)
				}
				workerPart[e.Worker.ID] = i
			}
		}
	}
	// edge conservation: parts (+ cut) == deduplicated input, no duplicates
	kept := map[[2]uint64]float32{}
	for _, e := range in {
		if s, ok := kept[edgeKey(e)]; !ok || e.Score > s {
			kept[edgeKey(e)] = e.Score
		}
	}
	got := map[[2]uint64]int{}
	for _, p := range res.Parts {
		for _, e := range p.Edges {
			got[edgeKey(e)]++
		}
	}
	for _, e := range res.Cut {
		got[edgeKey(e)]++
	}
	if len(got) != len(kept) {
		t.Fatalf("edge count: got %d distinct, want %d", len(got), len(kept))
	}
	for k, n := range got {
		if n != 1 {
			t.Fatalf("edge %v appears %d times", k, n)
		}
		if _, ok := kept[k]; !ok {
			t.Fatalf("edge %v not in input", k)
		}
	}
	if cfg.Mode == UniqueTasks && len(res.Cut) != 0 {
		t.Fatalf("UniqueTasks must not drop edges, cut=%d", len(res.Cut))
	}
}

func TestInvariants(t *testing.T) {
	for _, mode := range []Mode{EdgeCut, UniqueTasks} {
		for seed := int64(1); seed <= 5; seed++ {
			s := clustered(seed, 6, 40, 30, 0.3, 0.004)
			cfg := Config{Mode: mode, MaxEdges: 500}
			res := Partition(cfg, NewGraph(s.edges))
			checkInvariants(t, cfg, s.edges, res)
			if res.Oversized != 0 {
				t.Fatalf("%s seed %d: unexpected oversized parts", mode, seed)
			}
		}
	}
}

func TestDeterminism(t *testing.T) {
	for _, mode := range []Mode{EdgeCut, UniqueTasks} {
		s := clustered(42, 5, 50, 40, 0.3, 0.003)
		cfg := Config{Mode: mode, MaxEdges: 600}
		base := Partition(cfg, NewGraph(s.edges))
		r := rand.New(rand.NewSource(7))
		for i := 0; i < 5; i++ {
			res := Partition(cfg, NewGraph(shuffled(r, s.edges)))
			if len(res.Parts) != len(base.Parts) || res.CutCost != base.CutCost {
				t.Fatalf("%s: run %d differs: parts %d vs %d, cost %v vs %v",
					mode, i, len(res.Parts), len(base.Parts), res.CutCost, base.CutCost)
			}
			for j := range res.Parts {
				if len(res.Parts[j].Edges) != len(base.Parts[j].Edges) {
					t.Fatalf("%s: part %d size differs", mode, j)
				}
				for k := range res.Parts[j].Edges {
					got, want := res.Parts[j].Edges[k], base.Parts[j].Edges[k]
					if edgeKey(got) != edgeKey(want) || got.Score != want.Score {
						t.Fatalf("%s: part %d edge %d differs", mode, j, k)
					}
				}
			}
		}
	}
}

// Quality: on a clustered Graph the cut must not be worse than cutting
// exactly along cluster borders.
func TestQualityClustered(t *testing.T) {
	for _, mode := range []Mode{EdgeCut, UniqueTasks} {
		s := clustered(3, 4, 60, 50, 0.3, 0.002)
		cfg := Config{Mode: mode, MaxEdges: int(float64(s.intra)/4*1.3) + 50}
		res := Partition(cfg, NewGraph(s.edges))
		checkInvariants(t, cfg, s.edges, res)
		if res.CutCost > s.inter {
			t.Fatalf("%s: cut cost %.2f exceeds inter-cluster total %.2f", mode, res.CutCost, s.inter)
		}
		t.Logf("%s: parts=%d cutCost=%.2f interTotal=%.2f", mode, len(res.Parts), res.CutCost, s.inter)
	}
}

func TestDeduplication(t *testing.T) {
	edge := newVertices().edge
	es := []*Edge{edge(1, 1, 0.9), edge(1, 2, 0.05), edge(2, 2, 0.8), edge(1, 1, 0.4)}
	g := NewGraph(es)
	if g.Duplicates() != 1 || g.Len() != 3 {
		t.Fatalf("duplicates = %d, len = %d, want 1 and 3", g.Duplicates(), g.Len())
	}
	res := Partition(Config{Mode: EdgeCut, MaxEdges: 10}, g)
	if len(res.Parts) != 1 || len(res.Parts[0].Edges) != 3 { // no pruning: the thin bridge stays
		t.Fatalf("expected one part of 3 edges, got %+v", res.Parts)
	}
	if res.Parts[0].Edges[0] != es[0] { // the higher-scoring duplicate is kept
		t.Fatalf("kept %+v, want the 0.9 edge", *res.Parts[0].Edges[0])
	}
}

// Same IDs behind different pointers are different vertices.
func TestIdentityByPointer(t *testing.T) {
	es := []*Edge{
		newVertices().edge(1, 1, 0.9),
		newVertices().edge(1, 1, 0.4),
	}
	g := NewGraph(es)
	if g.Duplicates() != 0 || g.Len() != 2 {
		t.Fatalf("duplicates = %d, len = %d, want 0 and 2", g.Duplicates(), g.Len())
	}
	if res := Partition(Config{Mode: EdgeCut, MaxEdges: 10}, g); len(res.Parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(res.Parts))
	}
}

func TestNilGraph(t *testing.T) {
	if res := Partition(Config{Mode: EdgeCut, MaxEdges: 10}, nil); len(res.Parts) != 0 {
		t.Fatalf("expected empty result, got %+v", res)
	}
}

func TestOversizedSingleTask(t *testing.T) {
	edge := newVertices().edge
	var es []*Edge
	for w := uint64(1); w <= 30; w++ {
		es = append(es, edge(1, w, 0.5))
	}
	res := Partition(Config{Mode: UniqueTasks, MaxEdges: 10}, NewGraph(es))
	if res.Oversized != 1 || len(res.Parts) != 1 || len(res.Parts[0].Edges) != 30 {
		t.Fatalf("expected one oversized part of 30 edges, got %+v", res)
	}
}

func TestHubWorkerEdgeCut(t *testing.T) {
	// one worker connected to 30 tasks: EdgeCut can only split by dropping edges
	edge := newVertices().edge
	var es []*Edge
	for tk := uint64(1); tk <= 30; tk++ {
		es = append(es, edge(tk, 1, 0.5))
	}
	cfg := Config{Mode: EdgeCut, MaxEdges: 10}
	res := Partition(cfg, NewGraph(es))
	checkInvariants(t, cfg, es, res)
	if len(res.Parts) != 1 || len(res.Parts[0].Edges) > 10 {
		t.Fatalf("expected one part ≤ 10 edges, got %d parts", len(res.Parts))
	}
}

// ---------------------------------------------------------------------------
// Benchmarks

func benchGraph() []*Edge {
	return clustered(99, 40, 120, 100, 0.19, 0.0003).edges // ≈ 100k edges
}

func BenchmarkEdgeCut100k(b *testing.B) {
	g := NewGraph(benchGraph())
	b.ReportMetric(float64(g.Len()), "edges")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Partition(Config{Mode: EdgeCut, MaxEdges: 20000}, g)
	}
}

func BenchmarkUniqueTasks100k(b *testing.B) {
	g := NewGraph(benchGraph())
	b.ReportMetric(float64(g.Len()), "edges")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Partition(Config{Mode: UniqueTasks, MaxEdges: 20000}, g)
	}
}
