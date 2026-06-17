package web

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	actionpkg "vps-watchdog/internal/action"
	"vps-watchdog/internal/config"
)

func (s *Server) buildRoutes() http.Handler {
	mux := http.NewServeMux()

	// Public endpoints (no auth).
	mux.HandleFunc("/health", s.handleHealth)

	// All other routes require basic auth.
	protected := http.NewServeMux()
	protected.Handle("/static/", http.FileServer(http.FS(staticFiles)))
	protected.HandleFunc("/", s.handleIndex)

	// API routes.
	protected.HandleFunc("/api/metrics/live", s.handleSSE)
	protected.HandleFunc("/api/metrics/history", s.handleMetricsHistory)
	protected.HandleFunc("/api/metrics/latest", s.handleMetricsLatest)
	protected.HandleFunc("/api/docker", s.handleDockerList)
	protected.HandleFunc("/api/docker/order", s.handleDockerOrder)
	protected.HandleFunc("/api/docker/", s.handleDockerAction) // /api/docker/{name}/stop|start
	protected.HandleFunc("/api/actions", s.handleActions)
	protected.HandleFunc("/api/config", s.handleConfig)
	protected.HandleFunc("/api/logs", s.handleLogs)
	protected.HandleFunc("/api/graphs/", s.handleGraphs)
	protected.HandleFunc("/api/report/test", s.handleReportTest)

	mux.Handle("/", s.basicAuth(protected))
	return mux
}

// handleHealth returns {"ok": true} with no auth required.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// handleIndex serves the main dashboard HTML.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	content, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(content)
}

