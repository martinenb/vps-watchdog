package action

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"time"

	"vps-watchdog/internal/db"
)

// StopContainer stops a Docker container, logs the action, and returns success.
func StopContainer(name string, reason string, database *db.DB) (bool, error) {
	before := CaptureRAMSnapshot()
	log.Printf("action: stopping container %q — reason: %s", name, reason)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "stop", name)
	output, err := cmd.CombinedOutput()
	success := err == nil

	after := CaptureRAMSnapshot()
	details := fmt.Sprintf("container=%s reason=%s before_ram=%s after_ram=%s output=%s",
		name, reason, before, after, string(output))

	if dbErr := database.InsertAction("docker_stop", reason, details, success); dbErr != nil {
		log.Printf("action: insert action log: %v", dbErr)
	}

	if err != nil {
		return false, fmt.Errorf("docker stop %s: %w (output: %s)", name, err, string(output))
	}
	log.Printf("action: container %q stopped successfully", name)
	return true, nil
}

// StartContainer starts a stopped Docker container.
func StartContainer(name string, database *db.DB) (bool, error) {
	log.Printf("action: starting container %q", name)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "start", name)
	output, err := cmd.CombinedOutput()
	success := err == nil

	details := fmt.Sprintf("container=%s output=%s", name, string(output))
	if dbErr := database.InsertAction("docker_start", "manual", details, success); dbErr != nil {
		log.Printf("action: insert action log: %v", dbErr)
	}

	if err != nil {
		return false, fmt.Errorf("docker start %s: %w (output: %s)", name, err, string(output))
	}
	log.Printf("action: container %q started successfully", name)
	return true, nil
}

// LogDiskAlert logs a disk threshold alert.
func LogDiskAlert(mount string, pct float64, topDirs []string, database *db.DB) error {
	details := fmt.Sprintf("mount=%s used_pct=%.1f%% top_dirs=%v", mount, pct, topDirs)
	return database.InsertAction("disk_alert", fmt.Sprintf("disk.%s.used_pct > threshold", mount), details, true)
}

// CaptureRAMSnapshot returns a human-readable string of current RAM usage.
func CaptureRAMSnapshot() string {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return "unavailable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "free", "-h")
	out, err := cmd.Output()
	if err != nil {
		// Fallback: just report memory stats via a simple command.
		return "unavailable"
	}
	return string(out)
}
