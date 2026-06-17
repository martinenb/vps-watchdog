package report

import (
	"fmt"
	"log"
	"time"

	"vps-watchdog/internal/config"
	"vps-watchdog/internal/db"
)

// WeeklyScheduler runs the weekly report and hourly DB rollups.
type WeeklyScheduler struct {
	database *db.DB
	cfg      *config.Config
	brevo    *BrevoClient
	graphs   *GraphBuilder
	stop     chan struct{}
}

// NewScheduler creates a new WeeklyScheduler.
func NewScheduler(database *db.DB, cfg *config.Config, brevo *BrevoClient, graphs *GraphBuilder) *WeeklyScheduler {
	return &WeeklyScheduler{
		database: database,
		cfg:      cfg,
		brevo:    brevo,
		graphs:   graphs,
		stop:     make(chan struct{}),
	}
}

// Start runs the scheduler in the background. Call this once.
func (s *WeeklyScheduler) Start() {
	go s.rollupLoop()
	go s.weeklyLoop()
}

// Stop signals the scheduler goroutines to exit.
func (s *WeeklyScheduler) Stop() {
	close(s.stop)
}

// rollupLoop performs hourly DB rollups and daily cleanups.
func (s *WeeklyScheduler) rollupLoop() {
	// Run the first rollup shortly after startup.
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	// Immediately rollup the previous complete hour.
	s.runRollup()

	for {
		select {
		case <-s.stop:
			return
		case t := <-ticker.C:
			// Rollup the previous complete hour.
			prevHour := t.UTC().Add(-time.Hour)
			hourTS := hourStart(prevHour)
			log.Printf("scheduler: rolling up hour %s", time.Unix(hourTS, 0).UTC().Format(time.RFC3339))
			if err := s.database.RollupHour(hourTS); err != nil {
				log.Printf("scheduler: RollupHour error: %v", err)
			}
			// Daily cleanup: run around midnight UTC.
			if t.UTC().Hour() == 0 {
				log.Printf("scheduler: running daily cleanup")
				if err := s.database.CleanupOld(); err != nil {
					log.Printf("scheduler: CleanupOld error: %v", err)
				}
			}
		}
	}
}

func (s *WeeklyScheduler) runRollup() {
	now := time.Now().UTC()
	prevHour := now.Add(-time.Hour)
	hourTS := hourStart(prevHour)
	if err := s.database.RollupHour(hourTS); err != nil {
		log.Printf("scheduler: initial RollupHour error: %v", err)
	}
}

// weeklyLoop waits until the next Monday at cfg.Weekly.HourUTC and sends the weekly report.
func (s *WeeklyScheduler) weeklyLoop() {
	for {
		next := nextMondayAt(s.cfg.Weekly.HourUTC)
		log.Printf("scheduler: next weekly report at %s", next.Format(time.RFC3339))

		timer := time.NewTimer(time.Until(next))
		select {
		case <-s.stop:
			timer.Stop()
			return
		case <-timer.C:
			log.Printf("scheduler: sending weekly report")
			if err := s.SendWeeklyReport(); err != nil {
				log.Printf("scheduler: weekly report error: %v", err)
			}
			// Sleep 2 minutes to avoid firing twice.
			time.Sleep(2 * time.Minute)
		}
	}
}

