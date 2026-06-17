package config

import (
	"sync"

	"github.com/BurntSushi/toml"
)

// Config is the top-level configuration structure.
type Config struct {
	General    GeneralConfig    `toml:"general"`
	Thresholds ThresholdConfig  `toml:"thresholds"`
	Docker     DockerConfig     `toml:"docker"`
	Brevo      BrevoConfig      `toml:"brevo"`
	Recipients RecipientsConfig `toml:"recipients"`
	Weekly     WeeklyConfig     `toml:"weekly"`
	DiskWalk   DiskWalkConfig   `toml:"disk_walk"`
	Web        WebConfig        `toml:"web"`
}

type GeneralConfig struct {
	IntervalSeconds int    `toml:"interval_seconds"`
	LogDir          string `toml:"log_dir"`
	DBPath          string `toml:"db_path"`
	TopProcessesN   int    `toml:"top_processes_n"`
}

type ThresholdConfig struct {
	RAMPCT                  float64 `toml:"ram_pct"`
	CPUPCT                  float64 `toml:"cpu_pct"`
	CPUSustainedMinutes     int     `toml:"cpu_sustained_minutes"`
	DiskPCT                 float64 `toml:"disk_pct"`
	DiskAlertCooldownHours  int     `toml:"disk_alert_cooldown_hours"`
	RAMAlertCooldownMinutes int     `toml:"ram_alert_cooldown_minutes"`
	CPUAlertCooldownMinutes int     `toml:"cpu_alert_cooldown_minutes"`
}

type DockerConfig struct {
	IdleCPUPct          float64  `toml:"idle_cpu_pct"`
	IdleDurationMinutes int      `toml:"idle_duration_minutes"`
	AutoStop            bool     `toml:"auto_stop"`
	StopOrder           []string `toml:"stop_order"`
}

type BrevoConfig struct {
	APIKey      string `toml:"api_key"`
	SenderEmail string `toml:"sender_email"`
	SenderName  string `toml:"sender_name"`
}

type RecipientsConfig struct {
	Emails []string `toml:"emails"`
}

type WeeklyConfig struct {
	HourUTC         int  `toml:"hour_utc"`
	IncludeGraphs   bool `toml:"include_graphs"`
	WeeksComparison int  `toml:"weeks_comparison"`
}

type DiskWalkConfig struct {
	Paths    []string `toml:"paths"`
	MaxDepth int      `toml:"max_depth"`
	TopDirsN int      `toml:"top_dirs_n"`
}

type WebConfig struct {
	Port     int    `toml:"port"`
	Username string `toml:"username"`
	Password string `toml:"password"`
	Enabled  bool   `toml:"enabled"`
}

var (
	Current *Config
	mu      sync.RWMutex
	cfgPath string
)

// Load reads and parses a TOML config file, applies defaults for zero values.
func Load(path string) (*Config, error) {
	cfg := &Config{}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, err
	}
	applyDefaults(cfg)
	return cfg, nil
}

// Reload re-reads the config file and atomically replaces Current.
func Reload(path string) error {
	cfg, err := Load(path)
	if err != nil {
		return err
	}
	mu.Lock()
	Current = cfg
	cfgPath = path
	mu.Unlock()
	return nil
}

// Get returns a pointer to the current config under a read lock.
func Get() *Config {
	mu.RLock()
	defer mu.RUnlock()
	return Current
}

// SetPath records the path used at startup so Reload() can be called with no args.
func SetPath(path string) {
	mu.Lock()
	cfgPath = path
	mu.Unlock()
}

// GetPath returns the last loaded config path.
func GetPath() string {
	mu.RLock()
	defer mu.RUnlock()
	return cfgPath
}

func applyDefaults(cfg *Config) {
	if cfg.General.IntervalSeconds == 0 {
		cfg.General.IntervalSeconds = 30
	}
	if cfg.General.LogDir == "" {
		cfg.General.LogDir = "/var/log/watchdog"
	}
	if cfg.General.DBPath == "" {
		cfg.General.DBPath = "/var/lib/watchdog/metrics.db"
	}
	if cfg.General.TopProcessesN == 0 {
		cfg.General.TopProcessesN = 10
	}
	if cfg.Thresholds.RAMPCT == 0 {
		cfg.Thresholds.RAMPCT = 85.0
	}
	if cfg.Thresholds.CPUPCT == 0 {
		cfg.Thresholds.CPUPCT = 90.0
	}
	if cfg.Thresholds.CPUSustainedMinutes == 0 {
		cfg.Thresholds.CPUSustainedMinutes = 5
	}
	if cfg.Thresholds.DiskPCT == 0 {
		cfg.Thresholds.DiskPCT = 90.0
	}
	if cfg.Thresholds.DiskAlertCooldownHours == 0 {
		cfg.Thresholds.DiskAlertCooldownHours = 6
	}
	if cfg.Thresholds.RAMAlertCooldownMinutes == 0 {
		cfg.Thresholds.RAMAlertCooldownMinutes = 60
	}
	if cfg.Thresholds.CPUAlertCooldownMinutes == 0 {
		cfg.Thresholds.CPUAlertCooldownMinutes = 30
	}
	if cfg.Docker.IdleCPUPct == 0 {
		cfg.Docker.IdleCPUPct = 0.5
	}
	if cfg.Docker.IdleDurationMinutes == 0 {
		cfg.Docker.IdleDurationMinutes = 10
	}
	if cfg.Weekly.WeeksComparison == 0 {
		cfg.Weekly.WeeksComparison = 4
	}
	if cfg.Weekly.HourUTC == 0 {
		cfg.Weekly.HourUTC = 8
	}
	if cfg.DiskWalk.MaxDepth == 0 {
		cfg.DiskWalk.MaxDepth = 4
	}
	if cfg.DiskWalk.TopDirsN == 0 {
		cfg.DiskWalk.TopDirsN = 15
	}
	if cfg.Web.Port == 0 {
		cfg.Web.Port = 47832
	}
	if cfg.Web.Username == "" {
		cfg.Web.Username = "admin"
	}
	if cfg.Web.Password == "" {
		cfg.Web.Password = "changeme"
	}
}
