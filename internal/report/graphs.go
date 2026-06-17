package report

import (
	"bytes"
	"fmt"
	"time"

	"github.com/wcharczuk/go-chart/v2"
	"github.com/wcharczuk/go-chart/v2/drawing"

	"vps-watchdog/internal/config"
	"vps-watchdog/internal/db"
)

// GraphBuilder generates PNG charts from stored metrics.
type GraphBuilder struct {
	db  *db.DB
	cfg *config.Config
}

// NewGraphBuilder creates a new GraphBuilder.
func NewGraphBuilder(database *db.DB, cfg *config.Config) *GraphBuilder {
	return &GraphBuilder{db: database, cfg: cfg}
}

// Palette of drawing.Color values used in charts.
var (
	colorDarkBG     = drawing.Color{R: 26, G: 26, B: 46, A: 255}
	colorDarkCard   = drawing.Color{R: 22, G: 33, B: 62, A: 255}
	colorAccentBlue = drawing.Color{R: 88, G: 166, B: 255, A: 255}
	colorAccentGreen = drawing.Color{R: 63, G: 185, B: 80, A: 255}
	colorAccentRed  = drawing.Color{R: 248, G: 81, B: 73, A: 255}
	colorAccentOrange = drawing.Color{R: 210, G: 153, B: 34, A: 255}
	colorText       = drawing.Color{R: 201, G: 209, B: 217, A: 255}
	colorGrid       = drawing.Color{R: 48, G: 54, B: 61, A: 255}
	colorPurple     = drawing.Color{R: 180, G: 100, B: 220, A: 255}
	colorCyan       = drawing.Color{R: 100, G: 220, B: 220, A: 255}
)

// alphaColor returns a copy of the color with reduced alpha.
func alphaColor(c drawing.Color, alpha uint8) drawing.Color {
	return drawing.Color{R: c.R, G: c.G, B: c.B, A: alpha}
}

// RAMOverTime returns a PNG of RAM usage over the last 7 days (hourly).
func (g *GraphBuilder) RAMOverTime() ([]byte, error) {
	endTS := time.Now().Unix()
	startTS := endTS - 7*24*3600
	points, err := g.db.QueryRange("ram.used_pct", startTS, endTS, true)
	if err != nil {
		return nil, fmt.Errorf("RAMOverTime query: %w", err)
	}
	return renderLineChart("RAM Usage (Last 7 Days)", "Time", "RAM %", points,
		colorAccentBlue, g.cfg.Thresholds.RAMPCT)
}

// CPUOverTime returns a PNG of CPU usage over the last 7 days (hourly).
func (g *GraphBuilder) CPUOverTime() ([]byte, error) {
	endTS := time.Now().Unix()
	startTS := endTS - 7*24*3600
	points, err := g.db.QueryRange("cpu.total", startTS, endTS, true)
	if err != nil {
		return nil, fmt.Errorf("CPUOverTime query: %w", err)
	}
	return renderLineChart("CPU Usage (Last 7 Days)", "Time", "CPU %", points,
		colorAccentGreen, g.cfg.Thresholds.CPUPCT)
}

// DiskOverTime returns a PNG of disk usage over the last 7 days (hourly).
func (g *GraphBuilder) DiskOverTime() ([]byte, error) {
	endTS := time.Now().Unix()
	startTS := endTS - 7*24*3600
	points, err := g.db.QueryRange("disk.root.used_pct", startTS, endTS, true)
	if err != nil {
		return nil, fmt.Errorf("DiskOverTime query: %w", err)
	}
	return renderLineChart("Disk Usage / (Last 7 Days)", "Time", "Disk %", points,
		colorAccentOrange, g.cfg.Thresholds.DiskPCT)
}

// NetworkOverTime returns a PNG of network throughput over the last 7 days (hourly).
func (g *GraphBuilder) NetworkOverTime() ([]byte, error) {
	endTS := time.Now().Unix()
	startTS := endTS - 7*24*3600

	recvPoints, err := g.db.QueryRange("net.bytes_recv_delta", startTS, endTS, true)
	if err != nil {
		return nil, fmt.Errorf("NetworkOverTime recv query: %w", err)
	}
	sentPoints, err := g.db.QueryRange("net.bytes_sent_delta", startTS, endTS, true)
	if err != nil {
		return nil, fmt.Errorf("NetworkOverTime sent query: %w", err)
	}

	// Convert bytes to KB.
	for i := range recvPoints {
		recvPoints[i].Value /= 1024
	}
	for i := range sentPoints {
		sentPoints[i].Value /= 1024
	}

	return renderMultiLineChart("Network Traffic (Last 7 Days)", "Time", "KB/interval",
		[]seriesData{
			{name: "Recv", points: recvPoints, color: colorAccentBlue},
			{name: "Sent", points: sentPoints, color: colorAccentGreen},
		})
}

