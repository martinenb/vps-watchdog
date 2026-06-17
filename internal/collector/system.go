package collector

import (
	"bufio"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/mem"
)

// SystemCollector collects swap and open file descriptor metrics.
type SystemCollector struct{}

// NewSystemCollector creates a new SystemCollector.
func NewSystemCollector() *SystemCollector {
	return &SystemCollector{}
}

func (s *SystemCollector) Name() string { return "system" }

func (s *SystemCollector) Collect() ([]Metric, error) {
	ts := time.Now().Unix()
	var metrics []Metric

	// Swap metrics via gopsutil.
	swap, err := mem.SwapMemory()
	if err == nil {
		metrics = append(metrics,
			Metric{TS: ts, Category: "system", Name: "system.swap_pct", Value: swap.UsedPercent},
			Metric{TS: ts, Category: "system", Name: "system.swap_used_bytes", Value: float64(swap.Used)},
		)
	} else {
		log.Printf("system: swap memory: %v", err)
	}

	// Open file descriptors from /proc/sys/fs/file-nr (Linux only).
	if fds, ok := readOpenFDs(); ok {
		metrics = append(metrics, Metric{
			TS:       ts,
			Category: "system",
			Name:     "system.open_fds",
			Value:    float64(fds),
		})
	}

	return metrics, nil
}

// readOpenFDs reads the number of open file descriptors from /proc/sys/fs/file-nr.
// Returns (count, true) on success, (0, false) if the file doesn't exist (non-Linux).
func readOpenFDs() (int64, bool) {
	f, err := os.Open("/proc/sys/fs/file-nr")
	if err != nil {
		return 0, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return 0, false
	}
	// file-nr format: "allocated_fds\tfree_fds\tmax_fds"
	fields := strings.Fields(scanner.Text())
	if len(fields) < 1 {
		return 0, false
	}
	n, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
