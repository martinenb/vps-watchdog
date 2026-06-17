package config

import (
	"sync"

	"github.com/BurntSushi/toml"
)

type Config struct {
	General    GeneralConfig    `toml:"general"`
	Schedule   ScheduleConfig   `toml:"schedule"`
	Collection CollectionConfig `toml:"collection"`
	Thresholds ThresholdConfig  `toml:"thresholds"`
	Docker     DockerConfig     `toml:"docker"`
	Brevo      BrevoConfig      `toml:"brevo"`
	Recipients RecipientsConfig `toml:"recipients"`
	Weekly     WeeklyConfig     `toml:"weekly"`
	DiskWalk   DiskWalkConfig   `toml:"disk_walk"`
	Database   DBConfig         `toml:"database"`
	Web        WebConfig        `toml:"web"`
	Caps       []Cap            `toml:"caps"`
}

// Cap defines a threshold level with associated actions.
type Cap struct {
	Name            string      `toml:"name"`
	Description     string      `toml:"description"`
	Metric          string      `toml:"metric"`   // e.g. "ram.used_pct", "cpu.total", "disk.root.used_pct"
	Operator        string      `toml:"operator"` // ">", "<", ">=", "<=", "=="
	Threshold       float64     `toml:"threshold"`
	CooldownMinutes int         `toml:"cooldown_minutes"`
	RespectSchedule bool        `toml:"respect_schedule"`
	Enabled         bool        `toml:"enabled"`
	Actions         []CapAction `toml:"action"`
}

// CapAction defines what to do when a cap is triggered.
type CapAction struct {
	Type string `toml:"type"`
	// docker_stop / docker_restart: specify a container name
	Container string `toml:"container"`
	// docker_stop_idle: stop containers that have been idle
	IdleCPUPct  float64 `toml:"idle_cpu_pct"`
	IdleMinutes int     `toml:"idle_minutes"`
	// shell: run an arbitrary command
	Command  string `toml:"command"`
	TimeoutS int    `toml:"timeout_s"`
	// email: send an alert with custom subject (supports {value} and {metric} placeholders)
	Subject string `toml:"subject"`
	// http_webhook: POST/GET to a URL
	URL    string `toml:"url"`
	Method string `toml:"method"` // default "POST"
	Body   string `toml:"body"`
}

type GeneralConfig struct {
	IntervalSeconds int    `toml:"interval_seconds"`
	LogDir          string `toml:"log_dir"`
	DBPath          string `toml:"db_path"`
	TopProcessesN   int    `toml:"top_processes_n"`
}

// TimeWindow defines a time range when automated actions are allowed.
type TimeWindow struct {
	Days  []string `toml:"days"`  // ["mon","tue","wed","thu","fri","sat","sun"] or ["*"]
	Start string   `toml:"start"` // "07:00"
	End   string   `toml:"end"`   // "22:00"
}

type ScheduleConfig struct {
	Enabled  bool         `toml:"enabled"`
	Timezone string       `toml:"timezone"` // e.g. "Europe/Paris"
	Windows  []TimeWindow `toml:"windows"`
}

// CollectionConfig defines how often each collector runs.
type CollectionConfig struct {
	RAMIntervalS     int `toml:"ram_interval_s"`
	CPUIntervalS     int `toml:"cpu_interval_s"`
	NetworkIntervalS int `toml:"network_interval_s"`
	DockerIntervalS  int `toml:"docker_interval_s"`
	DiskIntervalS    int `toml:"disk_interval_s"`
	ProcessIntervalS int `toml:"process_interval_s"`
	SystemIntervalS  int `toml:"system_interval_s"`
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

// DBConfig controls data retention.
type DBConfig struct {
	RawTTLHours    int `toml:"raw_ttl_hours"`    // default 48
	HourlyTTLDays  int `toml:"hourly_ttl_days"`  // default 90
	WeeklyTTLWeeks int `toml:"weekly_ttl_weeks"` // default 52
	MaxSizeMB      int `toml:"max_size_mb"`      // alert threshold, default 500
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
	// Schedule defaults
	if cfg.Schedule.Timezone == "" {
		cfg.Schedule.Timezone = "Europe/Paris"
	}
	// Collection interval defaults
	if cfg.Collection.RAMIntervalS == 0 {
		cfg.Collection.RAMIntervalS = 5
	}
	if cfg.Collection.CPUIntervalS == 0 {
		cfg.Collection.CPUIntervalS = 5
	}
	if cfg.Collection.NetworkIntervalS == 0 {
		cfg.Collection.NetworkIntervalS = 5
	}
	if cfg.Collection.DockerIntervalS == 0 {
		cfg.Collection.DockerIntervalS = 15
	}
	if cfg.Collection.DiskIntervalS == 0 {
		cfg.Collection.DiskIntervalS = 60
	}
	if cfg.Collection.ProcessIntervalS == 0 {
		cfg.Collection.ProcessIntervalS = 30
	}
	if cfg.Collection.SystemIntervalS == 0 {
		cfg.Collection.SystemIntervalS = 30
	}
	// Database TTL defaults
	if cfg.Database.RawTTLHours == 0 {
		cfg.Database.RawTTLHours = 48
	}
	if cfg.Database.HourlyTTLDays == 0 {
		cfg.Database.HourlyTTLDays = 90
	}
	if cfg.Database.WeeklyTTLWeeks == 0 {
		cfg.Database.WeeklyTTLWeeks = 52
	}
	if cfg.Database.MaxSizeMB == 0 {
		cfg.Database.MaxSizeMB = 500
	}
	// Cap defaults
	for i := range cfg.Caps {
		if cfg.Caps[i].CooldownMinutes == 0 {
			cfg.Caps[i].CooldownMinutes = 30
		}
		for j := range cfg.Caps[i].Actions {
			if cfg.Caps[i].Actions[j].TimeoutS == 0 {
				cfg.Caps[i].Actions[j].TimeoutS = 30
			}
			if cfg.Caps[i].Actions[j].Method == "" {
				cfg.Caps[i].Actions[j].Method = "POST"
			}
		}
	}
}
