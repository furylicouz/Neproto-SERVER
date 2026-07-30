package main

import (
	"strings"
	"testing"
)

func TestParseLinuxHostMetricsProducesBoundedDashboardValues(t *testing.T) {
	metrics := parseLinuxHostMetrics(
		[]byte("90061.50 1.00\n"),
		[]byte("0.42 0.31 0.20 2/123 42\n"),
		[]byte("MemTotal:       8388608 kB\nMemAvailable:   2097152 kB\n"),
		[]byte("Inter-| Receive | Transmit\n eth0: 10485760 1 0 0 0 0 0 0 5242880 1 0 0 0 0 0 0\n lo: 999 1 0 0 0 0 0 0 999 1 0 0 0 0 0 0\n"),
	)

	if metrics.Uptime != "1d 01h 01m" || metrics.Load != "0.42" ||
		metrics.Memory != "6.0/8.0 GiB 75%" || metrics.NetworkRX != "10.0 MiB" ||
		metrics.NetworkTX != "5.0 MiB" {
		t.Fatalf("metrics=%+v", metrics)
	}
}

func TestParseLinuxHostMetricsRejectsMalformedAndUnboundedInput(t *testing.T) {
	metrics := parseLinuxHostMetrics(
		[]byte("not-uptime"), []byte(strings.Repeat("9", 100)),
		[]byte("MemTotal: 1 kB\nMemAvailable: 2 kB\n"),
		[]byte("eth0: invalid\n"),
	)
	if metrics.Uptime != "unavailable" || metrics.Load != "unavailable" ||
		metrics.Memory != "unavailable" || metrics.NetworkRX != "unavailable" ||
		metrics.NetworkTX != "unavailable" {
		t.Fatalf("malformed metrics=%+v", metrics)
	}
}
