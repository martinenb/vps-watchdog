package collector

import (
	"fmt"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/load"
)

// CPUCollector collects overall and per-core CPU metrics plus load averages.
// The first call is a warm-up and returns an empty slice.
type CPUCollector struct {
	warmedUp bool
}

// NewCPUCollector creates a new CPUCollector.
func NewCPUCollector() *CPUCollector {
	return &CPUCollector{}
}

func (c *CPUCollector) Name() string { return "cpu" }

func (c *CPUCollector) Collect() ([]Metric, error) {
	ts := time.Now().Unix()

	// cpu.Percent with interval=0 returns the usage since the last call.
	// The very first call initialises the baseline and returns 0 – skip it.
	totalPcts, err := cpu.Percent(0, false)
	if err != nil {
		return nil, fmt.Errorf("cpu: total percent: %w", err)
	}
	corePcts, err := cpu.Percent(0, true)
	if err != nil {
		return nil, fmt.Errorf("cpu: per-core percent: %w", err)
	}

	if !c.warmedUp {
		c.warmedUp = true
		return nil, nil
	}

	var metrics []Metric

	if len(totalPcts) > 0 {
		metrics = append(metrics, Metric{
			TS:       ts,
			Category: "cpu",
			Name:     "cpu.total",
			Value:    totalPcts[0],
		})
	}

	for i, pct := range corePcts {
		metrics = append(metrics, Metric{
			TS:       ts,
			Category: "cpu",
			Name:     fmt.Sprintf("cpu.core.%d", i),
			Value:    pct,
		})
	}

	avg, err := load.Avg()
	if err == nil {
		metrics = append(metrics,
			Metric{TS: ts, Category: "cpu", Name: "cpu.load_1", Value: avg.Load1},
			Metric{TS: ts, Category: "cpu", Name: "cpu.load_5", Value: avg.Load5},
			Metric{TS: ts, Category: "cpu", Name: "cpu.load_15", Value: avg.Load15},
		)
	}

	return metrics, nil
}
