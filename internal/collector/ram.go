package collector

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
)

// RAMCollector collects RAM and top-process metrics.
type RAMCollector struct {
	topN int
}

// NewRAMCollector creates a new RAMCollector that reports the top N processes by RSS.
func NewRAMCollector(topN int) *RAMCollector {
	if topN <= 0 {
		topN = 10
	}
	return &RAMCollector{topN: topN}
}

func (r *RAMCollector) Name() string { return "ram" }

func (r *RAMCollector) Collect() ([]Metric, error) {
	ts := time.Now().Unix()
	var metrics []Metric

	vm, err := mem.VirtualMemory()
	if err != nil {
		return nil, fmt.Errorf("ram: virtual memory: %w", err)
	}

	metrics = append(metrics,
		Metric{TS: ts, Category: "ram", Name: "ram.used_pct", Value: vm.UsedPercent},
		Metric{TS: ts, Category: "ram", Name: "ram.used_bytes", Value: float64(vm.Used)},
		Metric{TS: ts, Category: "ram", Name: "ram.total_bytes", Value: float64(vm.Total)},
		Metric{TS: ts, Category: "ram", Name: "ram.available_bytes", Value: float64(vm.Available)},
	)

	// Collect top N processes by RSS.
	procs, err := process.Processes()
	if err != nil {
		log.Printf("ram: list processes: %v", err)
		return metrics, nil
	}

	type procEntry struct {
		pid         int32
		name        string
		rss         uint64
		cpuPct      float64
		containerID string
	}

	entries := make([]procEntry, 0, len(procs))
	for _, p := range procs {
		mi, err := p.MemoryInfo()
		if err != nil {
			continue
		}
		name, _ := p.Name()
		cpuPct, _ := p.CPUPercent()
		containerID := readCgroupContainerID(p.Pid)
		entries = append(entries, procEntry{
			pid:         p.Pid,
			name:        name,
			rss:         mi.RSS,
			cpuPct:      cpuPct,
			containerID: containerID,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].rss > entries[j].rss
	})

	limit := r.topN
	if limit > len(entries) {
		limit = len(entries)
	}
	for _, e := range entries[:limit] {
		tags := map[string]string{
			"name": e.name,
		}
		if e.containerID != "" {
			tags["container_id"] = e.containerID
		}
		metrics = append(metrics, Metric{
			TS:       ts,
			Category: "ram",
			Name:     fmt.Sprintf("ram.proc.%d", e.pid),
			Value:    float64(e.rss),
			Tags:     tags,
		})
	}

	return metrics, nil
}

// readCgroupContainerID attempts to read a Docker container ID from /proc/{pid}/cgroup.
// Returns an empty string if the file doesn't exist or no container is found.
func readCgroupContainerID(pid int32) string {
	path := fmt.Sprintf("/proc/%d/cgroup", pid)
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		// Lines look like: "12:memory:/docker/abc123..."
		parts := strings.Split(line, "/")
		for i, part := range parts {
			if part == "docker" && i+1 < len(parts) {
				id := parts[i+1]
				// Container IDs are 64 hex chars; accept at least 12.
				if len(id) >= 12 {
					return id[:12]
				}
			}
		}
	}
	return ""
}
