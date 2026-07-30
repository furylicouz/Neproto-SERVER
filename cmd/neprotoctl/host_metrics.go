package main

import (
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

const maxProcMetricBytes = 128 * 1024

type hostMetrics struct {
	Hostname  string
	Uptime    string
	Load      string
	Memory    string
	NetworkRX string
	NetworkTX string

	MemoryPercent  uint64
	NetworkRXBytes uint64
	NetworkTXBytes uint64
}

func collectLinuxHostMetrics() hostMetrics {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unavailable"
	}
	metrics := parseLinuxHostMetrics(
		readBoundedProcFile("/proc/uptime"),
		readBoundedProcFile("/proc/loadavg"),
		readBoundedProcFile("/proc/meminfo"),
		readBoundedProcFile("/proc/net/dev"),
	)
	metrics.Hostname = boundedDisplay(hostname, 48)
	return metrics
}

func parseLinuxHostMetrics(uptimeRaw, loadRaw, memoryRaw, networkRaw []byte) hostMetrics {
	metrics := hostMetrics{
		Uptime: "unavailable", Load: "unavailable", Memory: "unavailable",
		NetworkRX: "unavailable", NetworkTX: "unavailable",
	}
	if fields := strings.Fields(string(uptimeRaw)); len(fields) >= 1 {
		if seconds, err := strconv.ParseFloat(fields[0], 64); err == nil &&
			seconds >= 0 && seconds <= float64(100*365*24*time.Hour/time.Second) {
			duration := time.Duration(seconds * float64(time.Second))
			days := duration / (24 * time.Hour)
			hours := duration / time.Hour % 24
			minutes := duration / time.Minute % 60
			metrics.Uptime = fmt.Sprintf("%dd %02dh %02dm", days, hours, minutes)
		}
	}
	if fields := strings.Fields(string(loadRaw)); len(fields) >= 1 {
		if load, err := strconv.ParseFloat(fields[0], 64); err == nil &&
			!math.IsInf(load, 0) && !math.IsNaN(load) && load >= 0 && load <= 1_000_000 {
			metrics.Load = fmt.Sprintf("%.2f", load)
		}
	}
	total, available, memoryOK := parseMemoryKiB(memoryRaw)
	if memoryOK && total >= available {
		used := total - available
		percent := used * 100 / total
		metrics.Memory = fmt.Sprintf("%.1f/%.1f GiB %d%%", kibToGiB(used), kibToGiB(total), percent)
		metrics.MemoryPercent = percent
	}
	received, transmitted, networkOK := parseNetworkBytes(networkRaw)
	if networkOK {
		metrics.NetworkRX = formatByteCount(received)
		metrics.NetworkTX = formatByteCount(transmitted)
		metrics.NetworkRXBytes = received
		metrics.NetworkTXBytes = transmitted
	}
	return metrics
}

func readBoundedProcFile(path string) []byte {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxProcMetricBytes+1))
	if err != nil || len(raw) > maxProcMetricBytes {
		return nil
	}
	return raw
}

func parseMemoryKiB(raw []byte) (total, available uint64, ok bool) {
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[2] != "kB" {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, 0, false
		}
		switch fields[0] {
		case "MemTotal:":
			total = value
		case "MemAvailable:":
			available = value
		}
	}
	return total, available, total > 0 && available <= total
}

func parseNetworkBytes(raw []byte) (received, transmitted uint64, ok bool) {
	for _, line := range strings.Split(string(raw), "\n") {
		name, counters, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(name) == "lo" {
			continue
		}
		fields := strings.Fields(counters)
		if len(fields) != 16 {
			continue
		}
		rx, errRX := strconv.ParseUint(fields[0], 10, 64)
		tx, errTX := strconv.ParseUint(fields[8], 10, 64)
		if errRX != nil || errTX != nil || math.MaxUint64-received < rx || math.MaxUint64-transmitted < tx {
			return 0, 0, false
		}
		received += rx
		transmitted += tx
		ok = true
	}
	return received, transmitted, ok
}

func kibToGiB(value uint64) float64 { return float64(value) / (1024 * 1024) }

func formatByteCount(value uint64) string {
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
		tib = 1024 * gib
	)
	switch {
	case value >= tib:
		return fmt.Sprintf("%.1f TiB", float64(value)/tib)
	case value >= gib:
		return fmt.Sprintf("%.1f GiB", float64(value)/gib)
	case value >= mib:
		return fmt.Sprintf("%.1f MiB", float64(value)/mib)
	case value >= kib:
		return fmt.Sprintf("%.1f KiB", float64(value)/kib)
	default:
		return fmt.Sprintf("%d B", value)
	}
}

func boundedDisplay(value string, maximum int) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	runes := []rune(value)
	if len(runes) > maximum {
		value = string(runes[:maximum])
	}
	if value == "" {
		return "unavailable"
	}
	return value
}
