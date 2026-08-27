package covertrace

import (
	"fmt"
	"hash/fnv"
	"math"
	"sort"
)

const burstGapUS = int64(50_000)

const (
	featurePacketCount = iota
	featureTotalBytes
	featureMeanSize
	featureUpByteRatio
	featureMeanGap
	featureBurstCount
	featureMeanBurstPackets
	featureCount
)

type trace struct {
	ID       string
	Label    string
	Records  []Record
	Features traceFeatures
}

type traceFeatures struct {
	Label  string
	Values [featureCount]float64
}

type Report struct {
	TraceCount     int               `json:"trace_count"`
	RecordCount    int               `json:"record_count"`
	LabelBalance   map[string]int    `json:"label_balance"`
	SizeHistogram  SizeHistogram     `json:"size_histogram"`
	Bursts         BurstSummary      `json:"bursts"`
	Delay          DelaySummary      `json:"added_delay"`
	Classifier     ClassifierSummary `json:"classifier"`
	DiversityScore float64           `json:"diversity_score"`
}

type SizeHistogram struct {
	LE128   int `json:"le_128"`
	LE512   int `json:"le_512"`
	LE1200  int `json:"le_1200"`
	LE4096  int `json:"le_4096"`
	LE16384 int `json:"le_16384"`
	LE65535 int `json:"le_65535"`
}

func (h SizeHistogram) Total() int {
	return h.LE128 + h.LE512 + h.LE1200 + h.LE4096 + h.LE16384 + h.LE65535
}

type BurstSummary struct {
	Total               int     `json:"total"`
	MeanPerTrace        float64 `json:"mean_per_trace"`
	MeanPacketsPerBurst float64 `json:"mean_packets_per_burst"`
}

type DelaySummary struct {
	Samples int   `json:"samples"`
	P50US   int64 `json:"p50_us"`
	P95US   int64 `json:"p95_us"`
	MaxUS   int64 `json:"max_us"`
}

type ClassifierSummary struct {
	TrainingTraces   int                `json:"training_traces"`
	TestTraces       int                `json:"test_traces"`
	Correct          int                `json:"correct"`
	Accuracy         float64            `json:"accuracy"`
	BalancedAccuracy float64            `json:"balanced_accuracy"`
	PerLabelRecall   map[string]float64 `json:"per_label_recall"`
}

func Analyze(records []Record) (Report, error) {
	traces, err := buildTraces(records)
	if err != nil {
		return Report{}, err
	}
	training, test, err := splitTraces(traces)
	if err != nil {
		return Report{}, err
	}
	classifier, err := classify(training, test)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		TraceCount: len(traces), RecordCount: len(records),
		LabelBalance: make(map[string]int), Classifier: classifier,
	}
	features := make([]traceFeatures, 0, len(traces))
	delays := make([]int64, 0)
	totalPackets := 0
	for _, current := range traces {
		report.LabelBalance[current.Label]++
		features = append(features, current.Features)
		bursts := int(current.Features.Values[featureBurstCount])
		report.Bursts.Total += bursts
		totalPackets += len(current.Records)
		for _, record := range current.Records {
			report.SizeHistogram.add(record.WireBytes)
			if record.AddedDelayUS != nil {
				delays = append(delays, *record.AddedDelayUS)
			}
		}
	}
	report.Bursts.MeanPerTrace = float64(report.Bursts.Total) / float64(len(traces))
	if report.Bursts.Total > 0 {
		report.Bursts.MeanPacketsPerBurst = float64(totalPackets) / float64(report.Bursts.Total)
	}
	report.Delay = summarizeDelays(delays)
	report.DiversityScore = diversityScore(features)
	return report, nil
}

