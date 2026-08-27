package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"neproto.local/chameleon/internal/covertrace"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	switch args[0] {
	case "evaluate":
		return runEvaluate(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintln(stderr, "unknown command; use evaluate")
		return 2
	}
}

func runEvaluate(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("evaluate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var input, jsonOutput, markdownOutput string
	flags.StringVar(&input, "input", "", "bounded metadata-only JSONL trace")
	flags.StringVar(&jsonOutput, "json", "", "summary JSON output")
	flags.StringVar(&markdownOutput, "markdown", "", "summary Markdown output")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || input == "" || jsonOutput == "" || markdownOutput == "" {
		fmt.Fprintln(stderr, "evaluate requires --input, --json, and --markdown")
		return 2
	}
	file, err := os.Open(input)
	if err != nil {
		fmt.Fprintf(stderr, "open trace: %v\n", err)
		return 1
	}
	records, readErr := covertrace.ReadJSONL(file)
	closeErr := file.Close()
	if readErr != nil {
		fmt.Fprintf(stderr, "read trace: %v\n", readErr)
		return 1
	}
	if closeErr != nil {
		fmt.Fprintf(stderr, "close trace: %v\n", closeErr)
		return 1
	}
	report, err := covertrace.Analyze(records)
	if err != nil {
		fmt.Fprintf(stderr, "evaluate trace: %v\n", err)
		return 1
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "encode report: %v\n", err)
		return 1
	}
	if err := writePrivateFile(jsonOutput, append(encoded, '\n')); err != nil {
		fmt.Fprintf(stderr, "write JSON report: %v\n", err)
		return 1
	}
	if err := writePrivateFile(markdownOutput, []byte(covertrace.RenderMarkdown(report))); err != nil {
		fmt.Fprintf(stderr, "write Markdown report: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "evaluated %d traces and %d records\n", report.TraceCount, report.RecordCount)
	return 0
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
	fmt.Fprintln(output, "Usage: neproto-coverlab evaluate --input trace.jsonl --json report.json --markdown report.md")
}
