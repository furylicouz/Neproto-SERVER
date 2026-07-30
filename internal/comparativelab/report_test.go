package comparativelab

import (
	"strings"
	"testing"
	"time"
)

func TestSummarizeMatchesDirectBaselineAndIgnoresFailuresInPercentiles(t *testing.T) {
	started := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	samples := []Sample{
		sample(started, "direct", "baseline", "direct", 1, true, 100_000_000, 100),
		sample(started, "direct", "baseline", "direct", 2, true, 120_000_000, 90),
		sample(started, "np2", "constellation", "https", 1, true, 80_000_000, 130),
		sample(started, "np2", "constellation", "https", 2, true, 100_000_000, 110),
		{
			Schema: SchemaV1, Timestamp: started, RunID: "run-1",
			Implementation: "np2", Profile: "constellation", Transport: "https",
			Network: "mac-wifi", Endpoint: "vps-50mb", Iteration: 3,
			Success: false, ErrorCategory: "timeout",
		},
	}

	report, err := Summarize(samples)
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if len(report.Groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(report.Groups))
	}

	var np2 GroupSummary
	for _, group := range report.Groups {
		if group.Implementation == "np2" {
			np2 = group
		}
	}
	if np2.Total != 3 || np2.Successful != 2 || np2.Failed != 1 {
		t.Fatalf("NP/2 counts = total %d successful %d failed %d", np2.Total, np2.Successful, np2.Failed)
	}
	if np2.ThroughputP50Mbps != 80 || np2.ThroughputP95Mbps != 100 {
		t.Fatalf("NP/2 throughput p50/p95 = %.1f/%.1f", np2.ThroughputP50Mbps, np2.ThroughputP95Mbps)
	}
	if np2.DirectThroughputP50Mbps != 100 || np2.RelativeThroughputPercent != 80 {
		t.Fatalf("direct/relative = %.1f/%.1f", np2.DirectThroughputP50Mbps, np2.RelativeThroughputPercent)
	}
}

func TestSummarizeRejectsSecretBearingOrInvalidLabels(t *testing.T) {
	s := sample(time.Now().UTC(), "np2", "https://host/private-token", "https", 1, true, 1, 1)
	if _, err := Summarize([]Sample{s}); err == nil {
		t.Fatal("Summarize() accepted a URL-like profile label")
	}
}

func TestSummarizeRejectsMixedRunIdentifiers(t *testing.T) {
	started := time.Now().UTC()
	first := sample(started, "direct", "baseline", "direct", 1, true, 1, 1)
	second := sample(started, "np2", "constellation", "https", 1, true, 1, 1)
	second.RunID = "run-2"
	if _, err := Summarize([]Sample{first, second}); err == nil {
		t.Fatal("Summarize() accepted samples from different experiments")
	}
}

func TestRenderMarkdownStatesScopeAndFailureRate(t *testing.T) {
	started := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	report, err := Summarize([]Sample{
		sample(started, "direct", "baseline", "direct", 1, true, 100_000_000, 100),
		sample(started, "np2", "constellation", "https", 1, true, 75_000_000, 130),
	})
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}

	markdown := RenderMarkdown(report, started)
	for _, expected := range []string{
		"NP/2 Comparative Lab Report",
		"100.00%",
		"75.00%",
		"does not establish universal DPI or GFW resistance",
	} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("markdown does not contain %q:\n%s", expected, markdown)
		}
	}
}

func sample(started time.Time, implementation, profile, transport string, iteration int, success bool, throughput float64, totalMS float64) Sample {
	return Sample{
		Schema: SchemaV1, Timestamp: started, RunID: "run-1",
		Implementation: implementation, Profile: profile, Transport: transport,
		Network: "mac-wifi", Endpoint: "vps-50mb", Iteration: iteration,
		Success: success, HTTPStatus: 200, Bytes: 50_000_000,
		ConnectMS: 10, TTFBMS: 20, TotalMS: totalMS, ThroughputBPS: throughput,
	}
}
