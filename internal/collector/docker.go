package collector

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DockerCollector collects Docker container metrics.
type DockerCollector struct {
	once    sync.Once
	noDocker bool
}

// NewDockerCollector creates a new DockerCollector.
func NewDockerCollector() *DockerCollector {
	return &DockerCollector{}
}

func (d *DockerCollector) Name() string { return "docker" }

// dockerStatsLine is the JSON format from `docker stats --format "{{json .}}"`.
type dockerStatsLine struct {
	Name      string `json:"Name"`
	CPUPerc   string `json:"CPUPerc"`
	MemUsage  string `json:"MemUsage"`
	MemPerc   string `json:"MemPerc"`
	Container string `json:"Container"`
}

// dockerPSLine is the JSON format from `docker ps --format "{{json .}}"`.
type dockerPSLine struct {
	Names  string `json:"Names"`
	Status string `json:"Status"`
	ID     string `json:"ID"`
}

func (d *DockerCollector) Collect() ([]Metric, error) {
	ts := time.Now().Unix()

	// Check once if docker is available.
	available := true
	d.once.Do(func() {
		if _, err := exec.LookPath("docker"); err != nil {
			log.Printf("docker: docker not found, disabling Docker collector")
			d.noDocker = true
			available = false
		}
	})
	if d.noDocker || !available {
		return nil, nil
	}

	// Get container statuses from `docker ps`.
	statuses := map[string]string{}
	psOut, err := runCommand("docker", "ps", "--format", "{{json .}}")
	if err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(psOut))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var ps dockerPSLine
			if err := json.Unmarshal([]byte(line), &ps); err == nil {
				statuses[ps.Names] = ps.Status
			}
		}
	}

	// Get resource usage from `docker stats`.
	statsOut, err := runCommand("docker", "stats", "--no-stream", "--format", "{{json .}}")
	if err != nil {
		if strings.Contains(err.Error(), "Cannot connect") || strings.Contains(err.Error(), "Is the docker daemon running") {
			log.Printf("docker: daemon not running, skipping")
			return nil, nil
		}
		return nil, fmt.Errorf("docker stats: %w", err)
	}

	var metrics []Metric
	scanner := bufio.NewScanner(bytes.NewReader(statsOut))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var s dockerStatsLine
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			continue
		}

		name := sanitizeContainerName(s.Name)

		cpuPct := parsePercent(s.CPUPerc)
		memPct := parsePercent(s.MemPerc)
		memBytes := parseMemUsage(s.MemUsage)

		status := statuses[s.Name]
		if strings.HasPrefix(strings.ToLower(status), "up") || status == "" {
			status = "running"
		} else {
			status = "stopped"
		}

		tags := map[string]string{"container": s.Name, "status": status}

		metrics = append(metrics,
			Metric{TS: ts, Category: "docker", Name: fmt.Sprintf("docker.%s.cpu_pct", name), Value: cpuPct, Tags: tags},
			Metric{TS: ts, Category: "docker", Name: fmt.Sprintf("docker.%s.mem_pct", name), Value: memPct, Tags: tags},
			Metric{TS: ts, Category: "docker", Name: fmt.Sprintf("docker.%s.mem_bytes", name), Value: float64(memBytes), Tags: tags},
		)
	}

	return metrics, nil
}

func runCommand(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %v: %w", name, args, err)
	}
	return out, nil
}

func sanitizeContainerName(name string) string {
	name = strings.TrimPrefix(name, "/")
	replacer := strings.NewReplacer("-", "_", ".", "_", " ", "_")
	return replacer.Replace(name)
}

// parsePercent strips the trailing "%" and converts to float64.
func parsePercent(s string) float64 {
	s = strings.TrimSpace(strings.TrimSuffix(s, "%"))
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// parseMemUsage parses strings like "128MiB / 2GiB" and returns the used bytes.
func parseMemUsage(s string) int64 {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) == 0 {
		return 0
	}
	return parseBytes(strings.TrimSpace(parts[0]))
}

func parseBytes(s string) int64 {
	s = strings.TrimSpace(s)
	multipliers := []struct {
		suffix string
		factor int64
	}{
		{"GiB", 1 << 30},
		{"MiB", 1 << 20},
		{"KiB", 1 << 10},
		{"GB", 1_000_000_000},
		{"MB", 1_000_000},
		{"KB", 1_000},
		{"B", 1},
	}
	for _, m := range multipliers {
		if strings.HasSuffix(s, m.suffix) {
			numStr := strings.TrimSpace(strings.TrimSuffix(s, m.suffix))
			v, err := strconv.ParseFloat(numStr, 64)
			if err != nil {
				return 0
			}
			return int64(v * float64(m.factor))
		}
	}
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}
