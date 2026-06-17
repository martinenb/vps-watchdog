package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"vps-watchdog/internal/collector"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite connection.
type DB struct {
	db *sql.DB
	mu sync.Mutex
}

// Metric mirrors collector.Metric for insertion.
type Metric = collector.Metric

// DataPoint mirrors collector.DataPoint.
type DataPoint = collector.DataPoint

// ActionRecord mirrors collector.ActionRecord.
type ActionRecord = collector.ActionRecord

// DockerMetric mirrors collector.DockerMetric.
type DockerMetric = collector.DockerMetric

// WeeklySummary mirrors collector.WeeklySummary.
type WeeklySummary = collector.WeeklySummary

// ProcessInfo mirrors collector.ProcessInfo.
type ProcessInfo = collector.ProcessInfo

const schema = `
CREATE TABLE IF NOT EXISTS metrics_raw (
    id       INTEGER PRIMARY KEY,
    ts       INTEGER NOT NULL,
    category TEXT NOT NULL,
    name     TEXT NOT NULL,
    value    REAL NOT NULL,
    tags     TEXT
);
CREATE INDEX IF NOT EXISTS idx_raw_ts_name ON metrics_raw(ts, name);

CREATE TABLE IF NOT EXISTS metrics_hourly (
    id       INTEGER PRIMARY KEY,
    hour_ts  INTEGER NOT NULL,
    category TEXT NOT NULL,
    name     TEXT NOT NULL,
    min_val  REAL NOT NULL,
    max_val  REAL NOT NULL,
    avg_val  REAL NOT NULL,
    p95_val  REAL NOT NULL,
    tags     TEXT,
    UNIQUE(hour_ts, name, COALESCE(tags,''))
);
CREATE INDEX IF NOT EXISTS idx_hourly_ts_name ON metrics_hourly(hour_ts, name);

CREATE TABLE IF NOT EXISTS action_log (
    id          INTEGER PRIMARY KEY,
    ts          INTEGER NOT NULL,
    action_type TEXT NOT NULL,
    trigger     TEXT NOT NULL,
    details     TEXT NOT NULL,
    success     INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS weekly_summary (
    id       INTEGER PRIMARY KEY,
    week_ts  INTEGER NOT NULL,
    name     TEXT NOT NULL,
    avg_val  REAL NOT NULL,
    max_val  REAL NOT NULL,
    p95_val  REAL NOT NULL,
    UNIQUE(week_ts, name)
);
`

// New opens the SQLite database and runs schema migrations.
func New(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("db open: %w", err)
	}
	sqlDB.SetMaxOpenConns(1) // SQLite is single-writer
	if _, err := sqlDB.Exec(schema); err != nil {
		return nil, fmt.Errorf("db schema: %w", err)
	}
	return &DB{db: sqlDB}, nil
}

// Close closes the underlying database connection.
func (d *DB) Close() error {
	return d.db.Close()
}

