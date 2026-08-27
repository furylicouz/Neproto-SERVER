package covertrace

import (
	"fmt"
	"sort"
	"strings"
)

func RenderMarkdown(report Report) string {
	var builder strings.Builder
	builder.WriteString("# NP/2 Mosaic Metadata Trace Report\n\n")
	builder.WriteString("## Dataset\n\n")
	fmt.Fprintf(&builder, "- Traces: %d\n- Records: %d\n", report.TraceCount, report.RecordCount)
	builder.WriteString("\n| Label | Traces |\n|---|---:|\n")
	labels := make([]string, 0, len(report.LabelBalance))
	for label := range report.LabelBalance {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		fmt.Fprintf(&builder, "| %s | %d |\n", label, report.LabelBalance[label])
	}

	builder.WriteString("\n## Regression metrics\n\n")
	fmt.Fprintf(&builder,
		"- Classifier accuracy: %.2f%% (%d/%d test traces)\n- Balanced accuracy: %.2f%%\n- Diversity score: %.4f\n",
		report.Classifier.Accuracy*100, report.Classifier.Correct, report.Classifier.TestTraces,
		report.Classifier.BalancedAccuracy*100, report.DiversityScore)
	fmt.Fprintf(&builder,
		"- Burst count: %d (mean %.2f per trace, %.2f packets per burst)\n",
		report.Bursts.Total, report.Bursts.MeanPerTrace, report.Bursts.MeanPacketsPerBurst)
	if report.Delay.Samples > 0 {
		fmt.Fprintf(&builder, "- Added delay: p50 %d us, p95 %d us, max %d us (%d samples)\n",
			report.Delay.P50US, report.Delay.P95US, report.Delay.MaxUS, report.Delay.Samples)
	} else {
		builder.WriteString("- Added delay: not supplied\n")
	}

	builder.WriteString("\n## Size histogram\n\n")
	builder.WriteString("| Wire size bucket | Records |\n|---|---:|\n")
	fmt.Fprintf(&builder,
		"| <=128 | %d |\n| <=512 | %d |\n| <=1200 | %d |\n| <=4096 | %d |\n| <=16384 | %d |\n| <=65535 | %d |\n",
		report.SizeHistogram.LE128, report.SizeHistogram.LE512, report.SizeHistogram.LE1200,
		report.SizeHistogram.LE4096, report.SizeHistogram.LE16384, report.SizeHistogram.LE65535)

	builder.WriteString("\n## Interpretation boundary\n\n")
	builder.WriteString("This deterministic classifier is a regression instrument over the supplied metadata-only traces. ")
	builder.WriteString("It is not proof of invisibility or resistance to a network operator or global observer.\n")
	return builder.String()
}