// handleSSE streams live metrics as Server-Sent Events.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := make(chan string, 16)
	s.hub.Register(ch)
	defer s.hub.Unregister(ch)

	// Send a heartbeat comment immediately.
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-ticker.C:
			// Heartbeat to keep connection alive.
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// handleMetricsHistory returns historical data for a given metric.
func (s *Server) handleMetricsHistory(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	hoursStr := r.URL.Query().Get("hours")
	hours := 168 // 7 days default
	if hoursStr != "" {
		if h, err := strconv.Atoi(hoursStr); err == nil && h > 0 {
			hours = h
		}
	}

	endTS := time.Now().Unix()
	startTS := endTS - int64(hours)*3600
	useHourly := hours > 6

	points, err := s.db.QueryRange(name, startTS, endTS, useHourly)
	if err != nil {
		log.Printf("web: QueryRange %s: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(points)
}

// handleMetricsLatest returns the latest values for the standard dashboard metrics.
func (s *Server) handleMetricsLatest(w http.ResponseWriter, r *http.Request) {
	names := []string{
		"ram.used_pct", "cpu.total", "disk.root.used_pct",
		"system.swap_pct", "cpu.load_1", "cpu.load_5", "cpu.load_15",
		"net.bytes_recv_delta", "net.bytes_sent_delta", "system.open_fds",
	}
	latest, err := s.db.QueryLatest(names)
	if err != nil {
		log.Printf("web: QueryLatest: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(latest)
}

// handleDockerList returns current Docker container metrics.
func (s *Server) handleDockerList(w http.ResponseWriter, r *http.Request) {
	containers, err := s.db.QueryDockerMetrics()
	if err != nil {
		log.Printf("web: QueryDockerMetrics: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(containers)
}

// handleDockerOrder handles GET/POST for the Docker container stop order.
func (s *Server) handleDockerOrder(w http.ResponseWriter, r *http.Request) {
	cfg := config.Get()
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cfg.Docker.StopOrder)
	case http.MethodPost:
		var order []string
		if err := json.NewDecoder(r.Body).Decode(&order); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		// Update config and persist.
		cfg.Docker.StopOrder = order
		if err := saveConfigField(config.GetPath(), "docker", "stop_order", order); err != nil {
			log.Printf("web: save docker order: %v", err)
			http.Error(w, "could not save config", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleDockerAction handles /api/docker/{name}/stop and /api/docker/{name}/start.
func (s *Server) handleDockerAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse path: /api/docker/{name}/{action}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/docker/"), "/")
	if len(parts) < 2 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	containerName := parts[0]
	actionName := parts[1]

	switch actionName {
	case "stop":
		ok, err := actionpkg.StopContainer(containerName, "manual via web UI", s.db)
		if err != nil {
			log.Printf("web: stop container %s: %v", containerName, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": ok})
	case "start":
		ok, err := actionpkg.StartContainer(containerName, s.db)
		if err != nil {
			log.Printf("web: start container %s: %v", containerName, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": ok})
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
	}
}

// handleActions returns the recent action log.
func (s *Server) handleActions(w http.ResponseWriter, r *http.Request) {
	actions, err := s.db.QueryActionLog(50)
	if err != nil {
		log.Printf("web: QueryActionLog: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(actions)
}

// handleConfig handles GET/POST for configuration.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := config.Get()
		// Redact sensitive fields.
		safe := map[string]interface{}{
			"general":    cfg.General,
			"thresholds": cfg.Thresholds,
			"docker": map[string]interface{}{
				"idle_cpu_pct":          cfg.Docker.IdleCPUPct,
				"idle_duration_minutes": cfg.Docker.IdleDurationMinutes,
				"auto_stop":             cfg.Docker.AutoStop,
				"stop_order":            cfg.Docker.StopOrder,
			},
			"brevo": map[string]interface{}{
				"api_key":      "[REDACTED]",
				"sender_email": cfg.Brevo.SenderEmail,
				"sender_name":  cfg.Brevo.SenderName,
			},
			"recipients": cfg.Recipients,
			"weekly":     cfg.Weekly,
			"disk_walk":  cfg.DiskWalk,
			"web": map[string]interface{}{
				"port":     cfg.Web.Port,
				"username": cfg.Web.Username,
				"password": "[REDACTED]",
				"enabled":  cfg.Web.Enabled,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(safe)

	case http.MethodPost:
		var updates map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		// Only allow updating thresholds and docker auto_stop from the web UI.
		// Full config reload on success.
		cfgPath := config.GetPath()
		if err := applyConfigUpdates(cfgPath, updates); err != nil {
			log.Printf("web: apply config updates: %v", err)
			http.Error(w, "could not update config", http.StatusInternalServerError)
			return
		}
		if err := config.Reload(cfgPath); err != nil {
			log.Printf("web: reload config: %v", err)
			http.Error(w, "could not reload config", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleLogs tails the watchdog log file.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	linesStr := r.URL.Query().Get("lines")
	maxLines := 200
	if linesStr != "" {
		if n, err := strconv.Atoi(linesStr); err == nil && n > 0 && n <= 10000 {
			maxLines = n
		}
	}

	logPath := filepath.Join(s.cfg.General.LogDir, "watchdog.log")
	lines, err := tailFile(logPath, maxLines)
	if err != nil {
		// Not fatal — log dir might not exist yet.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]string{"Log file not available: " + err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(lines)
}

// handleGraphs returns a PNG chart for the given type.
func (s *Server) handleGraphs(w http.ResponseWriter, r *http.Request) {
	graphType := strings.TrimPrefix(r.URL.Path, "/api/graphs/")

	var (
		png []byte
		err error
	)
	switch graphType {
	case "ram":
		png, err = s.graphs.RAMOverTime()
	case "cpu":
		png, err = s.graphs.CPUOverTime()
	case "disk":
		png, err = s.graphs.DiskOverTime()
	case "network":
		png, err = s.graphs.NetworkOverTime()
	case "docker":
		png, err = s.graphs.DockerMemory()
	case "weekly":
		png, err = s.graphs.WeeklyComparison()
	default:
		http.Error(w, "unknown graph type", http.StatusBadRequest)
		return
	}

	if err != nil {
		log.Printf("web: generate graph %s: %v", graphType, err)
		http.Error(w, "graph generation failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "max-age=60")
	w.Write(png)
}

// handleReportTest triggers an immediate weekly report.
func (s *Server) handleReportTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	go func() {
		if err := s.scheduler.SendWeeklyReport(); err != nil {
			log.Printf("web: test report: %v", err)
		}
	}()
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true,"message":"Report queued"}`))
}

// tailFile returns the last n lines of a file.
func tailFile(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return nil, err
	}

	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// applyConfigUpdates writes a subset of config values back to the TOML file.
// Only safe, non-sensitive fields are accepted.
func applyConfigUpdates(path string, updates map[string]interface{}) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}

	if thresholds, ok := updates["thresholds"].(map[string]interface{}); ok {
		if v, ok := thresholds["ram_pct"].(float64); ok {
			cfg.Thresholds.RAMPCT = v
		}
		if v, ok := thresholds["cpu_pct"].(float64); ok {
			cfg.Thresholds.CPUPCT = v
		}
		if v, ok := thresholds["disk_pct"].(float64); ok {
			cfg.Thresholds.DiskPCT = v
		}
		if v, ok := thresholds["cpu_sustained_minutes"].(float64); ok {
			cfg.Thresholds.CPUSustainedMinutes = int(v)
		}
		if v, ok := thresholds["ram_alert_cooldown_minutes"].(float64); ok {
			cfg.Thresholds.RAMAlertCooldownMinutes = int(v)
		}
		if v, ok := thresholds["cpu_alert_cooldown_minutes"].(float64); ok {
			cfg.Thresholds.CPUAlertCooldownMinutes = int(v)
		}
		if v, ok := thresholds["disk_alert_cooldown_hours"].(float64); ok {
			cfg.Thresholds.DiskAlertCooldownHours = int(v)
		}
	}
	if docker, ok := updates["docker"].(map[string]interface{}); ok {
		if v, ok := docker["auto_stop"].(bool); ok {
			cfg.Docker.AutoStop = v
		}
		if v, ok := docker["idle_cpu_pct"].(float64); ok {
			cfg.Docker.IdleCPUPct = v
		}
		if v, ok := docker["idle_duration_minutes"].(float64); ok {
			cfg.Docker.IdleDurationMinutes = int(v)
		}
	}

	return writeConfig(path, cfg)
}

// saveConfigField reloads the config file and updates a specific nested field.
func saveConfigField(path, section, field string, value interface{}) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	switch {
	case section == "docker" && field == "stop_order":
		if order, ok := value.([]string); ok {
			cfg.Docker.StopOrder = order
		}
	}
	return writeConfig(path, cfg)
}

// writeConfig marshals the config struct back to TOML and writes it.
func writeConfig(path string, cfg *config.Config) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("writeConfig create: %w", err)
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}
