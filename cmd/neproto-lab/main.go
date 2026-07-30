package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"neproto.local/chameleon/internal/comparativelab"
)

const maximumSampleLine = 1 << 20

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	switch args[0] {
	case "measure":
		return runMeasure(args[1:], stdout, stderr)
	case "summarize":
		return runSummarize(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintln(stderr, "unknown command; use measure or summarize")
		return 2
	}
}

func runMeasure(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("measure", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var config comparativelab.MeasureConfig
	var output string
	flags.StringVar(&config.RunID, "run-id", "", "safe experiment identifier")
	flags.StringVar(&config.Implementation, "implementation", "", "direct, np2, or vless")
	flags.StringVar(&config.Profile, "profile", "", "safe versioned profile label")
	flags.StringVar(&config.Transport, "transport", "", "transport label")
	flags.StringVar(&config.Network, "network", "", "coarse network label")
	flags.StringVar(&config.Endpoint, "endpoint", "", "non-secret endpoint label")
	flags.StringVar(&config.URL, "url", "", "HTTPS benchmark object")
	flags.StringVar(&config.ProxyURL, "proxy", "", "optional loopback socks5 URL")
	flags.IntVar(&config.Runs, "runs", 20, "number of sequential requests")
	flags.Int64Var(&config.ExpectedBytes, "expected-bytes", 0, "exact expected response size")
	flags.DurationVar(&config.Timeout, "timeout", 90*time.Second, "per-request deadline")
	flags.BoolVar(&config.Warm, "warm", false, "reuse destination HTTP connections")
	flags.StringVar(&config.AddressFamily, "ip-version", "4", "target address family: 4, 6, or auto")
	flags.StringVar(&config.TargetAddress, "target-address", "", "optional fixed benchmark target IP; never written to samples")
	flags.StringVar(&output, "output", "", "JSONL sample file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "measure does not accept positional arguments")
		return 2
	}
	missing := missingMeasureFlags(config, output)
	if len(missing) > 0 {
		fmt.Fprintf(stderr, "missing required flags: %s\n", strings.Join(missing, ", "))
		return 2
	}

	samples, err := comparativelab.Measure(context.Background(), config)
	if err != nil {
		fmt.Fprintf(stderr, "measurement setup failed: %v\n", err)
		return 1
	}
	if err := appendSamples(output, samples); err != nil {
		fmt.Fprintf(stderr, "write samples: %v\n", err)
		return 1
	}
	successful := 0
	for _, sample := range samples {
		if sample.Success {
			successful++
		}
	}
	fmt.Fprintf(stdout, "recorded %d samples (%d successful) in %s\n", len(samples), successful, output)
	if successful != len(samples) {
		return 3
	}
	return 0
}

func runSummarize(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("summarize", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var input, jsonOutput, markdownOutput string
	flags.StringVar(&input, "input", "", "JSONL sample file")
	flags.StringVar(&jsonOutput, "json", "", "summary JSON output")
	flags.StringVar(&markdownOutput, "markdown", "", "summary Markdown output")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if input == "" || jsonOutput == "" || markdownOutput == "" {
		fmt.Fprintln(stderr, "summarize requires --input, --json, and --markdown")
		return 2
	}
	samples, err := readSamples(input)
	if err != nil {
		fmt.Fprintf(stderr, "read samples: %v\n", err)
		return 1
	}
	report, err := comparativelab.Summarize(samples)
	if err != nil {
		fmt.Fprintf(stderr, "summarize samples: %v\n", err)
		return 1
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "encode summary: %v\n", err)
		return 1
	}
	encoded = append(encoded, '\n')
	if err := writePrivateFile(jsonOutput, encoded); err != nil {
		fmt.Fprintf(stderr, "write JSON report: %v\n", err)
		return 1
	}
	markdown := comparativelab.RenderMarkdown(report, time.Now())
	if err := writePrivateFile(markdownOutput, []byte(markdown)); err != nil {
		fmt.Fprintf(stderr, "write Markdown report: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "summarized %d samples into %d groups\n", len(samples), len(report.Groups))
	return 0
}

func missingMeasureFlags(config comparativelab.MeasureConfig, output string) []string {
	checks := []struct {
		name  string
		value string
	}{
		{"--run-id", config.RunID}, {"--implementation", config.Implementation},
		{"--profile", config.Profile}, {"--transport", config.Transport},
		{"--network", config.Network}, {"--endpoint", config.Endpoint},
		{"--url", config.URL}, {"--output", output},
	}
	missing := make([]string, 0)
	for _, check := range checks {
		if check.value == "" {
			missing = append(missing, check.name)
		}
	}
	return missing
}

func appendSamples(path string, samples []comparativelab.Sample) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	for _, sample := range samples {
		if err := encoder.Encode(sample); err != nil {
			return err
		}
	}
	return file.Sync()
}

func readSamples(path string) ([]comparativelab.Sample, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maximumSampleLine)
	samples := make([]comparativelab.Sample, 0, 64)
	for line := 1; scanner.Scan(); line++ {
		data := scanner.Bytes()
		if len(strings.TrimSpace(string(data))) == 0 {
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		var sample comparativelab.Sample
		if err := decoder.Decode(&sample); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			return nil, fmt.Errorf("line %d: trailing JSON data", line)
		}
		samples = append(samples, sample)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		return nil, errors.New("sample file is empty")
	}
	return samples, nil
}

func writePrivateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func printUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: neproto-lab measure [flags] | summarize [flags]")
}
