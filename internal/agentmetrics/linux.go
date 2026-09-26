package agentmetrics

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

type Snapshot struct {
	CPUPct          float64
	MemoryUsedBytes int64
	RXBytes         int64
	TXBytes         int64
}

type Collector struct {
	mu            sync.Mutex
	readFile      func(string) ([]byte, error)
	previousTotal uint64
	previousIdle  uint64
}

func NewCollector(readFile func(string) ([]byte, error)) *Collector {
	if readFile == nil {
		readFile = os.ReadFile
	}
	return &Collector{readFile: readFile}
}

func (c *Collector) Sample() (Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	stat, err := c.readFile("/proc/stat")
	if err != nil {
		return Snapshot{}, err
	}
	line, _, _ := bytes.Cut(stat, []byte("\n"))
	fields := strings.Fields(string(line))
	if len(fields) < 5 || fields[0] != "cpu" {
		return Snapshot{}, errors.New("invalid /proc/stat CPU line")
	}
	var total, idle uint64
	for index, value := range fields[1:] {
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return Snapshot{}, fmt.Errorf("invalid CPU counter: %w", err)
		}
		total += parsed
		if index == 3 || index == 4 {
			idle += parsed
		}
	}
	mem, err := c.readFile("/proc/meminfo")
	if err != nil {
		return Snapshot{}, err
	}
	var memoryTotal, memoryAvailable int64 = -1, -1
	for _, line := range strings.Split(string(mem), "\n") {
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		if parts[0] != "MemTotal:" && parts[0] != "MemAvailable:" {
			continue
		}
		value, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || parts[2] != "kB" || value < 0 {
			return Snapshot{}, errors.New("invalid memory counter")
		}
		if parts[0] == "MemTotal:" {
			memoryTotal = value
		} else {
			memoryAvailable = value
		}
	}
	if memoryTotal < 0 || memoryAvailable < 0 || memoryAvailable > memoryTotal {
		return Snapshot{}, errors.New("missing or invalid memory counters")
	}
	netdev, err := c.readFile("/proc/net/dev")
	if err != nil {
		return Snapshot{}, err
	}
	var rx, tx int64
	validInterfaces := 0
	for _, line := range strings.Split(string(netdev), "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) == "lo" {
			continue
		}
		parts := strings.Fields(rest)
		if len(parts) < 16 {
			return Snapshot{}, errors.New("invalid network counters")
		}
		received, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return Snapshot{}, err
		}
		sent, err := strconv.ParseInt(parts[8], 10, 64)
		if err != nil || received < 0 || sent < 0 {
			return Snapshot{}, errors.New("invalid network bytes")
		}
		rx += received
		tx += sent
		validInterfaces++
	}
	if validInterfaces == 0 {
		return Snapshot{}, errors.New("no network interfaces")
	}
	result := Snapshot{MemoryUsedBytes: (memoryTotal - memoryAvailable) * 1024, RXBytes: rx, TXBytes: tx}
	if c.previousTotal > 0 && total > c.previousTotal && idle >= c.previousIdle {
		busy := (total - c.previousTotal) - (idle - c.previousIdle)
		result.CPUPct = float64(busy) * 100 / float64(total-c.previousTotal)
	}
	c.previousTotal = total
	c.previousIdle = idle
	return result, nil
}
