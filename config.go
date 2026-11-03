package partitioning

// Mode selects what must stay unique across sub-graphs.
type Mode uint8

const (
	// EdgeCut keeps both workers and tasks unique; crossing edges are dropped.
	EdgeCut Mode = iota
	// UniqueTasks keeps tasks unique; workers are replicated as needed.
	UniqueTasks
)

func (m Mode) String() string {
	switch m {
	case EdgeCut:
		return "EdgeCut"
	case UniqueTasks:
		return "UniqueTasks"
	}
	return "Mode(?)"
}

// Config controls partitioning.
type Config struct {
	// Mode selects EdgeCut or UniqueTasks.
	Mode Mode
	// MaxEdges is the edge limit of one sub-graph. Must be > 0.
	MaxEdges int
	// Theta drops every edge with Score < Theta before partitioning.
	// 0 disables pruning.
	Theta float32
	// Epsilon is the balance tolerance of a bisection: each half may hold
	// up to (0.5+Epsilon) of the edges. Default 0.1.
	Epsilon float64
	// FMPasses is the maximum number of refinement passes per bisection.
	// Default 3. 0 disables refinement (pure BFS cut).
	FMPasses int
}

func (c Config) withDefaults() Config {
	if c.Epsilon <= 0 {
		c.Epsilon = 0.1
	}
	if c.FMPasses < 0 {
		c.FMPasses = 0
	} else if c.FMPasses == 0 {
		c.FMPasses = 3
	}
	return c
}
