package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"neproto.local/chameleon/internal/covertrace"
)

func TestRunEvaluateWritesBoundedJSONAndMarkdownReports(t *testing.T) {
	directory := t.TempDir()
	input := filepath.Join(directory, "trace.jsonl")
	jsonOutput := filepath.Join(directory, "report.json")
	markdownOutput := filepath.Join(directory, "report.md")
	file, err := os.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for labelIndex, label := range []string{"fixed", "mosaic"} {
		for traceIndex := 0; traceIndex < 4; traceIndex++ {
			for packet := 0; packet < 3; packet++ {
				record := covertrace.Record{
					TraceID: label + "-" + string(rune('a'+traceIndex)), Label: label,
					RelativeTimeUS: int64(packet * (10_000 + labelIndex*80_000)),
					Direction:      covertrace.DirectionUp,
					WireBytes:      200 + labelIndex*8_000 + packet,
				}
				if err := encoder.Encode(record); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"evaluate", "--input", input, "--json", jsonOutput, "--markdown", markdownOutput,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	encoded, err := os.ReadFile(jsonOutput)
	if err != nil {
		t.Fatal(err)
	}
	var report covertrace.Report
	if err := json.Unmarshal(encoded, &report); err != nil || report.TraceCount != 8 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	markdown, err := os.ReadFile(markdownOutput)
	if err != nil || !strings.Contains(string(markdown), "Balanced accuracy") {
		t.Fatalf("markdown=%q err=%v", markdown, err)
	}
	if !strings.Contains(stdout.String(), "evaluated 8 traces") {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestRunRequiresExplicitOutputs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"evaluate", "--input", "trace.jsonl"}, &stdout, &stderr); code != 2 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}