// DockerMemory returns a PNG of per-container memory usage over the last 24 hours.
func (g *GraphBuilder) DockerMemory() ([]byte, error) {
	containers, err := g.db.QueryDockerMetrics()
	if err != nil {
		return nil, fmt.Errorf("DockerMemory query: %w", err)
	}

	endTS := time.Now().Unix()
	startTS := endTS - 24*3600

	palette := []drawing.Color{
		colorAccentBlue,
		colorAccentGreen,
		colorAccentOrange,
		colorAccentRed,
		colorPurple,
		colorCyan,
	}

	var series []seriesData
	for i, c := range containers {
		metricName := fmt.Sprintf("docker.%s.mem_pct", sanitizeForMetric(c.Name))
		points, err := g.db.QueryRange(metricName, startTS, endTS, false)
		if err != nil || len(points) == 0 {
			continue
		}
		series = append(series, seriesData{
			name:   c.Name,
			points: points,
			color:  palette[i%len(palette)],
		})
	}

	if len(series) == 0 {
		return renderEmptyChart("Docker Memory (Last 24h)")
	}
	return renderMultiLineChart("Docker Memory % (Last 24h)", "Time", "Mem %", series)
}

// WeeklyComparison returns a PNG bar chart comparing the last N weeks.
func (g *GraphBuilder) WeeklyComparison() ([]byte, error) {
	weeks := g.cfg.Weekly.WeeksComparison
	if weeks <= 0 {
		weeks = 4
	}
	summaries, err := g.db.QueryWeeklySummaries(weeks)
	if err != nil {
		return nil, fmt.Errorf("WeeklyComparison query: %w", err)
	}
	if len(summaries) == 0 {
		return renderEmptyChart("Weekly Comparison")
	}

	type weekData struct {
		label  string
		ramAvg float64
	}
	weekMap := map[int64]*weekData{}
	seen := map[int64]bool{}

	for _, s := range summaries {
		if !seen[s.WeekTS] {
			t := time.Unix(s.WeekTS, 0).UTC()
			weekMap[s.WeekTS] = &weekData{label: t.Format("Jan 02")}
			seen[s.WeekTS] = true
		}
		if s.Name == "ram.used_pct" {
			weekMap[s.WeekTS].ramAvg = s.AvgVal
		}
	}

	bars := make([]chart.Value, 0, len(weekMap))
	// Collect in a deterministic order.
	for _, s := range summaries {
		wd, ok := weekMap[s.WeekTS]
		if !ok {
			continue
		}
		delete(weekMap, s.WeekTS)
		bars = append(bars, chart.Value{
			Label: wd.label,
			Value: wd.ramAvg,
			Style: chart.Style{
				FillColor:   colorAccentBlue,
				StrokeColor: colorAccentBlue,
				StrokeWidth: 0,
			},
		})
	}

	bc := chart.BarChart{
		Title: "Weekly Avg RAM Usage",
		Background: chart.Style{
			FillColor: colorDarkBG,
			Padding:   chart.Box{Top: 40, Left: 20, Right: 20, Bottom: 20},
		},
		Canvas: chart.Style{
			FillColor: colorDarkCard,
		},
		TitleStyle: chart.Style{
			FontColor: colorText,
		},
		XAxis: chart.Style{
			FontColor:   colorText,
			StrokeColor: colorGrid,
		},
		YAxis: chart.YAxis{
			Style: chart.Style{
				FontColor:   colorText,
				StrokeColor: colorGrid,
			},
		},
		Height: 400,
		Width:  700,
		Bars:   bars,
	}

	buf := &bytes.Buffer{}
	if err := bc.Render(chart.PNG, buf); err != nil {
		return nil, fmt.Errorf("render bar chart: %w", err)
	}
	return buf.Bytes(), nil
}

type seriesData struct {
	name   string
	points []db.DataPoint
	color  drawing.Color
}

