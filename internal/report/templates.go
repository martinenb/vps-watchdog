package report

import (
	"fmt"
	"strings"
	"time"

	"vps-watchdog/internal/collector"
)

// AlertData holds data for an immediate alert email.
type AlertData struct {
	Timestamp    string
	AlertType    string // "RAM", "CPU", "DISK"
	TriggerValue float64
	Threshold    float64
	Actions      []string
	TopProcesses []string
	BeforeRAM    string
	AfterRAM     string
}

// WeekStat holds a per-week summary row for the weekly email.
type WeekStat struct {
	WeekLabel string
	AvgRAM    float64
	MaxCPU    float64
	AvgDisk   float64
}

// WeeklyData holds data for the weekly report email.
type WeeklyData struct {
	WeekStart    string
	WeekEnd      string
	AvgRAM       float64
	MaxRAM       float64
	AvgCPU       float64
	MaxCPU       float64
	TotalActions int
	ActionList   []collector.ActionRecord
	WeeklyStats  []WeekStat
}

const emailCSS = `
body { margin:0; padding:0; background:#0d1117; color:#c9d1d9; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; }
.container { max-width:800px; margin:0 auto; padding:20px; }
.header { background:linear-gradient(135deg,#161b22,#1f2937); border-radius:12px; padding:30px; margin-bottom:24px; border-left:4px solid #58a6ff; }
.header h1 { margin:0 0 8px; font-size:24px; color:#58a6ff; }
.header p { margin:0; color:#8b949e; font-size:14px; }
.alert-badge { display:inline-block; padding:4px 12px; border-radius:20px; font-size:13px; font-weight:600; margin-bottom:16px; }
.alert-ram { background:#1f1a00; color:#d29922; border:1px solid #d29922; }
.alert-cpu { background:#1a1a00; color:#f85149; border:1px solid #f85149; }
.alert-disk { background:#1a1500; color:#d29922; border:1px solid #d29922; }
.card { background:#161b22; border-radius:8px; padding:20px; margin-bottom:16px; border:1px solid #30363d; }
.card h3 { margin:0 0 12px; font-size:16px; color:#58a6ff; }
.stat-row { display:flex; justify-content:space-between; padding:8px 0; border-bottom:1px solid #21262d; }
.stat-row:last-child { border-bottom:none; }
.stat-label { color:#8b949e; font-size:13px; }
.stat-value { color:#c9d1d9; font-size:13px; font-weight:600; }
.value-high { color:#f85149; }
.value-mid { color:#d29922; }
.value-ok { color:#3fb950; }
table { width:100%; border-collapse:collapse; font-size:13px; }
th { background:#21262d; color:#8b949e; padding:8px 12px; text-align:left; font-weight:600; border-bottom:1px solid #30363d; }
td { padding:8px 12px; border-bottom:1px solid #21262d; color:#c9d1d9; }
tr:last-child td { border-bottom:none; }
.success { color:#3fb950; }
.failure { color:#f85149; }
.footer { text-align:center; padding:20px; color:#484f58; font-size:12px; }
ul { margin:0; padding-left:20px; }
li { padding:4px 0; color:#c9d1d9; font-size:13px; }
`

