package action

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"vps-watchdog/internal/collector"
	"vps-watchdog/internal/config"
	"vps-watchdog/internal/db"
)

// BrevoSender is the interface required by the engine to send email alerts.
type BrevoSender interface {
	Send(subject, htmlBody string, attachments []interface{}) error
}

// Engine evaluates collected metrics and triggers automated actions.
type Engine struct {
	cfg      *config.Config
	db       *db.DB
	cooldown *CooldownRegistry
	brevo    EmailSender
	cpuBuf   []float64
	cpuBufSz int
	mu       sync.Mutex
}

// EmailSender is a minimal interface for sending alert emails.
type EmailSender interface {
	SendAlert(subject, body string) error
}

// New creates a new Engine.
func New(cfg *config.Config, database *db.DB, emailSender EmailSender) *Engine {
	return &Engine{
		cfg:      cfg,
		db:       database,
		cooldown: NewCooldownRegistry(),
		brevo:    emailSender,
	}
}

// UpdateConfig replaces the config reference (called on SIGHUP reload).
func (e *Engine) UpdateConfig(cfg *config.Config) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cfg = cfg
	// Resize CPU buffer.
	e.cpuBuf = nil
	e.cpuBufSz = 0
}

// Evaluate inspects the latest metrics slice and triggers actions as needed.
func (e *Engine) Evaluate(metrics []collector.Metric) {
	e.mu.Lock()
	cfg := e.cfg
	e.mu.Unlock()

	metricMap := make(map[string]collector.Metric, len(metrics))
	for _, m := range metrics {
		metricMap[m.Name] = m
	}

	e.checkRAM(cfg, metricMap, metrics)
	e.checkCPU(cfg, metricMap)
	e.checkDisk(cfg, metricMap)
}

func (e *Engine) checkRAM(cfg *config.Config, metricMap map[string]collector.Metric, allMetrics []collector.Metric) {
	ramMetric, ok := metricMap["ram.used_pct"]
	if !ok {
		return
	}
	if ramMetric.Value <= cfg.Thresholds.RAMPCT {
		return
	}

	cooldownKey := "ram_alert"
	cooldownDuration := time.Duration(cfg.Thresholds.RAMAlertCooldownMinutes) * time.Minute
	if !e.cooldown.Allow(cooldownKey, cooldownDuration) {
		return
	}

	log.Printf("engine: RAM threshold exceeded: %.1f%% > %.1f%%", ramMetric.Value, cfg.Thresholds.RAMPCT)

	// Collect process metrics for candidates.
	type procInfo struct {
		name        string
		containerID string
		rss         float64
	}
	var processes []procInfo
	for _, m := range allMetrics {
		if !strings.HasPrefix(m.Name, "ram.proc.") {
			continue
		}
		pi := procInfo{rss: m.Value}
		if m.Tags != nil {
			pi.name = m.Tags["name"]
			pi.containerID = m.Tags["container_id"]
		}
		processes = append(processes, pi)
	}

	var actions []string
	var topProcs []string
	for _, p := range processes {
		topProcs = append(topProcs, fmt.Sprintf("%s (RSS: %.0f MB)", p.name, p.rss/1e6))
	}

	if cfg.Docker.AutoStop && len(processes) > 0 {
		// Build a set from stop_order for prioritisation.
		orderIndex := map[string]int{}
		for i, name := range cfg.Docker.StopOrder {
			orderIndex[name] = i
		}

		// Collect containers that are idle.
		idleDuration := time.Duration(cfg.Docker.IdleDurationMinutes) * time.Minute
		type candidate struct {
			containerID string
			orderIdx    int
		}
		seen := map[string]bool{}
		var candidates []candidate

		for _, p := range processes {
			if p.containerID == "" || seen[p.containerID] {
				continue
			}
			seen[p.containerID] = true

			idle, err := e.db.IsContainerIdle(p.containerID, cfg.Docker.IdleCPUPct, idleDuration)
			if err != nil {
				log.Printf("engine: IsContainerIdle %s: %v", p.containerID, err)
				continue
			}
			if !idle {
				continue
			}

			idx, ordered := orderIndex[p.containerID]
			if !ordered {
				idx = 9999 // stop ordered containers first
			}
			candidates = append(candidates, candidate{containerID: p.containerID, orderIdx: idx})
		}

		// Sort by stop_order priority.
		for i := 0; i < len(candidates); i++ {
			for j := i + 1; j < len(candidates); j++ {
				if candidates[j].orderIdx < candidates[i].orderIdx {
					candidates[i], candidates[j] = candidates[j], candidates[i]
				}
			}
		}

		for _, c := range candidates {
			reason := fmt.Sprintf("RAM %.1f%% > %.1f%% threshold", ramMetric.Value, cfg.Thresholds.RAMPCT)
			success, err := StopContainer(c.containerID, reason, e.db)
			if err != nil {
				log.Printf("engine: StopContainer %s: %v", c.containerID, err)
			}
			if success {
				actions = append(actions, fmt.Sprintf("Stopped container %s (idle, RAM relief)", c.containerID))
			}
		}
	}

	// Send email alert.
	subject := fmt.Sprintf("[VPS Watchdog] RAM Alert: %.1f%% used", ramMetric.Value)
	body := buildAlertBody("RAM", ramMetric.Value, cfg.Thresholds.RAMPCT, actions, topProcs)
	if err := e.brevo.SendAlert(subject, body); err != nil {
		log.Printf("engine: send RAM alert: %v", err)
	}
}