func renderLineChart(title, xLabel, yLabel string, points []db.DataPoint, lineColor drawing.Color, threshold float64) ([]byte, error) {
	if len(points) == 0 {
		return renderEmptyChart(title)
	}

	xVals := make([]float64, len(points))
	yVals := make([]float64, len(points))
	for i, p := range points {
		xVals[i] = float64(p.TS)
		yVals[i] = p.Value
	}

	fillColor := alphaColor(lineColor, 40)

	mainSeries := chart.ContinuousSeries{
		Name:    yLabel,
		XValues: xVals,
		YValues: yVals,
		Style: chart.Style{
			StrokeColor: lineColor,
			StrokeWidth: 2,
			FillColor:   fillColor,
		},
	}

	var allSeries []chart.Series
	allSeries = append(allSeries, mainSeries)

	if threshold > 0 {
		thresholdSeries := chart.ContinuousSeries{
			Name:    "Threshold",
			XValues: []float64{xVals[0], xVals[len(xVals)-1]},
			YValues: []float64{threshold, threshold},
			Style: chart.Style{
				StrokeColor:     colorAccentRed,
				StrokeWidth:     1,
				StrokeDashArray: []float64{5, 3},
			},
		}
		allSeries = append(allSeries, thresholdSeries)
	}

	graph := chart.Chart{
		Title:  title,
		Width:  900,
		Height: 350,
		Background: chart.Style{
			FillColor: colorDarkBG,
			Padding:   chart.Box{Top: 40, Left: 20, Right: 20, Bottom: 20},
		},
		Canvas: chart.Style{
			FillColor: colorDarkCard,
		},
		TitleStyle: chart.Style{
			FontColor: colorText,
		},
		XAxis: chart.XAxis{
			Name:           xLabel,
			NameStyle:      chart.Style{FontColor: colorText},
			Style:          chart.Style{FontColor: colorText, StrokeColor: colorGrid},
			ValueFormatter: chart.TimeValueFormatterWithFormat("01/02 15:04"),
		},
		YAxis: chart.YAxis{
			Name:      yLabel,
			NameStyle: chart.Style{FontColor: colorText},
			Style:     chart.Style{FontColor: colorText, StrokeColor: colorGrid},
		},
		Series: allSeries,
	}
	graph.Elements = []chart.Renderable{
		chart.Legend(&graph, chart.Style{
			FillColor:   colorDarkCard,
			FontColor:   colorText,
			StrokeColor: colorGrid,
		}),
	}

	buf := &bytes.Buffer{}
	if err := graph.Render(chart.PNG, buf); err != nil {
		return nil, fmt.Errorf("render chart: %w", err)
	}
	return buf.Bytes(), nil
}

func renderMultiLineChart(title, xLabel, yLabel string, series []seriesData) ([]byte, error) {
	if len(series) == 0 {
		return renderEmptyChart(title)
	}

	var allSeries []chart.Series
	for _, s := range series {
		if len(s.points) == 0 {
			continue
		}
		xVals := make([]float64, len(s.points))
		yVals := make([]float64, len(s.points))
		for i, p := range s.points {
			xVals[i] = float64(p.TS)
			yVals[i] = p.Value
		}
		allSeries = append(allSeries, chart.ContinuousSeries{
			Name:    s.name,
			XValues: xVals,
			YValues: yVals,
			Style: chart.Style{
				StrokeColor: s.color,
				StrokeWidth: 2,
			},
		})
	}

	if len(allSeries) == 0 {
		return renderEmptyChart(title)
	}

	graph := chart.Chart{
		Title:  title,
		Width:  900,
		Height: 350,
		Background: chart.Style{
			FillColor: colorDarkBG,
			Padding:   chart.Box{Top: 40, Left: 20, Right: 20, Bottom: 20},
		},
		Canvas: chart.Style{
			FillColor: colorDarkCard,
		},
		TitleStyle: chart.Style{
			FontColor: colorText,
		},
		XAxis: chart.XAxis{
			Name:           xLabel,
			NameStyle:      chart.Style{FontColor: colorText},
			Style:          chart.Style{FontColor: colorText, StrokeColor: colorGrid},
			ValueFormatter: chart.TimeValueFormatterWithFormat("01/02 15:04"),
		},
		YAxis: chart.YAxis{
			Name:      yLabel,
			NameStyle: chart.Style{FontColor: colorText},
			Style:     chart.Style{FontColor: colorText, StrokeColor: colorGrid},
		},
		Series: allSeries,
	}
	graph.Elements = []chart.Renderable{
		chart.Legend(&graph, chart.Style{
			FillColor:   colorDarkCard,
			FontColor:   colorText,
			StrokeColor: colorGrid,
		}),
	}

	buf := &bytes.Buffer{}
	if err := graph.Render(chart.PNG, buf); err != nil {
		return nil, fmt.Errorf("render multi-chart: %w", err)
	}
	return buf.Bytes(), nil
}

func renderEmptyChart(title string) ([]byte, error) {
	graph := chart.Chart{
		Title:  title + " (No Data)",
		Width:  900,
		Height: 350,
		Background: chart.Style{
			FillColor: colorDarkBG,
			Padding:   chart.Box{Top: 40, Left: 20, Right: 20, Bottom: 20},
		},
		Canvas: chart.Style{
			FillColor: colorDarkCard,
		},
		TitleStyle: chart.Style{
			FontColor: colorText,
		},
		Series: []chart.Series{
			chart.ContinuousSeries{
				XValues: []float64{0, 1},
				YValues: []float64{0, 0},
				Style:   chart.Style{StrokeColor: colorGrid},
			},
		},
	}

	buf := &bytes.Buffer{}
	if err := graph.Render(chart.PNG, buf); err != nil {
		return nil, fmt.Errorf("render empty chart: %w", err)
	}
	return buf.Bytes(), nil
}

func sanitizeForMetric(name string) string {
	replacer := map[rune]rune{'-': '_', '.': '_', '/': '_', ' ': '_'}
	var b []rune
	for _, c := range name {
		if r, ok := replacer[c]; ok {
			b = append(b, r)
		} else {
			b = append(b, c)
		}
	}
	return string(b)
}
