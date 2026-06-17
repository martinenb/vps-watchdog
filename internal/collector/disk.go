package collector

import (
	"crypto/md5"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
)

// DiskCollector collects disk usage metrics and (in background) top directory sizes.
type DiskCollector struct {
	walkPaths []string
	maxDepth  int
	topDirsN  int

	mu        sync.RWMutex
	dirCache  []DirEntry // result of the last background walk
	walkRunAt time.Time
}

// DirEntry holds a directory path and its total size.
type DirEntry struct {
	Path  string
	Bytes int64
}

// NewDiskCollector creates a new DiskCollector.
func NewDiskCollector(walkPaths []string, maxDepth, topDirsN int) *DiskCollector {
	d := &DiskCollector{
		walkPaths: walkPaths,
		maxDepth:  maxDepth,
		topDirsN:  topDirsN,
	}
	// Kick off the first background walk immediately.
	go d.backgroundWalk()
	return d
}

func (d *DiskCollector) Name() string { return "disk" }

func (d *DiskCollector) Collect() ([]Metric, error) {
	ts := time.Now().Unix()
	var metrics []Metric

	partitions, err := disk.Partitions(false)
	if err != nil {
		return nil, fmt.Errorf("disk: partitions: %w", err)
	}

	seen := map[string]struct{}{}
	for _, p := range partitions {
		if _, ok := seen[p.Mountpoint]; ok {
			continue
		}
		seen[p.Mountpoint] = struct{}{}

		usage, err := disk.Usage(p.Mountpoint)
		if err != nil {
			log.Printf("disk: usage %s: %v", p.Mountpoint, err)
			continue
		}

		label := mountLabel(p.Mountpoint)
		metrics = append(metrics,
			Metric{TS: ts, Category: "disk", Name: "disk." + label + ".used_pct", Value: usage.UsedPercent},
			Metric{TS: ts, Category: "disk", Name: "disk." + label + ".free_bytes", Value: float64(usage.Free)},
			Metric{TS: ts, Category: "disk", Name: "disk." + label + ".total_bytes", Value: float64(usage.Total)},
		)
	}

	// Emit cached directory metrics.
	d.mu.RLock()
	dirs := d.dirCache
	d.mu.RUnlock()

	for _, de := range dirs {
		hash := fmt.Sprintf("%x", md5.Sum([]byte(de.Path)))[:8]
		metrics = append(metrics, Metric{
			TS:       ts,
			Category: "disk",
			Name:     "disk.dir." + hash + ".bytes",
			Value:    float64(de.Bytes),
			Tags:     map[string]string{"path": de.Path},
		})
	}

	// Refresh background walk every hour.
	d.mu.RLock()
	lastWalk := d.walkRunAt
	d.mu.RUnlock()
	if time.Since(lastWalk) > time.Hour {
		go d.backgroundWalk()
	}

	return metrics, nil
}

func (d *DiskCollector) backgroundWalk() {
	d.mu.Lock()
	d.walkRunAt = time.Now()
	d.mu.Unlock()

	type dirSize struct {
		path  string
		bytes int64
	}

	sizes := map[string]int64{}

	for _, root := range d.walkPaths {
		rootDepth := strings.Count(filepath.Clean(root), string(os.PathSeparator))

		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable
			}
			depth := strings.Count(filepath.Clean(path), string(os.PathSeparator)) - rootDepth
			if entry.IsDir() {
				if depth > d.maxDepth {
					return filepath.SkipDir
				}
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return nil
			}
			// Attribute file size to all ancestor directories up to maxDepth.
			dir := filepath.Dir(path)
			for {
				d2 := strings.Count(filepath.Clean(dir), string(os.PathSeparator)) - rootDepth
				if d2 < 0 {
					break
				}
				sizes[dir] += info.Size()
				if dir == root || dir == "/" || dir == "." {
					break
				}
				dir = filepath.Dir(dir)
			}
			return nil
		})
		if err != nil {
			log.Printf("disk: walk %s: %v", root, err)
		}
	}

	entries := make([]DirEntry, 0, len(sizes))
	for p, b := range sizes {
		entries = append(entries, DirEntry{Path: p, Bytes: b})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Bytes > entries[j].Bytes })

	limit := d.topDirsN
	if limit > len(entries) {
		limit = len(entries)
	}
	entries = entries[:limit]

	d.mu.Lock()
	d.dirCache = entries
	d.mu.Unlock()
}

// GetTopDirs returns the cached top directories by size.
func (d *DiskCollector) GetTopDirs() []DirEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]DirEntry, len(d.dirCache))
	copy(result, d.dirCache)
	return result
}

// mountLabel converts a mount point to a safe metric label.
func mountLabel(mount string) string {
	if mount == "/" {
		return "root"
	}
	label := strings.TrimPrefix(mount, "/")
	label = strings.ReplaceAll(label, "/", "_")
	return label
}