func (e *Engine) checkCPU(cfg *config.Config, metricMap map[string]collector.Metric) {
	cpuMetric, ok := metricMap["cpu.total"]
	if !ok {
		return
	}

	e.mu.Lock()
	// Compute buffer size based on sustained minutes and collection interval.
	intervalSec := cfg.General.IntervalSeconds
	if intervalSec <= 0 {
		intervalSec = 30
	}
	needed := cfg.Thresholds.CPUSustainedMinutes * 60 / intervalSec
	if needed < 1 {
		needed = 1
	}
	if e.cpuBufSz != needed {
		e.cpuBuf = make([]float64, 0, needed)
		e.cpuBufSz = needed
	}
	e.cpuBuf = append(e.cpuBuf, cpuMetric.Value)
	if len(e.cpuBuf) > needed {
		e.cpuBuf = e.cpuBuf[len(e.cpuBuf)-needed:]
	}
	buf := make([]float64, len(e.cpuBuf))
	copy(buf, e.cpuBuf)
	e.mu.Unlock()

	// Only alert once the buffer is full.
	if len(buf) < needed {
		return
	}

	// Check if all values exceed the threshold.
	for _, v := range buf {
		if v < cfg.Thresholds.CPUPCT {
			return
		}
	}

	cooldownKey := "cpu_alert"
	cooldownDuration := time.Duration(cfg.Thresholds.CPUAlertCooldownMinutes) * time.Minute
	if !e.cooldown.Allow(cooldownKey, cooldownDuration) {
		return
	}

	log.Printf("engine: sustained CPU threshold exceeded: %.1f%% for %d minutes",
		cpuMetric.Value, cfg.Thresholds.CPUSustainedMinutes)

	details := fmt.Sprintf("cpu_pct=%.1f threshold=%.1f sustained_minutes=%d",
		cpuMetric.Value, cfg.Thresholds.CPUPCT, cfg.Thresholds.CPUSustainedMinutes)
	if err := e.db.InsertAction("cpu_alert", fmt.Sprintf("cpu.total > %.1f%% for %dm", cfg.Thresholds.CPUPCT, cfg.Thresholds.CPUSustainedMinutes), details, true); err != nil {
		log.Printf("engine: insert CPU action: %v", err)
	}

	subject := fmt.Sprintf("[VPS Watchdog] CPU Alert: %.1f%% for %d minutes", cpuMetric.Value, cfg.Thresholds.CPUSustainedMinutes)
	body := buildAlertBody("CPU", cpuMetric.Value, cfg.Thresholds.CPUPCT, nil, nil)
	if err := e.brevo.SendAlert(subject, body); err != nil {
		log.Printf("engine: send CPU alert: %v", err)
	}
}

func (e *Engine) checkDisk(cfg *config.Config, metricMap map[string]collector.Metric) {
	for name, m := range metricMap {
		if !strings.HasSuffix(name, ".used_pct") || !strings.HasPrefix(name, "disk.") {
			continue
		}
		if m.Value <= cfg.Thresholds.DiskPCT {
			continue
		}

		cooldownKey := "disk_alert_" + name
		cooldownDuration := time.Duration(cfg.Thresholds.DiskAlertCooldownHours) * time.Hour
		if !e.cooldown.Allow(cooldownKey, cooldownDuration) {
			continue
		}

		log.Printf("engine: disk threshold exceeded on %s: %.1f%% > %.1f%%", name, m.Value, cfg.Thresholds.DiskPCT)

		// Extract mount from metric name.
		mount := strings.TrimSuffix(strings.TrimPrefix(name, "disk."), ".used_pct")

		// Get top directories from DB for that mount (tags matching the path).
		topDirs := []string{"(run disk walk for details)"}

		if err := LogDiskAlert(mount, m.Value, topDirs, e.db); err != nil {
			log.Printf("engine: LogDiskAlert: %v", err)
		}

		subject := fmt.Sprintf("[VPS Watchdog] Disk Alert: %s at %.1f%%", mount, m.Value)
		body := buildAlertBody("DISK", m.Value, cfg.Thresholds.DiskPCT, nil, topDirs)
		if err := e.brevo.SendAlert(subject, body); err != nil {
			log.Printf("engine: send disk alert: %v", err)
		}
	}
}

func buildAlertBody(alertType string, triggerValue, threshold float64, actions, topProcs []string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<h2>%s Alert</h2>", alertType))
	sb.WriteString(fmt.Sprintf("<p>Current value: <strong>%.1f%%</strong> (threshold: %.1f%%)</p>", triggerValue, threshold))
	if len(actions) > 0 {
		sb.WriteString("<h3>Actions Taken</h3><ul>")
		for _, a := range actions {
			sb.WriteString("<li>" + a + "</li>")
		}
		sb.WriteString("</ul>")
	}
	if len(topProcs) > 0 {
		sb.WriteString("<h3>Top Processes</h3><ul>")
		for _, p := range topProcs {
			sb.WriteString("<li>" + p + "</li>")
		}
		sb.WriteString("</ul>")
	}
	return sb.String()
}
