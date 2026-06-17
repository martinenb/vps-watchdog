package collector

import (
	"fmt"
	"time"

	psnet "github.com/shirou/gopsutil/v3/net"
)

// NetworkCollector collects network delta metrics.
type NetworkCollector struct {
	prevSent uint64
	prevRecv uint64
	prevTS   int64
	warmedUp bool
}

// NewNetworkCollector creates a new NetworkCollector.
func NewNetworkCollector() *NetworkCollector {
	return &NetworkCollector{}
}

func (n *NetworkCollector) Name() string { return "network" }

func (n *NetworkCollector) Collect() ([]Metric, error) {
	ts := time.Now().Unix()

	counters, err := psnet.IOCounters(false) // false = aggregate all interfaces
	if err != nil {
		return nil, fmt.Errorf("network: io counters: %w", err)
	}

	var totalSent, totalRecv uint64
	for _, c := range counters {
		totalSent += c.BytesSent
		totalRecv += c.BytesRecv
	}

	if !n.warmedUp {
		n.prevSent = totalSent
		n.prevRecv = totalRecv
		n.prevTS = ts
		n.warmedUp = true
		return nil, nil
	}

	var sentDelta, recvDelta uint64
	if totalSent >= n.prevSent {
		sentDelta = totalSent - n.prevSent
	}
	if totalRecv >= n.prevRecv {
		recvDelta = totalRecv - n.prevRecv
	}

	n.prevSent = totalSent
	n.prevRecv = totalRecv
	n.prevTS = ts

	metrics := []Metric{
		{TS: ts, Category: "network", Name: "net.bytes_sent_delta", Value: float64(sentDelta)},
		{TS: ts, Category: "network", Name: "net.bytes_recv_delta", Value: float64(recvDelta)},
	}

	// Count ESTABLISHED TCP connections.
	conns, err := psnet.Connections("tcp")
	if err == nil {
		established := 0
		for _, c := range conns {
			if c.Status == "ESTABLISHED" {
				established++
			}
		}
		metrics = append(metrics, Metric{
			TS:       ts,
			Category: "network",
			Name:     "net.connections",
			Value:    float64(established),
		})
	}

	return metrics, nil
}