func buildTraces(records []Record) ([]*trace, error) {
	if len(records) == 0 || len(records) > MaximumRecords {
		return nil, fmt.Errorf("%w: record count outside bounds", ErrInvalidTrace)
	}
	grouped := make(map[string]*trace)
	validations := make(map[string]traceValidation)
	for index, record := range records {
		state, exists := validations[record.TraceID]
		if !exists && len(grouped) >= MaximumTraces {
			return nil, fmt.Errorf("%w: trace limit exceeded", ErrInvalidTrace)
		}
		if err := validateRecord(record, state, exists); err != nil {
			return nil, fmt.Errorf("%w: record %d: %v", ErrInvalidTrace, index+1, err)
		}
		state.label = record.Label
		state.lastTimeUS = record.RelativeTimeUS
		state.records++
		state.initialized = true
		validations[record.TraceID] = state
		current := grouped[record.TraceID]
		if current == nil {
			current = &trace{ID: record.TraceID, Label: record.Label}
			grouped[record.TraceID] = current
		}
		current.Records = append(current.Records, record)
	}
	result := make([]*trace, 0, len(grouped))
	for _, current := range grouped {
		current.Features = extractFeatures(current)
		result = append(result, current)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func extractFeatures(current *trace) traceFeatures {
	features := traceFeatures{Label: current.Label}
	count := len(current.Records)
	features.Values[featurePacketCount] = float64(count)
	if count == 0 {
		return features
	}
	totalBytes := 0
	upBytes := 0
	burstCount := 1
	var totalGap int64
	for index, record := range current.Records {
		totalBytes += record.WireBytes
		if record.Direction == DirectionUp {
			upBytes += record.WireBytes
		}
		if index > 0 {
			gap := record.RelativeTimeUS - current.Records[index-1].RelativeTimeUS
			totalGap += gap
			if gap >= burstGapUS {
				burstCount++
			}
		}
	}
	features.Values[featureTotalBytes] = float64(totalBytes)
	features.Values[featureMeanSize] = float64(totalBytes) / float64(count)
	features.Values[featureUpByteRatio] = float64(upBytes) / float64(totalBytes)
	if count > 1 {
		features.Values[featureMeanGap] = float64(totalGap) / float64(count-1)
	}
	features.Values[featureBurstCount] = float64(burstCount)
	features.Values[featureMeanBurstPackets] = float64(count) / float64(burstCount)
	return features
}

func splitTraces(traces []*trace) ([]*trace, []*trace, error) {
	byLabel := make(map[string][]*trace)
	for _, current := range traces {
		byLabel[current.Label] = append(byLabel[current.Label], current)
	}
	if len(byLabel) < 2 {
		return nil, nil, fmt.Errorf("%w: classifier requires at least two labels", ErrInvalidTrace)
	}
	labels := sortedLabels(byLabel)
	training := make([]*trace, 0, len(traces))
	test := make([]*trace, 0, len(traces)/3+len(labels))
	for _, label := range labels {
		group := byLabel[label]
		if len(group) < 2 {
			return nil, nil, fmt.Errorf("%w: label %q needs at least two traces", ErrInvalidTrace, label)
		}
		sort.Slice(group, func(i, j int) bool {
			left, right := traceHash(group[i].ID), traceHash(group[j].ID)
			if left == right {
				return group[i].ID < group[j].ID
			}
			return left < right
		})
		testCount := max(1, len(group)/3)
		test = append(test, group[:testCount]...)
		training = append(training, group[testCount:]...)
	}
	sort.Slice(training, func(i, j int) bool { return training[i].ID < training[j].ID })
	sort.Slice(test, func(i, j int) bool { return test[i].ID < test[j].ID })
	return training, test, nil
}

func classify(training, test []*trace) (ClassifierSummary, error) {
	if len(training) == 0 || len(test) == 0 {
		return ClassifierSummary{}, fmt.Errorf("%w: empty classifier split", ErrInvalidTrace)
	}
	means, scales := featureScale(training)
	centroids := make(map[string][featureCount]float64)
	counts := make(map[string]int)
	for _, current := range training {
		centroid := centroids[current.Label]
		for feature := 0; feature < featureCount; feature++ {
			centroid[feature] += (current.Features.Values[feature] - means[feature]) / scales[feature]
		}
		centroids[current.Label] = centroid
		counts[current.Label]++
	}
	for label, centroid := range centroids {
		for feature := range centroid {
			centroid[feature] /= float64(counts[label])
		}
		centroids[label] = centroid
	}
	labels := sortedCentroidLabels(centroids)
	correctByLabel := make(map[string]int)
	totalByLabel := make(map[string]int)
	correct := 0
	for _, current := range test {
		predicted := nearestLabel(current.Features.Values, means, scales, labels, centroids)
		totalByLabel[current.Label]++
		if predicted == current.Label {
			correct++
			correctByLabel[current.Label]++
		}
	}
	result := ClassifierSummary{
		TrainingTraces: len(training), TestTraces: len(test), Correct: correct,
		Accuracy: float64(correct) / float64(len(test)), PerLabelRecall: make(map[string]float64),
	}
	for _, label := range labels {
		total := totalByLabel[label]
		if total == 0 {
			return ClassifierSummary{}, fmt.Errorf("%w: label %q missing from test split", ErrInvalidTrace, label)
		}
		recall := float64(correctByLabel[label]) / float64(total)
		result.PerLabelRecall[label] = recall
		result.BalancedAccuracy += recall
	}
	result.BalancedAccuracy /= float64(len(labels))
	return result, nil
}

func featureScale(training []*trace) ([featureCount]float64, [featureCount]float64) {
	var means [featureCount]float64
	var scales [featureCount]float64
	for _, current := range training {
		for feature, value := range current.Features.Values {
			means[feature] += value
		}
	}
	for feature := range means {
		means[feature] /= float64(len(training))
	}
	for _, current := range training {
		for feature, value := range current.Features.Values {
			delta := value - means[feature]
			scales[feature] += delta * delta
		}
	}
	for feature := range scales {
		scales[feature] = math.Sqrt(scales[feature] / float64(len(training)))
		if scales[feature] == 0 {
			scales[feature] = 1
		}
	}
	return means, scales
}

func nearestLabel(values, means, scales [featureCount]float64, labels []string, centroids map[string][featureCount]float64) string {
	bestLabel := ""
	bestDistance := math.Inf(1)
	for _, label := range labels {
		centroid := centroids[label]
		distance := 0.0
		for feature, value := range values {
			normalized := (value - means[feature]) / scales[feature]
			delta := normalized - centroid[feature]
			distance += delta * delta
		}
		if distance < bestDistance {
			bestDistance = distance
			bestLabel = label
		}
	}
	return bestLabel
}

func diversityScore(features []traceFeatures) float64 {
	if len(features) < 2 {
		return 0
	}
	var minimums, maximums [featureCount]float64
	for feature := 0; feature < featureCount; feature++ {
		minimums[feature] = math.Inf(1)
		maximums[feature] = math.Inf(-1)
	}
	for _, current := range features {
		for feature, value := range current.Values {
			minimums[feature] = min(minimums[feature], value)
			maximums[feature] = max(maximums[feature], value)
		}
	}
	total := 0.0
	pairs := 0
	for left := 0; left < len(features); left++ {
		for right := left + 1; right < len(features); right++ {
			if features[left].Label != features[right].Label {
				continue
			}
			distance := 0.0
			active := 0
			for feature := 0; feature < featureCount; feature++ {
				rangeWidth := maximums[feature] - minimums[feature]
				if rangeWidth <= 0 {
					continue
				}
				distance += math.Abs(features[left].Values[feature]-features[right].Values[feature]) / rangeWidth
				active++
			}
			if active > 0 {
				total += distance / float64(active)
			}
			pairs++
		}
	}
	if pairs == 0 {
		return 0
	}
	return min(1, total/float64(pairs))
}

func (h *SizeHistogram) add(size int) {
	switch {
	case size <= 128:
		h.LE128++
	case size <= 512:
		h.LE512++
	case size <= 1200:
		h.LE1200++
	case size <= 4096:
		h.LE4096++
	case size <= 16_384:
		h.LE16384++
	default:
		h.LE65535++
	}
}

func summarizeDelays(delays []int64) DelaySummary {
	if len(delays) == 0 {
		return DelaySummary{}
	}
	sorted := append([]int64(nil), delays...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return DelaySummary{
		Samples: len(sorted), P50US: nearestRank(sorted, 0.50),
		P95US: nearestRank(sorted, 0.95), MaxUS: sorted[len(sorted)-1],
	}
}

func nearestRank(sorted []int64, quantile float64) int64 {
	index := int(math.Ceil(quantile*float64(len(sorted)))) - 1
	index = max(0, min(index, len(sorted)-1))
	return sorted[index]
}

func traceHash(value string) uint64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(value))
	return hasher.Sum64()
}

func sortedLabels(groups map[string][]*trace) []string {
	labels := make([]string, 0, len(groups))
	for label := range groups {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return labels
}

func sortedCentroidLabels(groups map[string][featureCount]float64) []string {
	labels := make([]string, 0, len(groups))
	for label := range groups {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return labels
}
