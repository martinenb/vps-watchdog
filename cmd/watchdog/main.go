package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	actionpkg "vps-watchdog/internal/action"
	"vps-watchdog/internal/collector"
	"vps-watchdog/internal/config"
	"vps-watchdog/internal/db"
	"vps-watchdog/internal/report"
	"vps-watchdog/internal/web"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "./config.toml", "path to config.toml")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("vps-watchdog", version)
		os.Exit(0)
	}

	// Load config.
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	config.SetPath(*cfgPath)
	config.Current = cfg

	// Setup logging: write to both stdout and a log file.
	if err := os.MkdirAll(cfg.General.LogDir, 0755); err != nil {
		log.Printf("warning: could not create log dir %s: %v", cfg.General.LogDir, err)
	}
	logFile, err := os.OpenFile(
		filepath.Join(cfg.General.LogDir, "watchdog.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY,
		0644,
	)
	if err != nil {
		log.Printf("warning: could not open log file: %v", err)
	} else {
		defer logFile.Close()
		log.SetOutput(io.MultiWriter(os.Stdout, logFile))
	}
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	log.Printf("vps-watchdog %s starting — config: %s", version, *cfgPath)

	// Create DB directory if needed.
	if err := os.MkdirAll(filepath.Dir(cfg.General.DBPath), 0755); err != nil {
		log.Printf("warning: could not create DB dir: %v", err)
	}

	// Open database.
	database, err := db.New(cfg.General.DBPath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer database.Close()
	log.Printf("database opened: %s", cfg.General.DBPath)

	// Init collectors.
	collectors := []collector.Collector{
		collector.NewRAMCollector(cfg.General.TopProcessesN),
		collector.NewCPUCollector(),
		collector.NewDiskCollector(cfg.DiskWalk.Paths, cfg.DiskWalk.MaxDepth, cfg.DiskWalk.TopDirsN),
		collector.NewNetworkCollector(),
		collector.NewDockerCollector(),
		collector.NewSystemCollector(),
	}

	// Init Brevo email client.
	brevoClient := report.New(cfg)

	// Init action engine.
	engine := actionpkg.New(cfg, database, brevoClient)

	// Init graph builder.
	graphs := report.NewGraphBuilder(database, cfg)

	// Init weekly scheduler.
	scheduler := report.NewScheduler(database, cfg, brevoClient, graphs)

	// Init web server.
	var webServer *web.Server
	if cfg.Web.Enabled {
		webServer = web.New(cfg, database, engine, scheduler, graphs)
	}

	// SSE broadcaster: queries latest metrics and broadcasts to hub every 5s.
	var sseHub interface{ Broadcast(string) }
	if webServer != nil {
		sseHub = webServer.Hub()
	}

	// Start background goroutines.
	scheduler.Start()
	log.Printf("weekly scheduler started")

	stopCh := make(chan struct{})

	// Collection loop.
	go func() {
		ticker := time.NewTicker(time.Duration(cfg.General.IntervalSeconds) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				runCollection(collectors, database, engine)
			}
		}
	}()

	// SSE broadcast loop.
	if sseHub != nil {
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-stopCh:
					return
				case <-ticker.C:
					broadcastMetrics(database, sseHub)
				}
			}
		}()
	}

	// Web server.
	if webServer != nil {
		go func() {
			log.Printf("web server starting on port %d", cfg.Web.Port)
			if err := webServer.Start(); err != nil {
				log.Printf("web server error: %v", err)
			}
		}()
	}

	// Signal handling.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	for sig := range sigCh {
		switch sig {
		case syscall.SIGHUP:
			log.Printf("received SIGHUP, reloading config from %s", *cfgPath)
			if err := config.Reload(*cfgPath); err != nil {
				log.Printf("config reload error: %v", err)
			} else {
				newCfg := config.Get()
				engine.UpdateConfig(newCfg)
				log.Printf("config reloaded successfully")
			}
		case syscall.SIGTERM, syscall.SIGINT:
			log.Printf("received %s, shutting down...", sig)
			close(stopCh)
			scheduler.Stop()
			if webServer != nil {
				webServer.Stop()
			}
			database.Close()
			log.Printf("shutdown complete")
			return
		}
	}
}

// runCollection runs all collectors and processes the resulting metrics.
func runCollection(collectors []collector.Collector, database *db.DB, engine *actionpkg.Engine) {
	var allMetrics []collector.Metric

	for _, c := range collectors {
		metrics, err := c.Collect()
		if err != nil {
			log.Printf("collector %s error: %v", c.Name(), err)
			continue
		}
		allMetrics = append(allMetrics, metrics...)
	}

	if len(allMetrics) == 0 {
		return
	}

	if err := database.InsertMetrics(allMetrics); err != nil {
		log.Printf("db insert metrics: %v", err)
	}

	engine.Evaluate(allMetrics)
}

// broadcastMetrics queries the latest metric values and broadcasts them to the SSE hub.
func broadcastMetrics(database *db.DB, hub interface{ Broadcast(string) }) {
	names := []string{
		"ram.used_pct", "cpu.total", "disk.root.used_pct",
		"system.swap_pct", "cpu.load_1", "cpu.load_5",
		"net.bytes_recv_delta", "net.bytes_sent_delta",
	}
	latest, err := database.QueryLatest(names)
	if err != nil {
		log.Printf("broadcastMetrics QueryLatest: %v", err)
		return
	}

	docker, err := database.QueryDockerMetrics()
	if err != nil {
		log.Printf("broadcastMetrics QueryDockerMetrics: %v", err)
	}

	type dockerItem struct {
		Name   string  `json:"name"`
		CPUPct float64 `json:"cpu_pct"`
		MemPct float64 `json:"mem_pct"`
		MemMB  float64 `json:"mem_mb"`
		Status string  `json:"status"`
	}
	dockerItems := make([]dockerItem, 0, len(docker))
	for _, d := range docker {
		dockerItems = append(dockerItems, dockerItem{
			Name:   d.Name,
			CPUPct: d.CPUPct,
			MemPct: d.MemPct,
			MemMB:  d.MemMB,
			Status: d.Status,
		})
	}

	payload := map[string]interface{}{
		"ts":          time.Now().Unix(),
		"ram_pct":     latest["ram.used_pct"],
		"cpu_pct":     latest["cpu.total"],
		"disk_pct":    latest["disk.root.used_pct"],
		"swap_pct":    latest["system.swap_pct"],
		"load_1":      latest["cpu.load_1"],
		"load_5":      latest["cpu.load_5"],
		"net_recv_kb": latest["net.bytes_recv_delta"] / 1024,
		"net_sent_kb": latest["net.bytes_sent_delta"] / 1024,
		"docker":      dockerItems,
	}

	b, err := json.Marshal(payload)
	if err != nil {
		log.Printf("broadcastMetrics marshal: %v", err)
		return
	}
	hub.Broadcast(string(b))
}