// InsertMetrics bulk-inserts metrics in a single transaction.
func (d *DB) InsertMetrics(metrics []Metric) error {
	if len(metrics) == 0 {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("InsertMetrics begin: %w", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO metrics_raw(ts, category, name, value, tags) VALUES(?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("InsertMetrics prepare: %w", err)
	}
	defer stmt.Close()

	for _, m := range metrics {
		var tagsJSON *string
		if len(m.Tags) > 0 {
			b, _ := json.Marshal(m.Tags)
			s := string(b)
			tagsJSON = &s
		}
		if _, err := stmt.Exec(m.TS, m.Category, m.Name, m.Value, tagsJSON); err != nil {
			tx.Rollback()
			return fmt.Errorf("InsertMetrics exec: %w", err)
		}
	}
	return tx.Commit()
}

// InsertAction logs an automated action.
func (d *DB) InsertAction(actionType, trigger, details string, success bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	successInt := 0
	if success {
		successInt = 1
	}
	_, err := d.db.Exec(
		`INSERT INTO action_log(ts, action_type, trigger, details, success) VALUES(?,?,?,?,?)`,
		time.Now().Unix(), actionType, trigger, details, successInt,
	)
	return err
}

// QueryRange returns data points for a metric within [startTS, endTS].
// If useHourly is true and endTS-startTS > 3600, it uses the hourly table.
func (d *DB) QueryRange(name string, startTS, endTS int64, useHourly bool) ([]DataPoint, error) {
	var rows *sql.Rows
	var err error

	if useHourly {
		rows, err = d.db.Query(
			`SELECT hour_ts, avg_val FROM metrics_hourly WHERE name=? AND hour_ts>=? AND hour_ts<=? ORDER BY hour_ts`,
			name, startTS, endTS,
		)
	} else {
		rows, err = d.db.Query(
			`SELECT ts, value FROM metrics_raw WHERE name=? AND ts>=? AND ts<=? ORDER BY ts`,
			name, startTS, endTS,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("QueryRange: %w", err)
	}
	defer rows.Close()

	var points []DataPoint
	for rows.Next() {
		var dp DataPoint
		if err := rows.Scan(&dp.TS, &dp.Value); err != nil {
			return nil, err
		}
		points = append(points, dp)
	}
	return points, rows.Err()
}

// QueryLatest returns the most recent value for each requested metric name.
func (d *DB) QueryLatest(names []string) (map[string]float64, error) {
	result := make(map[string]float64, len(names))
	for _, name := range names {
		var value float64
		err := d.db.QueryRow(
			`SELECT value FROM metrics_raw WHERE name=? ORDER BY ts DESC LIMIT 1`, name,
		).Scan(&value)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("QueryLatest %s: %w", name, err)
		}
		result[name] = value
	}
	return result, nil
}

// QueryTopProcesses returns the top processes by RSS from the latest ram.proc.* metrics.
func (d *DB) QueryTopProcesses(limit int) ([]ProcessInfo, error) {
	rows, err := d.db.Query(`
		SELECT r.name, r.value, r.tags
		FROM metrics_raw r
		INNER JOIN (
			SELECT name, MAX(ts) as max_ts FROM metrics_raw WHERE name LIKE 'ram.proc.%' GROUP BY name
		) latest ON r.name = latest.name AND r.ts = latest.max_ts
		ORDER BY r.value DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("QueryTopProcesses: %w", err)
	}
	defer rows.Close()

	var procs []ProcessInfo
	for rows.Next() {
		var name string
		var value float64
		var tagsJSON sql.NullString
		if err := rows.Scan(&name, &value, &tagsJSON); err != nil {
			return nil, err
		}
		pi := ProcessInfo{RSS: uint64(value)}
		if tagsJSON.Valid {
			tags := map[string]string{}
			if err := json.Unmarshal([]byte(tagsJSON.String), &tags); err == nil {
				pi.Name = tags["name"]
				pi.ContainerID = tags["container_id"]
			}
		}
		procs = append(procs, pi)
	}
	return procs, rows.Err()
}

// QueryActionLog returns the most recent action records.
func (d *DB) QueryActionLog(limit int) ([]ActionRecord, error) {
	rows, err := d.db.Query(
		`SELECT id, ts, action_type, trigger, details, success FROM action_log ORDER BY ts DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("QueryActionLog: %w", err)
	}
	defer rows.Close()

	var records []ActionRecord
	for rows.Next() {
		var r ActionRecord
		var successInt int
		if err := rows.Scan(&r.ID, &r.TS, &r.ActionType, &r.Trigger, &r.Details, &successInt); err != nil {
			return nil, err
		}
		r.Success = successInt == 1
		records = append(records, r)
	}
	return records, rows.Err()
}

// RollupHour aggregates raw metrics for a given hour timestamp into metrics_hourly,
// then deletes raw rows older than 48 hours.
func (d *DB) RollupHour(hourTS int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	endTS := hourTS + 3600

	// Fetch all distinct metric names for this hour.
	rows, err := d.db.Query(
		`SELECT DISTINCT name, category FROM metrics_raw WHERE ts >= ? AND ts < ?`, hourTS, endTS,
	)
	if err != nil {
		return fmt.Errorf("RollupHour names: %w", err)
	}
	type nameCategory struct{ name, category string }
	var names []nameCategory
	for rows.Next() {
		var nc nameCategory
		if err := rows.Scan(&nc.name, &nc.category); err != nil {
			rows.Close()
			return err
		}
		names = append(names, nc)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}

	for _, nc := range names {
		valRows, err := tx.Query(
			`SELECT value FROM metrics_raw WHERE name=? AND ts>=? AND ts<?`, nc.name, hourTS, endTS,
		)
		if err != nil {
			tx.Rollback()
			return err
		}
		var vals []float64
		for valRows.Next() {
			var v float64
			if err := valRows.Scan(&v); err != nil {
				valRows.Close()
				tx.Rollback()
				return err
			}
			vals = append(vals, v)
		}
		valRows.Close()

		if len(vals) == 0 {
			continue
		}

		minV, maxV, avgV := computeStats(vals)
		p95 := computeP95(vals)

		_, err = tx.Exec(`
			INSERT INTO metrics_hourly(hour_ts, category, name, min_val, max_val, avg_val, p95_val)
			VALUES(?,?,?,?,?,?,?)
			ON CONFLICT(hour_ts, name, COALESCE(tags,'')) DO UPDATE SET
				min_val=excluded.min_val, max_val=excluded.max_val,
				avg_val=excluded.avg_val, p95_val=excluded.p95_val`,
			hourTS, nc.category, nc.name, minV, maxV, avgV, p95,
		)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("RollupHour insert: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// Delete raw data older than 48h.
	cutoff := time.Now().Unix() - 48*3600
	d.db.Exec(`DELETE FROM metrics_raw WHERE ts < ?`, cutoff)
	return nil
}

// CleanupOld removes hourly metrics older than 90 days.
func (d *DB) CleanupOld() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	cutoff := time.Now().Unix() - 90*24*3600
	_, err := d.db.Exec(`DELETE FROM metrics_hourly WHERE hour_ts < ?`, cutoff)
	return err
}

// ComputeWeeklySummary aggregates hourly metrics for the given week start timestamp.
func (d *DB) ComputeWeeklySummary(weekTS int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	endTS := weekTS + 7*24*3600

	rows, err := d.db.Query(
		`SELECT DISTINCT name FROM metrics_hourly WHERE hour_ts>=? AND hour_ts<?`, weekTS, endTS,
	)
	if err != nil {
		return fmt.Errorf("ComputeWeeklySummary names: %w", err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		names = append(names, n)
	}
	rows.Close()

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}

	for _, name := range names {
		valRows, err := tx.Query(
			`SELECT avg_val FROM metrics_hourly WHERE name=? AND hour_ts>=? AND hour_ts<?`, name, weekTS, endTS,
		)
		if err != nil {
			tx.Rollback()
			return err
		}
		var vals []float64
		for valRows.Next() {
			var v float64
			if err := valRows.Scan(&v); err != nil {
				valRows.Close()
				tx.Rollback()
				return err
			}
			vals = append(vals, v)
		}
		valRows.Close()

		if len(vals) == 0 {
			continue
		}
		_, maxV, avgV := computeStats(vals)
		p95 := computeP95(vals)

		_, err = tx.Exec(`
			INSERT INTO weekly_summary(week_ts, name, avg_val, max_val, p95_val)
			VALUES(?,?,?,?,?)
			ON CONFLICT(week_ts, name) DO UPDATE SET avg_val=excluded.avg_val, max_val=excluded.max_val, p95_val=excluded.p95_val`,
			weekTS, name, avgV, maxV, p95,
		)
		if err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// QueryWeeklySummaries returns aggregated weekly summaries for the last N weeks.
func (d *DB) QueryWeeklySummaries(weeks int) ([]WeeklySummary, error) {
	cutoff := weekStart(time.Now()).Unix() - int64(weeks)*7*24*3600
	rows, err := d.db.Query(
		`SELECT week_ts, name, avg_val, max_val, p95_val FROM weekly_summary WHERE week_ts >= ? ORDER BY week_ts, name`,
		cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("QueryWeeklySummaries: %w", err)
	}
	defer rows.Close()

	var summaries []WeeklySummary
	for rows.Next() {
		var s WeeklySummary
		if err := rows.Scan(&s.WeekTS, &s.Name, &s.AvgVal, &s.MaxVal, &s.P95Val); err != nil {
			return nil, err
		}
		summaries = append(summaries, s)
	}
	return summaries, rows.Err()
}

// QueryDockerMetrics returns the latest Docker container metrics.
func (d *DB) QueryDockerMetrics() ([]DockerMetric, error) {
	rows, err := d.db.Query(`
		SELECT r.name, r.value, r.tags
		FROM metrics_raw r
		INNER JOIN (
			SELECT name, MAX(ts) as max_ts FROM metrics_raw
			WHERE name LIKE 'docker.%'
			GROUP BY name
		) latest ON r.name = latest.name AND r.ts = latest.max_ts
		ORDER BY r.name`)
	if err != nil {
		return nil, fmt.Errorf("QueryDockerMetrics: %w", err)
	}
	defer rows.Close()

	dockerMap := map[string]*DockerMetric{}

	for rows.Next() {
		var name string
		var value float64
		var tagsJSON sql.NullString
		if err := rows.Scan(&name, &value, &tagsJSON); err != nil {
			return nil, err
		}

		var containerName, status string
		if tagsJSON.Valid {
			tags := map[string]string{}
			if err := json.Unmarshal([]byte(tagsJSON.String), &tags); err == nil {
				containerName = tags["container"]
				status = tags["status"]
			}
		}
		if containerName == "" {
			// Try to extract container name from metric name docker.{name}.{metric}
			parts := splitMetricName(name)
			if len(parts) >= 2 {
				containerName = parts[1]
			}
		}
		if containerName == "" {
			continue
		}

		dm, ok := dockerMap[containerName]
		if !ok {
			dm = &DockerMetric{Name: containerName, Status: status}
			dockerMap[containerName] = dm
		}
		if status != "" {
			dm.Status = status
		}

		if hasSuffix(name, ".cpu_pct") {
			dm.CPUPct = value
		} else if hasSuffix(name, ".mem_pct") {
			dm.MemPct = value
		} else if hasSuffix(name, ".mem_bytes") {
			dm.MemMB = value / (1 << 20)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := make([]DockerMetric, 0, len(dockerMap))
	for _, dm := range dockerMap {
		result = append(result, *dm)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// IsContainerIdle checks whether a container has been idle (cpu_pct < threshold) for the specified duration.
func (d *DB) IsContainerIdle(containerName string, cpuThreshold float64, duration time.Duration) (bool, error) {
	metricName := "docker." + sanitize(containerName) + ".cpu_pct"
	startTS := time.Now().Unix() - int64(duration.Seconds())

	rows, err := d.db.Query(
		`SELECT value FROM metrics_raw WHERE name=? AND ts>=? ORDER BY ts`, metricName, startTS,
	)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var v float64
		if err := rows.Scan(&v); err != nil {
			return false, err
		}
		if v >= cpuThreshold {
			return false, nil
		}
		count++
	}
	// Need at least some data points to make a determination.
	return count >= 2, rows.Err()
}

func sanitize(s string) string {
	replacer := map[rune]rune{'-': '_', '.': '_', '/': '_', ' ': '_'}
	var b []rune
	for _, c := range s {
		if r, ok := replacer[c]; ok {
			b = append(b, r)
		} else {
			b = append(b, c)
		}
	}
	return string(b)
}

func splitMetricName(name string) []string {
	var parts []string
	var cur []rune
	for _, c := range name {
		if c == '.' {
			parts = append(parts, string(cur))
			cur = cur[:0]
		} else {
			cur = append(cur, c)
		}
	}
	parts = append(parts, string(cur))
	return parts
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func computeStats(vals []float64) (min, max, avg float64) {
	min = math.MaxFloat64
	max = -math.MaxFloat64
	sum := 0.0
	for _, v := range vals {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		sum += v
	}
	avg = sum / float64(len(vals))
	return
}

func computeP95(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	sort.Float64s(sorted)
	idx := int(math.Ceil(0.95*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func weekStart(t time.Time) time.Time {
	t = t.UTC()
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	start := t.AddDate(0, 0, -(weekday - 1))
	return time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
}
