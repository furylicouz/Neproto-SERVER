package covertrace

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestReadJSONLAcceptsMetadataOnlyAndRejectsInvalidInput(t *testing.T) {
	valid := strings.Join([]string{
		`{"trace_id":"web-1","label":"fixed","relative_time_us":0,"direction":"up","wire_bytes":512}`,
		`{"trace_id":"web-1","label":"fixed","relative_time_us":2500,"direction":"down","wire_bytes":1200,"added_delay_us":750}`,
	}, "\n")
	records, err := ReadJSONL(strings.NewReader(valid))
	if err != nil || len(records) != 2 || records[1].AddedDelayUS == nil || *records[1].AddedDelayUS != 750 {
		t.Fatalf("records=%+v err=%v", records, err)
	}

	tests := map[string]string{
		"unknown field":  `{"trace_id":"x","label":"a","relative_time_us":0,"direction":"up","wire_bytes":1,"payload":"secret"}`,
		"unsafe label":   `{"trace_id":"x/y","label":"a","relative_time_us":0,"direction":"up","wire_bytes":1}`,
		"bad direction":  `{"trace_id":"x","label":"a","relative_time_us":0,"direction":"sideways","wire_bytes":1}`,
		"oversized cell": `{"trace_id":"x","label":"a","relative_time_us":0,"direction":"up","wire_bytes":65536}`,
		"trailing json":  `{"trace_id":"x","label":"a","relative_time_us":0,"direction":"up","wire_bytes":1} {}`,
		"time reversal": strings.Join([]string{
			`{"trace_id":"x","label":"a","relative_time_us":2,"direction":"up","wire_bytes":1}`,
			`{"trace_id":"x","label":"a","relative_time_us":1,"direction":"up","wire_bytes":1}`,
		}, "\n"),
		"label mutation": strings.Join([]string{
			`{"trace_id":"x","label":"a","relative_time_us":0,"direction":"up","wire_bytes":1}`,
			`{"trace_id":"x","label":"b","relative_time_us":1,"direction":"up","wire_bytes":1}`,
		}, "\n"),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadJSONL(strings.NewReader(input)); err == nil {
				t.Fatal("accepted invalid JSONL")
			}
		})
	}

	tooLong := `{"trace_id":"x","label":"a","relative_time_us":0,"direction":"up","wire_bytes":1,"` +
		strings.Repeat("x", MaximumLineBytes) + `":0}`
	if _, err := ReadJSONL(strings.NewReader(tooLong)); err == nil {
		t.Fatal("accepted overlong JSONL line")
	}
}

func TestAnalyzeKnownDatasetIsDeterministicAndTraceSplit(t *testing.T) {
	records := knownDataset()
	first, err := Analyze(records)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Analyze(records)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("nondeterministic report:\n%+v\n%+v", first, second)
	}
	if first.TraceCount != 12 || first.RecordCount != len(records) ||
		first.LabelBalance["fixed"] != 6 || first.LabelBalance["mosaic"] != 6 {
		t.Fatalf("unexpected counts: %+v", first)
	}
	if first.Classifier.Accuracy != 1 || first.Classifier.BalancedAccuracy != 1 ||
		first.Classifier.TrainingTraces+first.Classifier.TestTraces != first.TraceCount {
		t.Fatalf("unexpected classifier result: %+v", first.Classifier)
	}
	if first.Delay.Samples != 12 || first.Delay.P50US <= 0 || first.Delay.P95US < first.Delay.P50US {
		t.Fatalf("unexpected delay summary: %+v", first.Delay)
	}
	if first.SizeHistogram.Total() != len(records) || first.Bursts.Total == 0 || first.DiversityScore <= 0 {
		t.Fatalf("unexpected metadata summaries: %+v", first)
	}

	traces, err := buildTraces(records)
	if err != nil {
		t.Fatal(err)
	}
	training, test, err := splitTraces(traces)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(training))
	for _, trace := range training {
		seen[trace.ID] = true
	}
	for _, trace := range test {
		if seen[trace.ID] {
			t.Fatalf("trace %q leaked across split", trace.ID)
		}
	}
}

func TestDiversityScoreDistinguishesIdenticalAndVariedSessions(t *testing.T) {
	identical := make([]traceFeatures, 4)
	for index := range identical {
		identical[index] = traceFeatures{Label: "same", Values: [featureCount]float64{10, 1000, 100, 0, 50, 1, 10}}
	}
	varied := append([]traceFeatures(nil), identical...)
	varied[1].Values[featureMeanSize] = 300
	varied[2].Values[featureMeanGap] = 5_000
	varied[3].Values[featureBurstCount] = 5
	if zero := diversityScore(identical); zero != 0 {
		t.Fatalf("identical diversity=%f", zero)
	}
	if score := diversityScore(varied); score <= 0 || score > 1 {
		t.Fatalf("varied diversity=%f", score)
	}
}

func FuzzReadJSONL(f *testing.F) {
	f.Add([]byte(`{"trace_id":"x","label":"a","relative_time_us":0,"direction":"up","wire_bytes":128}` + "\n"))
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > MaximumLineBytes*2 {
			t.Skip()
		}
		_, _ = ReadJSONL(bytes.NewReader(input))
	})
}

func knownDataset() []Record {
	records := make([]Record, 0, 72)
	for group, label := range []string{"fixed", "mosaic"} {
		for traceIndex := 0; traceIndex < 6; traceIndex++ {
			traceID := label + "-" + string(rune('a'+traceIndex))
			baseSize := 200 + traceIndex*5
			gap := int64(10_000 + traceIndex*250)
			if group == 1 {
				baseSize = 8_000 + traceIndex*500
				gap = 90_000 + int64(traceIndex*2_000)
			}
			for packet := 0; packet < 6; packet++ {
				direction := DirectionUp
				if packet%2 == 1 {
					direction = DirectionDown
				}
				record := Record{
					TraceID: traceID, Label: label, RelativeTimeUS: int64(packet) * gap,
					Direction: direction, WireBytes: baseSize + packet*3,
				}
				if packet == 0 {
					delay := int64(500 + group*1_000 + traceIndex*10)
					record.AddedDelayUS = &delay
				}
				records = append(records, record)
			}
		}
	}
	return records
}

func TestReportJSONDoesNotContainTraceIdentifiers(t *testing.T) {
	report, err := Analyze(knownDataset())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("fixed-a")) || bytes.Contains(encoded, []byte("mosaic-a")) {
		t.Fatalf("report leaked trace identifiers: %s", encoded)
	}
}
