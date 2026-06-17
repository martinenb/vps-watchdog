package action

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"vps-watchdog/internal/config"
	"vps-watchdog/internal/db"
)

// ExecuteAction runs a single CapAction and returns (success, durationMS, details, error).
func ExecuteAction(a config.CapAction, triggerValue float64, metricName string, database *db.DB) (bool, int64, string, error) {
	start := time.Now()
	var details string
	var err error
	var success bool

	switch a.Type {
	case "docker_stop":
		success, details, err = execDockerStop(a.Container)
	case "docker_restart":
		success, details, err = execDockerRestart(a.Container)
	case "docker_stop_idle":
		success, details, err = execDockerStopIdle(a, database)
	case "shell":
		success, details, err = execShell(a.Command, a.TimeoutS)
	case "http_webhook":
		success, details, err = execWebhook(a)
	case "email":
		// Email is handled by the engine (needs the brevo client). Just mark success and return.
		subject := strings.ReplaceAll(a.Subject, "{value}", fmt.Sprintf("%.1f", triggerValue))
		subject = strings.ReplaceAll(subject, "{metric}", metricName)
		details = "email: subject=" + subject
		success = true
	case "log_only":
		details = fmt.Sprintf("log_only: metric=%s value=%.2f", metricName, triggerValue)
		success = true
	default:
		details = "unknown action type: " + a.Type
		err = fmt.Errorf("unknown action type: %s", a.Type)
	}

	durationMS := time.Since(start).Milliseconds()
	return success, durationMS, details, err
}

func execDockerStop(container string) (bool, string, error) {
	if container == "" {
		return false, "", fmt.Errorf("docker_stop: container name is required")
	}
	before := CaptureRAMSnapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "stop", container).CombinedOutput()
	after := CaptureRAMSnapshot()
	details := fmt.Sprintf("container=%s before_ram=%s after_ram=%s output=%s", container, before, after, string(out))
	if err != nil {
		return false, details, fmt.Errorf("docker stop %s: %w", container, err)
	}
	log.Printf("action: container %q stopped", container)
	return true, details, nil
}

func execDockerRestart(container string) (bool, string, error) {
	if container == "" {
		return false, "", fmt.Errorf("docker_restart: container name is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "restart", container).CombinedOutput()
	details := fmt.Sprintf("container=%s output=%s", container, string(out))
	if err != nil {
		return false, details, fmt.Errorf("docker restart %s: %w", container, err)
	}
	log.Printf("action: container %q restarted", container)
	return true, details, nil
}

func execDockerStopIdle(a config.CapAction, database *db.DB) (bool, string, error) {
	idleCPU := a.IdleCPUPct
	if idleCPU == 0 {
		idleCPU = 0.5
	}
	idleMin := a.IdleMinutes
	if idleMin == 0 {
		idleMin = 10
	}
	// Get running containers
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "ps", "--format", "{{.Names}}").Output()
	if err != nil {
		return false, "", fmt.Errorf("docker ps: %w", err)
	}
	names := strings.Fields(string(out))
	var stopped []string
	for _, name := range names {
		idleDur := time.Duration(idleMin) * time.Minute
		idle, err := database.IsContainerIdle(name, idleCPU, idleDur)
		if err != nil || !idle {
			continue
		}
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		stopOut, stopErr := exec.CommandContext(stopCtx, "docker", "stop", name).CombinedOutput()
		stopCancel()
		if stopErr == nil {
			stopped = append(stopped, name)
			log.Printf("action: docker_stop_idle stopped container %q", name)
		} else {
			log.Printf("action: docker_stop_idle failed for %q: %v (output: %s)", name, stopErr, string(stopOut))
		}
	}
	details := fmt.Sprintf("idle_cpu_pct=%.1f idle_minutes=%d stopped=%v", idleCPU, idleMin, stopped)
	return len(stopped) > 0, details, nil
}

func execShell(command string, timeoutS int) (bool, string, error) {
	if command == "" {
		return false, "", fmt.Errorf("shell: command is required")
	}
	if timeoutS <= 0 {
		timeoutS = 30
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutS)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	out, err := cmd.CombinedOutput()
	details := fmt.Sprintf("command=%q output=%s", command, string(out))
	if err != nil {
		return false, details, fmt.Errorf("shell %q: %w", command, err)
	}
	log.Printf("action: shell command %q succeeded", command)
	return true, details, nil
}

func execWebhook(a config.CapAction) (bool, string, error) {
	if a.URL == "" {
		return false, "", fmt.Errorf("http_webhook: url is required")
	}
	method := a.Method
	if method == "" {
		method = "POST"
	}
	body := strings.NewReader(a.Body)
	req, err := http.NewRequestWithContext(context.Background(), method, a.URL, body)
	if err != nil {
		return false, "", fmt.Errorf("webhook create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Sprintf("url=%s", a.URL), fmt.Errorf("webhook %s: %w", a.URL, err)
	}
	defer resp.Body.Close()
	details := fmt.Sprintf("url=%s method=%s status=%d", a.URL, method, resp.StatusCode)
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	if !ok {
		return false, details, fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return true, details, nil
}

// StopContainer stops a specific Docker container (kept for backward compat with web handler).
func StopContainer(name string, reason string, database *db.DB) (bool, error) {
	start := time.Now()
	success, details, err := execDockerStop(name)
	durationMS := time.Since(start).Milliseconds()
	_ = database.InsertAction("docker_stop", reason, details, success, durationMS, "manual")
	return success, err
}

// StartContainer starts a Docker container (kept for backward compat with web handler).
func StartContainer(name string, database *db.DB) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "start", name).CombinedOutput()
	success := err == nil
	details := fmt.Sprintf("container=%s output=%s", name, string(out))
	_ = database.InsertAction("docker_start", "manual", details, success, 0, "manual")
	if err != nil {
		return false, fmt.Errorf("docker start %s: %w", name, err)
	}
	return true, nil
}

// LogDiskAlert logs a disk threshold alert (kept for backward compat).
func LogDiskAlert(mount string, pct float64, topDirs []string, database *db.DB) error {
	details := fmt.Sprintf("mount=%s used_pct=%.1f%% top_dirs=%v", mount, pct, topDirs)
	return database.InsertAction("disk_alert", fmt.Sprintf("disk.%s.used_pct > threshold", mount), details, true, 0, "disk_check")
}

// CaptureRAMSnapshot returns a human-readable string of current RAM usage.
func CaptureRAMSnapshot() string {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return "unavailable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "free", "-h").Output()
	if err != nil {
		return "unavailable"
	}
	return string(out)
}
