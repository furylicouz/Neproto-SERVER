package comparativelab

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const SchemaV1 = "np2-comparative-sample/v1"

type Sample struct {
	Schema         string    `json:"schema"`
	Timestamp      time.Time `json:"timestamp"`
	RunID          string    `json:"run_id"`
	Implementation string    `json:"implementation"`
	Profile        string    `json:"profile"`
	Transport      string    `json:"transport"`
	Network        string    `json:"network"`
	Endpoint       string    `json:"endpoint"`
	Iteration      int       `json:"iteration"`
	Success        bool      `json:"success"`
	HTTPStatus     int       `json:"http_status,omitempty"`
	Bytes          int64     `json:"bytes,omitempty"`
	ConnectMS      float64   `json:"connect_ms,omitempty"`
	TTFBMS         float64   `json:"ttfb_ms,omitempty"`
	TotalMS        float64   `json:"total_ms,omitempty"`
	ThroughputBPS  float64   `json:"throughput_bps,omitempty"`
	ErrorCategory  string    `json:"error_category,omitempty"`
}

type Report struct {
	RunIDs []string       `json:"run_ids"`
	Groups []GroupSummary `json:"groups"`
}

type GroupSummary struct {
	Implementation            string  `json:"implementation"`
	Profile                   string  `json:"profile"`
	Transport                 string  `json:"transport"`
	Network                   string  `json:"network"`
	Endpoint                  string  `json:"endpoint"`
	Total                     int     `json:"total"`
	Successful                int     `json:"successful"`
	Failed                    int     `json:"failed"`
	SuccessPercent            float64 `json:"success_percent"`
	ThroughputP50Mbps         float64 `json:"throughput_p50_mbps"`
	ThroughputP95Mbps         float64 `json:"throughput_p95_mbps"`
	ConnectP50MS              float64 `json:"connect_p50_ms"`
	ConnectP95MS              float64 `json:"connect_p95_ms"`
	TTFBP50MS                 float64 `json:"ttfb_p50_ms"`
	TTFBP95MS                 float64 `json:"ttfb_p95_ms"`
	TotalP50MS                float64 `json:"total_p50_ms"`
	TotalP95MS                float64 `json:"total_p95_ms"`
	DirectThroughputP50Mbps   float64 `json:"direct_throughput_p50_mbps,omitempty"`
	RelativeThroughputPercent float64 `json:"relative_throughput_percent,omitempty"`
}

type groupKey struct {
	implementation string
	profile        string
	transport      string
	network        string
	endpoint       string
}

type directKey struct {
	network  string
	endpoint string
}

var errInvalidSample = errors.New("invalid comparative sample")

func Summarize(samples []Sample) (Report, error) {
	if len(samples) == 0 {
		return Report{}, fmt.Errorf("%w: no samples", errInvalidSample)
	}

	grouped := make(map[groupKey][]Sample)
	runIDs := make(map[string]struct{})
	for index, sample := range samples {
		if err := validateSample(sample); err != nil {
			return Report{}, fmt.Errorf("sample %d: %w", index+1, err)
		}
		key := groupKey{
			implementation: sample.Implementation,
			profile:        sample.Profile,
			transport:      sample.Transport,
			network:        sample.Network,
			endpoint:       sample.Endpoint,
		}
		grouped[key] = append(grouped[key], sample)
		runIDs[sample.RunID] = struct{}{}
	}
	if len(runIDs) != 1 {
		return Report{}, fmt.Errorf("%w: samples must share one run identifier", errInvalidSample)
	}

	keys := make([]groupKey, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keyString(keys[i]) < keyString(keys[j]) })

	report := Report{RunIDs: sortedSet(runIDs), Groups: make([]GroupSummary, 0, len(keys))}
	direct := make(map[directKey]float64)
	for _, key := range keys {
		summary := summarizeGroup(key, grouped[key])
		report.Groups = append(report.Groups, summary)
		if key.implementation == "direct" && summary.Successful > 0 {
			direct[directKey{network: key.network, endpoint: key.endpoint}] = summary.ThroughputP50Mbps
		}
	}
	for index := range report.Groups {
		group := &report.Groups[index]
		if group.Implementation == "direct" {
			continue
		}
		baseline := direct[directKey{network: group.Network, endpoint: group.Endpoint}]
		if baseline <= 0 {
			continue
		}
		group.DirectThroughputP50Mbps = baseline
		group.RelativeThroughputPercent = group.ThroughputP50Mbps / baseline * 100
	}
	return report, nil
}

