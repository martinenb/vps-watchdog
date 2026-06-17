package collector

// Metric represents a single time-series data point.
type Metric struct {
	TS       int64
	Category string
	Name     string
	Value    float64
	Tags     map[string]string // nil means no tags
}

// Collector is implemented by each subsystem collector.
type Collector interface {
	Name() string
	Collect() ([]Metric, error)
}

// ProcessInfo holds information about a running process.
type ProcessInfo struct {
	PID         int32
	Name        string
	RSS         uint64
	CPUPct      float64
	ContainerID string // empty if not a Docker container
}

// DataPoint is a single (timestamp, value) pair for time-series queries.
type DataPoint struct {
	TS    int64
	Value float64
}

// DockerMetric holds the latest metrics for a Docker container.
type DockerMetric struct {
	Name   string
	CPUPct float64
	MemPct float64
	MemMB  float64
	Status string
}

// WeeklySummary holds aggregated weekly statistics for a metric.
type WeeklySummary struct {
	WeekTS int64
	Name   string
	AvgVal float64
	MaxVal float64
	P95Val float64
}