// AlertEmail renders the immediate alert email as HTML.
func AlertEmail(data AlertData) string {
	badgeClass := "alert-ram"
	switch data.AlertType {
	case "CPU":
		badgeClass = "alert-cpu"
	case "DISK":
		badgeClass = "alert-disk"
	}

	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html><html><head><meta charset="UTF-8"><style>`)
	sb.WriteString(emailCSS)
	sb.WriteString(`</style></head><body><div class="container">`)

	// Header
	sb.WriteString(fmt.Sprintf(`
<div class="header">
  <h1>VPS Watchdog Alert</h1>
  <p>%s</p>
</div>
<span class="alert-badge %s">%s ALERT</span>
`, data.Timestamp, badgeClass, data.AlertType))

	// Stats card
	valueClass := "value-ok"
	if data.TriggerValue >= data.Threshold {
		valueClass = "value-high"
	}
	sb.WriteString(fmt.Sprintf(`
<div class="card">
  <h3>Alert Details</h3>
  <div class="stat-row"><span class="stat-label">Alert Type</span><span class="stat-value">%s</span></div>
  <div class="stat-row"><span class="stat-label">Current Value</span><span class="stat-value %s">%.1f%%</span></div>
  <div class="stat-row"><span class="stat-label">Threshold</span><span class="stat-value">%.1f%%</span></div>
  <div class="stat-row"><span class="stat-label">Time</span><span class="stat-value">%s</span></div>
</div>
`, data.AlertType, valueClass, data.TriggerValue, data.Threshold, data.Timestamp))

	// Actions card
	if len(data.Actions) > 0 {
		sb.WriteString(`<div class="card"><h3>Actions Taken</h3><ul>`)
		for _, a := range data.Actions {
			sb.WriteString(fmt.Sprintf(`<li>%s</li>`, a))
		}
		sb.WriteString(`</ul></div>`)
	}

	// RAM before/after card
	if data.BeforeRAM != "" {
		sb.WriteString(fmt.Sprintf(`
<div class="card">
  <h3>Memory Snapshot</h3>
  <div class="stat-row"><span class="stat-label">Before</span><span class="stat-value">%s</span></div>
  <div class="stat-row"><span class="stat-label">After</span><span class="stat-value">%s</span></div>
</div>
`, data.BeforeRAM, data.AfterRAM))
	}

	// Top processes card
	if len(data.TopProcesses) > 0 {
		sb.WriteString(`<div class="card"><h3>Top Processes by Memory</h3><ul>`)
		for _, p := range data.TopProcesses {
			sb.WriteString(fmt.Sprintf(`<li>%s</li>`, p))
		}
		sb.WriteString(`</ul></div>`)
	}

	sb.WriteString(`<div class="footer">VPS Watchdog — Automated Monitoring System</div>`)
	sb.WriteString(`</div></body></html>`)
	return sb.String()
}

// WeeklyEmail renders the weekly report email as HTML.
func WeeklyEmail(data WeeklyData) string {
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html><html><head><meta charset="UTF-8"><style>`)
	sb.WriteString(emailCSS)
	sb.WriteString(`</style></head><body><div class="container">`)

	// Header
	sb.WriteString(fmt.Sprintf(`
<div class="header">
  <h1>VPS Watchdog — Weekly Report</h1>
  <p>Week of %s to %s</p>
</div>
`, data.WeekStart, data.WeekEnd))

	// Summary card
	ramClass := colorClass(data.AvgRAM, 70, 85)
	cpuClass := colorClass(data.AvgCPU, 70, 85)
	sb.WriteString(fmt.Sprintf(`
<div class="card">
  <h3>Weekly Summary</h3>
  <div class="stat-row"><span class="stat-label">Average RAM Usage</span><span class="stat-value %s">%.1f%%</span></div>
  <div class="stat-row"><span class="stat-label">Peak RAM Usage</span><span class="stat-value %s">%.1f%%</span></div>
  <div class="stat-row"><span class="stat-label">Average CPU Usage</span><span class="stat-value %s">%.1f%%</span></div>
  <div class="stat-row"><span class="stat-label">Peak CPU Usage</span><span class="stat-value %s">%.1f%%</span></div>
  <div class="stat-row"><span class="stat-label">Total Automated Actions</span><span class="stat-value">%d</span></div>
</div>
`,
		ramClass, data.AvgRAM,
		colorClass(data.MaxRAM, 80, 90), data.MaxRAM,
		cpuClass, data.AvgCPU,
		colorClass(data.MaxCPU, 80, 90), data.MaxCPU,
		data.TotalActions))

	// Weekly comparison table
	if len(data.WeeklyStats) > 1 {
		sb.WriteString(`
<div class="card">
  <h3>Week-over-Week Comparison</h3>
  <table>
    <thead><tr><th>Week</th><th>Avg RAM</th><th>Max CPU</th><th>Avg Disk</th></tr></thead>
    <tbody>`)
		for _, ws := range data.WeeklyStats {
			sb.WriteString(fmt.Sprintf(`
      <tr>
        <td>%s</td>
        <td class="%s">%.1f%%</td>
        <td class="%s">%.1f%%</td>
        <td class="%s">%.1f%%</td>
      </tr>`,
				ws.WeekLabel,
				colorClass(ws.AvgRAM, 70, 85), ws.AvgRAM,
				colorClass(ws.MaxCPU, 70, 90), ws.MaxCPU,
				colorClass(ws.AvgDisk, 70, 85), ws.AvgDisk,
			))
		}
		sb.WriteString(`</tbody></table></div>`)
	}

	// Action log table
	if len(data.ActionList) > 0 {
		sb.WriteString(`
<div class="card">
  <h3>Automated Actions This Week</h3>
  <table>
    <thead><tr><th>Time</th><th>Type</th><th>Trigger</th><th>Status</th></tr></thead>
    <tbody>`)
		for _, a := range data.ActionList {
			t := time.Unix(a.TS, 0).UTC().Format("Jan 02 15:04")
			status := `<span class="success">OK</span>`
			if !a.Success {
				status = `<span class="failure">FAILED</span>`
			}
			sb.WriteString(fmt.Sprintf(`
      <tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
				t, a.ActionType, a.Trigger, status))
		}
		sb.WriteString(`</tbody></table></div>`)
	}

	sb.WriteString(`<div class="footer">VPS Watchdog — Automated Monitoring System<br>Charts attached as PNG images.</div>`)
	sb.WriteString(`</div></body></html>`)
	return sb.String()
}

func colorClass(val, warnThreshold, critThreshold float64) string {
	if val >= critThreshold {
		return "value-high"
	}
	if val >= warnThreshold {
		return "value-mid"
	}
	return "value-ok"
}