func RenderMarkdown(report Report, generatedAt time.Time) string {
	var builder strings.Builder
	builder.WriteString("# NP/2 Comparative Lab Report\n\n")
	fmt.Fprintf(&builder, "Generated: %s\n\n", generatedAt.UTC().Format(time.RFC3339))
	builder.WriteString("| Implementation | Profile | Transport | Network | Success | Throughput p50/p95 | Direct ratio | TTFB p50/p95 | Total p50/p95 |\n")
	builder.WriteString("|---|---|---|---|---:|---:|---:|---:|---:|\n")
	for _, group := range report.Groups {
		directRatio := "n/a"
		if group.Implementation != "direct" && group.DirectThroughputP50Mbps > 0 {
			directRatio = fmt.Sprintf("%.2f%%", group.RelativeThroughputPercent)
		}
		fmt.Fprintf(&builder,
			"| %s | %s | %s | %s | %.2f%% (%d/%d) | %.2f / %.2f Mbit/s | %s | %.2f / %.2f ms | %.2f / %.2f ms |\n",
			group.Implementation, group.Profile, group.Transport, group.Network,
			group.SuccessPercent, group.Successful, group.Total,
			group.ThroughputP50Mbps, group.ThroughputP95Mbps, directRatio,
			group.TTFBP50MS, group.TTFBP95MS, group.TotalP50MS, group.TotalP95MS)
	}
	builder.WriteString("\n## Scope\n\n")
	builder.WriteString("This report describes only the recorded endpoints, clients, networks, and run window. ")
	builder.WriteString("It does not establish universal DPI or GFW resistance.\n")
	return builder.String()
}

func validateSample(sample Sample) error {
	if sample.Schema != SchemaV1 {
		return fmt.Errorf("%w: unsupported schema", errInvalidSample)
	}
	if sample.Timestamp.IsZero() || sample.Iteration < 1 {
		return fmt.Errorf("%w: missing timestamp or iteration", errInvalidSample)
	}
	if !safeLabel(sample.RunID) || !safeLabel(sample.Implementation) ||
		!safeLabel(sample.Profile) || !safeLabel(sample.Transport) ||
		!safeLabel(sample.Network) || !safeLabel(sample.Endpoint) {
		return fmt.Errorf("%w: unsafe label", errInvalidSample)
	}
	if sample.Implementation != "direct" && sample.Implementation != "np2" && sample.Implementation != "vless" {
		return fmt.Errorf("%w: unsupported implementation", errInvalidSample)
	}
	if sample.Bytes < 0 || !finiteNonNegative(sample.ConnectMS) ||
		!finiteNonNegative(sample.TTFBMS) || !finiteNonNegative(sample.TotalMS) ||
		!finiteNonNegative(sample.ThroughputBPS) {
		return fmt.Errorf("%w: invalid metric", errInvalidSample)
	}
	if sample.Success {
		if sample.HTTPStatus < 200 || sample.HTTPStatus >= 400 || sample.TotalMS <= 0 {
			return fmt.Errorf("%w: successful sample is incomplete", errInvalidSample)
		}
	} else if !safeLabel(sample.ErrorCategory) {
		return fmt.Errorf("%w: failed sample needs a stable error category", errInvalidSample)
	}
	return nil
}

func safeLabel(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, current := range value {
		if current >= 'a' && current <= 'z' || current >= 'A' && current <= 'Z' ||
			current >= '0' && current <= '9' || current == '-' || current == '_' || current == '.' {
			continue
		}
		return false
	}
	return true
}

func finiteNonNegative(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func summarizeGroup(key groupKey, samples []Sample) GroupSummary {
	summary := GroupSummary{
		Implementation: key.implementation, Profile: key.profile, Transport: key.transport,
		Network: key.network, Endpoint: key.endpoint, Total: len(samples),
	}
	throughput := make([]float64, 0, len(samples))
	connect := make([]float64, 0, len(samples))
	ttfb := make([]float64, 0, len(samples))
	total := make([]float64, 0, len(samples))
	for _, sample := range samples {
		if !sample.Success {
			continue
		}
		summary.Successful++
		throughput = append(throughput, sample.ThroughputBPS/1_000_000)
		connect = append(connect, sample.ConnectMS)
		ttfb = append(ttfb, sample.TTFBMS)
		total = append(total, sample.TotalMS)
	}
	summary.Failed = summary.Total - summary.Successful
	summary.SuccessPercent = float64(summary.Successful) / float64(summary.Total) * 100
	summary.ThroughputP50Mbps, summary.ThroughputP95Mbps = percentilePair(throughput)
	summary.ConnectP50MS, summary.ConnectP95MS = percentilePair(connect)
	summary.TTFBP50MS, summary.TTFBP95MS = percentilePair(ttfb)
	summary.TotalP50MS, summary.TotalP95MS = percentilePair(total)
	return summary
}

func percentilePair(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return nearestRank(sorted, 0.50), nearestRank(sorted, 0.95)
}

func nearestRank(sorted []float64, quantile float64) float64 {
	index := int(math.Ceil(quantile*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func keyString(key groupKey) string {
	return strings.Join([]string{key.implementation, key.profile, key.transport, key.network, key.endpoint}, "\x00")
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
