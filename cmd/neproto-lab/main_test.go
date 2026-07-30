package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neproto.local/chameleon/internal/comparativelab"
)

func TestRunSummarizeWritesJSONAndMarkdown(t *testing.T) {
	directory := t.TempDir()
	input := filepath.Join(directory, "samples.jsonl")
	jsonOutput := filepath.Join(directory, "report.json")
	markdownOutput := filepath.Join(directory, "report.md")
	sample := comparativelab.Sample{
		Schema: comparativelab.SchemaV1, Timestamp: time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC),
		RunID: "run-1", Implementation: "direct", Profile: "baseline", Transport: "direct",
		Network: "local", Endpoint: "fixture", Iteration: 1, Success: true,
		HTTPStatus: 200, Bytes: 1024, TTFBMS: 1, TotalMS: 2, ThroughputBPS: 4_096_000,
	}
	encoded, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"summarize", "--input", input, "--json", jsonOutput, "--markdown", markdownOutput}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, stderr = %s", code, stderr.String())
	}
	for _, path := range []string{jsonOutput, markdownOutput} {
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			t.Fatalf("output %s: info=%v err=%v", path, info, err)
		}
	}
	markdown, err := os.ReadFile(markdownOutput)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markdown), "NP/2 Comparative Lab Report") {
		t.Fatalf("unexpected markdown: %s", markdown)
	}
}

func TestRunMeasureRequiresExplicitHTTPSURL(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"measure", "--implementation", "direct", "--profile", "baseline",
		"--transport", "direct", "--network", "local", "--endpoint", "fixture",
		"--run-id", "run-1", "--output", filepath.Join(t.TempDir(), "samples.jsonl"),
	}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--url") {
		t.Fatalf("run() = %d, stderr = %s", code, stderr.String())
	}
}
