package web

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	actionpkg "vps-watchdog/internal/action"
	"vps-watchdog/internal/collector"
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
	protected.HandleFunc("/api/db/stats", s.handleDBStats)
	protected.HandleFunc("/api/db/vacuum", s.handleDBVacuum)
	protected.HandleFunc("/api/db/cleanup", s.handleDBCleanup)
	protected.HandleFunc("/api/metrics/names", s.handleMetricNames)
	protected.HandleFunc("/api/metrics/query", s.handleMetricsQuery)
	protected.HandleFunc("/api/config/full", s.handleConfigFull)
	protected.HandleFunc("/api/caps", s.handleCaps)
	protected.HandleFunc("/api/metrics/action-durations", s.handleActionDurations)

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

// handleDockerList returns current Docker container metrics with real-time status.
func (s *Server) handleDockerList(w http.ResponseWriter, r *http.Request) {
	// Get real-time status from docker ps --all
	type psLine struct {
		Names  string `json:"Names"`
		Status string `json:"Status"`
		State  string `json:"State"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "ps", "--all", "--format", "{{json .}}").Output()

	liveStatus := map[string]string{} // name -> "running" or "stopped"
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line == "" {
				continue
			}
			var ps psLine
			if json.Unmarshal([]byte(line), &ps) == nil {
				st := "stopped"
				if strings.HasPrefix(strings.ToLower(ps.Status), "up") || ps.State == "running" {
					st = "running"
				}
				liveStatus[ps.Names] = st
			}
		}
	}

	// Get metrics from DB
	containers, err := s.db.QueryDockerMetrics()
	if err != nil {
		log.Printf("web: QueryDockerMetrics: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Merge: override status with live data
	for i := range containers {
		if st, ok := liveStatus[containers[i].Name]; ok {
			containers[i].Status = st
		}
	}

	// Also add containers that exist in docker ps but have no DB metrics yet
	dbNames := map[string]bool{}
	for _, c := range containers {
		dbNames[c.Name] = true
	}
	for name, st := range liveStatus {
		if !dbNames[name] {
			containers = append(containers, collector.DockerMetric{Name: name, Status: st})
		}
	}

	// Sort by name
	sort.Slice(containers, func(i, j int) bool { return containers[i].Name < containers[j].Name })

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

func (s *Server) handleDBStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.db.Stats(s.cfg.General.DBPath)
	if err != nil {
		http.Error(w, "error", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (s *Server) handleDBVacuum(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if err := s.db.Vacuum(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (s *Server) handleDBCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	cfg := config.Get()
	if err := s.db.CleanupWithTTL(cfg.Database.RawTTLHours, cfg.Database.HourlyTTLDays, cfg.Database.WeeklyTTLWeeks); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (s *Server) handleMetricNames(w http.ResponseWriter, r *http.Request) {
	names, err := s.db.QueryMetricNames()
	if err != nil {
		http.Error(w, "error", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(names)
}

func (s *Server) handleMetricsQuery(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name required", 400)
		return
	}
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	granularity := r.URL.Query().Get("granularity") // "raw" or "hourly"

	now := time.Now().Unix()
	fromTS := now - 86400 // default 24h
	toTS := now

	if fromStr != "" {
		if v, err := strconv.ParseInt(fromStr, 10, 64); err == nil {
			fromTS = v
		}
	}
	if toStr != "" {
		if v, err := strconv.ParseInt(toStr, 10, 64); err == nil {
			toTS = v
		}
	}

	useHourly := granularity != "raw"
	if toTS-fromTS <= 7200 { // < 2h → use raw
		useHourly = false
	}

	points, err := s.db.QueryRange(name, fromTS, toTS, useHourly)
	if err != nil {
		http.Error(w, "error", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(points)
}

// handleConfigFull handles GET/POST for the complete config (all sections).
func (s *Server) handleConfigFull(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := config.Get()
		// Return full config, redacting secrets
		type safeConfig struct {
			General    config.GeneralConfig    `json:"general"`
			Schedule   config.ScheduleConfig   `json:"schedule"`
			Collection config.CollectionConfig `json:"collection"`
			Thresholds config.ThresholdConfig  `json:"thresholds"`
			Docker     config.DockerConfig     `json:"docker"`
			Brevo      struct {
				SenderEmail string `json:"sender_email"`
				SenderName  string `json:"sender_name"`
				HasAPIKey   bool   `json:"has_api_key"`
			} `json:"brevo"`
			Recipients config.RecipientsConfig `json:"recipients"`
			Weekly     config.WeeklyConfig     `json:"weekly"`
			DiskWalk   config.DiskWalkConfig   `json:"disk_walk"`
			Database   config.DBConfig         `json:"database"`
			Web        struct {
				Port    int  `json:"port"`
				Enabled bool `json:"enabled"`
			} `json:"web"`
		}
		sc := safeConfig{
			General:    cfg.General,
			Schedule:   cfg.Schedule,
			Collection: cfg.Collection,
			Thresholds: cfg.Thresholds,
			Docker:     cfg.Docker,
			Recipients: cfg.Recipients,
			Weekly:     cfg.Weekly,
			DiskWalk:   cfg.DiskWalk,
			Database:   cfg.Database,
		}
		sc.Brevo.SenderEmail = cfg.Brevo.SenderEmail
		sc.Brevo.SenderName = cfg.Brevo.SenderName
		sc.Brevo.HasAPIKey = cfg.Brevo.APIKey != "" && cfg.Brevo.APIKey != "YOUR_BREVO_API_KEY"
		sc.Web.Port = cfg.Web.Port
		sc.Web.Enabled = cfg.Web.Enabled
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sc)

	case http.MethodPost:
		var updates map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		cfgPath := config.GetPath()
		cfg, err := config.Load(cfgPath)
		if err != nil {
			http.Error(w, "could not load config", 500)
			return
		}

		// Apply updates per section
		if raw, ok := updates["schedule"]; ok {
			json.Unmarshal(raw, &cfg.Schedule)
		}
		if raw, ok := updates["collection"]; ok {
			json.Unmarshal(raw, &cfg.Collection)
		}
		if raw, ok := updates["thresholds"]; ok {
			json.Unmarshal(raw, &cfg.Thresholds)
		}
		if raw, ok := updates["docker"]; ok {
			// Partial update — don't overwrite stop_order unless explicitly provided
			var dUpdate map[string]json.RawMessage
			if json.Unmarshal(raw, &dUpdate) == nil {
				if v, ok := dUpdate["auto_stop"]; ok {
					json.Unmarshal(v, &cfg.Docker.AutoStop)
				}
				if v, ok := dUpdate["idle_cpu_pct"]; ok {
					json.Unmarshal(v, &cfg.Docker.IdleCPUPct)
				}
				if v, ok := dUpdate["idle_duration_minutes"]; ok {
					json.Unmarshal(v, &cfg.Docker.IdleDurationMinutes)
				}
				if v, ok := dUpdate["stop_order"]; ok {
					json.Unmarshal(v, &cfg.Docker.StopOrder)
				}
			}
		}
		if raw, ok := updates["recipients"]; ok {
			json.Unmarshal(raw, &cfg.Recipients)
		}
		if raw, ok := updates["weekly"]; ok {
			json.Unmarshal(raw, &cfg.Weekly)
		}
		if raw, ok := updates["disk_walk"]; ok {
			json.Unmarshal(raw, &cfg.DiskWalk)
		}
		if raw, ok := updates["database"]; ok {
			json.Unmarshal(raw, &cfg.Database)
		}
		if raw, ok := updates["brevo"]; ok {
			var bUpdate map[string]string
			if json.Unmarshal(raw, &bUpdate) == nil {
				if v, ok := bUpdate["api_key"]; ok && v != "" && v != "[REDACTED]" {
					cfg.Brevo.APIKey = v
				}
				if v, ok := bUpdate["sender_email"]; ok {
					cfg.Brevo.SenderEmail = v
				}
				if v, ok := bUpdate["sender_name"]; ok {
					cfg.Brevo.SenderName = v
				}
			}
		}

		if raw, ok := updates["caps"]; ok {
			json.Unmarshal(raw, &cfg.Caps)
		}

		if err := writeConfig(cfgPath, cfg); err != nil {
			http.Error(w, "could not save config", 500)
			return
		}
		config.Reload(cfgPath)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// handleCaps handles GET (list caps) and POST (save caps to config).
func (s *Server) handleCaps(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := config.Get()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cfg.Caps)
	case http.MethodPost:
		var caps []config.Cap
		if err := json.NewDecoder(r.Body).Decode(&caps); err != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		cfgPath := config.GetPath()
		cfg, err := config.Load(cfgPath)
		if err != nil {
			http.Error(w, "could not load config", 500)
			return
		}
		cfg.Caps = caps
		if err := writeConfig(cfgPath, cfg); err != nil {
			http.Error(w, "could not save", 500)
			return
		}
		config.Reload(cfgPath)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// handleActionDurations returns action execution times for graphing.
func (s *Server) handleActionDurations(w http.ResponseWriter, r *http.Request) {
	hoursStr := r.URL.Query().Get("hours")
	hours := 168
	if hoursStr != "" {
		if h, err := strconv.Atoi(hoursStr); err == nil && h > 0 {
			hours = h
		}
	}
	endTS := time.Now().Unix()
	startTS := endTS - int64(hours)*3600
	points, err := s.db.QueryActionDurations(startTS, endTS)
	if err != nil {
		http.Error(w, "error", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(points)
}
