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

// EmailSender is a minimal interface for sending alert emails.
type EmailSender interface {
	SendAlert(subject, body string) error
}

// Engine evaluates collected metrics against configured caps and triggers actions.
type Engine struct {
	db       *db.DB
	cooldown *CooldownRegistry
	brevo    EmailSender
	mu       sync.Mutex
}

func New(cfg *config.Config, database *db.DB, emailSender EmailSender) *Engine {
	return &Engine{
		db:       database,
		cooldown: NewCooldownRegistry(),
		brevo:    emailSender,
	}
}

func (e *Engine) UpdateConfig(cfg *config.Config) {
	// No-op: config is read fresh from config.Get() on each Evaluate call.
}

// Evaluate inspects the latest metrics against all configured caps.
func (e *Engine) Evaluate(metrics []collector.Metric) {
	cfg := config.Get()

	// Build a snapshot map: name → value.
	snapshot := make(map[string]float64, len(metrics))
	for _, m := range metrics {
		snapshot[m.Name] = m.Value
	}

	for _, cap := range cfg.Caps {
		if !cap.Enabled {
			continue
		}
		e.evaluateCap(cap, snapshot)
	}
}

func (e *Engine) evaluateCap(cap config.Cap, snapshot map[string]float64) {
	value, ok := snapshot[cap.Metric]
	if !ok {
		return
	}

	// Evaluate operator
	triggered := false
	switch cap.Operator {
	case ">":
		triggered = value > cap.Threshold
	case ">=":
		triggered = value >= cap.Threshold
	case "<":
		triggered = value < cap.Threshold
	case "<=":
		triggered = value <= cap.Threshold
	case "==":
		triggered = value == cap.Threshold
	default:
		triggered = value > cap.Threshold
	}

	if !triggered {
		return
	}

	// Check cooldown
	cooldownKey := "cap_" + cap.Name
	cooldownDur := time.Duration(cap.CooldownMinutes) * time.Minute
	if cooldownDur == 0 {
		cooldownDur = 30 * time.Minute
	}
	if !e.cooldown.Allow(cooldownKey, cooldownDur) {
		return
	}

	// Check schedule window
	if cap.RespectSchedule && !e.isActionAllowed() {
		log.Printf("engine: cap %q triggered but outside schedule window (value=%.2f)", cap.Name, value)
		return
	}

	log.Printf("engine: cap %q triggered: %s %.2f %s %.2f",
		cap.Name, cap.Metric, value, cap.Operator, cap.Threshold)

	// Execute each action
	for _, a := range cap.Actions {
		if a.Type == "email" {
			subject := strings.ReplaceAll(a.Subject, "{value}", fmt.Sprintf("%.1f", value))
			subject = strings.ReplaceAll(subject, "{metric}", cap.Metric)
			if subject == "" {
				subject = fmt.Sprintf("[VPS Watchdog] %s: %s %.1f %s %.1f", cap.Name, cap.Metric, value, cap.Operator, cap.Threshold)
			}
			body := buildAlertBody(cap.Name, cap.Metric, value, cap.Threshold, cap.Operator)
			start := time.Now()
			err := e.brevo.SendAlert(subject, body)
			durationMS := time.Since(start).Milliseconds()
			success := err == nil
			if err != nil {
				log.Printf("engine: cap %q email action failed: %v", cap.Name, err)
			}
			_ = e.db.InsertAction("email", fmt.Sprintf("%s %s %.2f", cap.Metric, cap.Operator, cap.Threshold),
				"subject="+subject, success, durationMS, cap.Name)
			continue
		}

		success, durationMS, details, err := ExecuteAction(a, value, cap.Metric, e.db)
		if err != nil {
			log.Printf("engine: cap %q action %s failed: %v", cap.Name, a.Type, err)
		}
		_ = e.db.InsertAction(a.Type, fmt.Sprintf("cap=%s metric=%s value=%.2f", cap.Name, cap.Metric, value),
			details, success, durationMS, cap.Name)
	}
}

func (e *Engine) isActionAllowed() bool {
	cfg := config.Get()
	if !cfg.Schedule.Enabled || len(cfg.Schedule.Windows) == 0 {
		return true
	}

	loc, err := time.LoadLocation(cfg.Schedule.Timezone)
	if err != nil {
		log.Printf("engine: invalid timezone %q: %v, using UTC", cfg.Schedule.Timezone, err)
		loc = time.UTC
	}

	now := time.Now().In(loc)
	dayNames := []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
	dayName := dayNames[now.Weekday()]
	currentMinutes := now.Hour()*60 + now.Minute()

	for _, w := range cfg.Schedule.Windows {
		dayMatch := false
		for _, d := range w.Days {
			if d == "*" || d == dayName {
				dayMatch = true
				break
			}
		}
		if !dayMatch {
			continue
		}
		var startH, startM, endH, endM int
		fmt.Sscanf(w.Start, "%d:%d", &startH, &startM)
		fmt.Sscanf(w.End, "%d:%d", &endH, &endM)
		if currentMinutes >= startH*60+startM && currentMinutes < endH*60+endM {
			return true
		}
	}
	return false
}

func buildAlertBody(capName, metric string, value, threshold float64, operator string) string {
	return fmt.Sprintf(`<h2>%s</h2>
<p>Metric: <strong>%s</strong></p>
<p>Current value: <strong>%.2f</strong> (threshold: %s %.2f)</p>
<p>Time: %s</p>`,
		capName, metric, value, operator, threshold,
		time.Now().Format("2006-01-02 15:04:05 UTC"))
}