// SendWeeklyReport compiles and sends the weekly email immediately.
// It is exported so the web handler can trigger it on demand.
func (s *WeeklyScheduler) SendWeeklyReport() error {
	now := time.Now().UTC()
	weekEnd := now
	weekStartTime := weekEnd.AddDate(0, 0, -7)

	weekTS := hourStart(weekStartTime)
	if err := s.database.ComputeWeeklySummary(weekTS); err != nil {
		log.Printf("scheduler: ComputeWeeklySummary: %v", err)
	}

	// Query summaries for comparison.
	weeklySummaries, err := s.database.QueryWeeklySummaries(s.cfg.Weekly.WeeksComparison)
	if err != nil {
		log.Printf("scheduler: QueryWeeklySummaries: %v", err)
	}

	// Build weekly stats for template.
	weekStatMap := map[int64]*WeekStat{}
	for _, ws := range weeklySummaries {
		st, ok := weekStatMap[ws.WeekTS]
		if !ok {
			t := time.Unix(ws.WeekTS, 0).UTC()
			st = &WeekStat{WeekLabel: t.Format("Jan 02")}
			weekStatMap[ws.WeekTS] = st
		}
		switch ws.Name {
		case "ram.used_pct":
			st.AvgRAM = ws.AvgVal
		case "cpu.total":
			st.MaxCPU = ws.MaxVal
		case "disk.root.used_pct":
			st.AvgDisk = ws.AvgVal
		}
	}
	var weekStats []WeekStat
	for _, st := range weekStatMap {
		weekStats = append(weekStats, *st)
	}

	// Query current week avg/max from hourly.
	startTS := weekStartTime.Unix()
	endTS := weekEnd.Unix()
	ramPoints, _ := s.database.QueryRange("ram.used_pct", startTS, endTS, true)
	cpuPoints, _ := s.database.QueryRange("cpu.total", startTS, endTS, true)

	var avgRAM, maxRAM, avgCPU, maxCPU float64
	avgRAM, maxRAM = computeAvgMax(ramPoints)
	avgCPU, maxCPU = computeAvgMax(cpuPoints)

	// Query action log.
	actions, err := s.database.QueryActionLog(50)
	if err != nil {
		log.Printf("scheduler: QueryActionLog: %v", err)
	}
	// Filter to this week.
	var weekActions []db.ActionRecord
	for _, a := range actions {
		if a.TS >= startTS && a.TS <= endTS {
			weekActions = append(weekActions, a)
		}
	}

	data := WeeklyData{
		WeekStart:    weekStartTime.Format("January 2, 2006"),
		WeekEnd:      weekEnd.Format("January 2, 2006"),
		AvgRAM:       avgRAM,
		MaxRAM:       maxRAM,
		AvgCPU:       avgCPU,
		MaxCPU:       maxCPU,
		TotalActions: len(weekActions),
		ActionList:   weekActions,
		WeeklyStats:  weekStats,
	}

	htmlBody := WeeklyEmail(data)

	// Attach graphs if enabled.
	var attachments []Attachment
	if s.cfg.Weekly.IncludeGraphs {
		graphs := []struct {
			name string
			fn   func() ([]byte, error)
		}{
			{"ram_7d.png", s.graphs.RAMOverTime},
			{"cpu_7d.png", s.graphs.CPUOverTime},
			{"disk_7d.png", s.graphs.DiskOverTime},
			{"network_7d.png", s.graphs.NetworkOverTime},
			{"docker_mem.png", s.graphs.DockerMemory},
			{"weekly_comparison.png", s.graphs.WeeklyComparison},
		}
		for _, g := range graphs {
			png, err := g.fn()
			if err != nil {
				log.Printf("scheduler: generate graph %s: %v", g.name, err)
				continue
			}
			attachments = append(attachments, Attachment{Name: g.name, Content: png})
		}
	}

	subject := fmt.Sprintf("[VPS Watchdog] Weekly Report — %s", weekStartTime.Format("Jan 2, 2006"))
	return s.brevo.Send(subject, htmlBody, attachments)
}

func computeAvgMax(points []db.DataPoint) (avg, max float64) {
	if len(points) == 0 {
		return 0, 0
	}
	sum := 0.0
	for _, p := range points {
		sum += p.Value
		if p.Value > max {
			max = p.Value
		}
	}
	avg = sum / float64(len(points))
	return
}

func hourStart(t time.Time) int64 {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, time.UTC).Unix()
}

func nextMondayAt(hourUTC int) time.Time {
	now := time.Now().UTC()
	// Find the upcoming Monday.
	daysUntilMonday := (int(time.Monday) - int(now.Weekday()) + 7) % 7
	if daysUntilMonday == 0 {
		// Today is Monday — check if the hour has passed.
		if now.Hour() >= hourUTC {
			daysUntilMonday = 7
		}
	}
	next := time.Date(now.Year(), now.Month(), now.Day()+daysUntilMonday, hourUTC, 0, 0, 0, time.UTC)
	return next
}
