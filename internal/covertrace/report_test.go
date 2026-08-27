package covertrace

import (
	"strings"
	"testing"
)

func TestRenderMarkdownSummarizesMetricsWithoutTraceIDs(t *testing.T) {
	report, err := Analyze(knownDataset())
	if err != nil {
		t.Fatal(err)
	}
	markdown := RenderMarkdown(report)
	for _, required := range []string{
		"NP/2 Mosaic Metadata Trace Report",
		"Balanced accuracy",
		"Diversity score",
		"Size histogram",
	} {
		if !strings.Contains(markdown, required) {
			t.Fatalf("markdown missing %q:\n%s", required, markdown)
		}
	}
	if strings.Contains(markdown, "fixed-a") || strings.Contains(markdown, "mosaic-a") {
		t.Fatalf("markdown leaked trace ID:\n%s", markdown)
	}
}
