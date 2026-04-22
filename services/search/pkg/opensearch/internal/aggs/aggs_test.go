package aggs

import (
	"encoding/json"
	"testing"

	searchsvc "github.com/opencloud-eu/opencloud/protogen/gen/opencloud/services/search/v0"
)

func TestBuildAggsTerms(t *testing.T) {
	res := Build([]*searchsvc.AggregationOption{
		{Field: "audio.artist", Size: 10},
	})
	if res == nil {
		t.Fatal("want aggs, got nil")
	}
	entry, ok := res["a_0"].(map[string]any)
	if !ok {
		t.Fatalf("want entry for a_0, got %T", res["a_0"])
	}
	terms, ok := entry["terms"].(map[string]any)
	if !ok {
		t.Fatalf("want terms sub-object, got %T", entry["terms"])
	}
	if terms["field"] != "audio.artist" || terms["size"] != 10 {
		t.Fatalf("unexpected terms body: %#v", terms)
	}
}

func TestBuildAggsRange(t *testing.T) {
	res := Build([]*searchsvc.AggregationOption{
		{
			Field: "audio.year",
			BucketDefinition: &searchsvc.BucketDefinition{
				Ranges: []*searchsvc.BucketRange{
					{From: "1970", To: "1980"},
					{To: "1970"},
					{From: "2020"},
				},
			},
		},
	})
	entry := res["a_0"].(map[string]any)
	r := entry["range"].(map[string]any)
	if r["field"] != "audio.year" {
		t.Fatalf("want field audio.year, got %v", r["field"])
	}
	ranges := r["ranges"].([]map[string]any)
	if len(ranges) != 3 {
		t.Fatalf("want 3 ranges, got %d", len(ranges))
	}
	if ranges[0]["key"] != "1970-1980" || ranges[0]["from"] != 1970.0 || ranges[0]["to"] != 1980.0 {
		t.Fatalf("range[0] wrong: %#v", ranges[0])
	}
	if _, has := ranges[1]["from"]; has {
		t.Fatalf("range[1] should not have from, got %#v", ranges[1])
	}
	if _, has := ranges[2]["to"]; has {
		t.Fatalf("range[2] should not have to, got %#v", ranges[2])
	}
}

func TestBuildAggsMetricsSumMinMax(t *testing.T) {
	res := Build([]*searchsvc.AggregationOption{
		{Field: "audio.duration", MetricKind: searchsvc.MetricKind_METRIC_KIND_SUM},
		{Field: "audio.duration", MetricKind: searchsvc.MetricKind_METRIC_KIND_MIN},
		{Field: "audio.duration", MetricKind: searchsvc.MetricKind_METRIC_KIND_MAX},
	})
	cases := []struct {
		name string
		kind string
	}{{"a_0", "sum"}, {"a_1", "min"}, {"a_2", "max"}}
	for _, tc := range cases {
		entry, ok := res[tc.name].(map[string]any)
		if !ok {
			t.Fatalf("missing entry %s", tc.name)
		}
		body, ok := entry[tc.kind].(map[string]any)
		if !ok {
			t.Fatalf("%s: want %s sub-object, got %#v", tc.name, tc.kind, entry)
		}
		if body["field"] != "audio.duration" {
			t.Fatalf("%s: field = %v", tc.name, body["field"])
		}
	}
}

func TestBuildAggsAvgUsesStats(t *testing.T) {
	res := Build([]*searchsvc.AggregationOption{
		{Field: "audio.duration", MetricKind: searchsvc.MetricKind_METRIC_KIND_AVG},
	})
	entry := res["a_0"].(map[string]any)
	stats, ok := entry["stats"].(map[string]any)
	if !ok {
		t.Fatalf("want stats body for AVG, got %#v", entry)
	}
	if stats["field"] != "audio.duration" {
		t.Fatalf("stats field = %v", stats["field"])
	}
}

func TestBuildAggsNested(t *testing.T) {
	res := Build([]*searchsvc.AggregationOption{
		{
			Field: "audio.artist", Size: 5,
			SubAggregations: []*searchsvc.AggregationOption{
				{
					Field: "audio.album", Size: 7,
					SubAggregations: []*searchsvc.AggregationOption{
						{Field: "audio.duration", MetricKind: searchsvc.MetricKind_METRIC_KIND_SUM},
					},
				},
			},
		},
	})
	artist := res["a_0"].(map[string]any)
	artistAggs := artist["aggs"].(map[string]any)
	album := artistAggs["a_0_0"].(map[string]any)
	albumTerms := album["terms"].(map[string]any)
	if albumTerms["field"] != "audio.album" || albumTerms["size"] != 7 {
		t.Fatalf("unexpected album terms body: %#v", albumTerms)
	}
	albumAggs := album["aggs"].(map[string]any)
	metric := albumAggs["a_0_0_0"].(map[string]any)
	if metric["sum"].(map[string]any)["field"] != "audio.duration" {
		t.Fatalf("unexpected metric body: %#v", metric)
	}
}

func TestParseAggregations(t *testing.T) {
	raw := json.RawMessage(`{
		"a_0": {
			"buckets": [
				{"key": "Pink Floyd", "doc_count": 42},
				{"key": "Motörhead", "doc_count": 35}
			]
		},
		"a_1": {
			"buckets": [
				{"key": "1970-1980", "from": 1970.0, "to": 1980.0, "doc_count": 12}
			]
		},
		"a_2": {
			"buckets": [
				{"key": 9, "doc_count": 3}
			]
		}
	}`)
	opts := []*searchsvc.AggregationOption{
		{Field: "audio.artist"},
		{Field: "audio.year"},
		{Field: "audio.track"},
	}
	out, err := Parse(raw, opts)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("want 3 aggs, got %d", len(out))
	}
	if out[0].Field != "audio.artist" || len(out[0].Buckets) != 2 ||
		out[0].Buckets[0].Key != "Pink Floyd" || out[0].Buckets[0].Count != 42 {
		t.Fatalf("audio.artist parsed wrong: %#v", out[0])
	}
	if out[1].Buckets[0].Key != "1970-1980" || out[1].Buckets[0].Count != 12 {
		t.Fatalf("audio.year parsed wrong: %#v", out[1].Buckets[0])
	}
	// numeric key stringified without trailing zeros
	if out[2].Buckets[0].Key != "9" {
		t.Fatalf("numeric-term key should stringify to \"9\", got %q", out[2].Buckets[0].Key)
	}
}

func TestParseAggregationsNested(t *testing.T) {
	raw := json.RawMessage(`{
		"a_0": {
			"buckets": [
				{
					"key": "Iron Maiden", "doc_count": 300,
					"a_0_0": {
						"buckets": [
							{
								"key": "The Number of the Beast", "doc_count": 8,
								"a_0_0_0": {"value": 2756000.0}
							},
							{
								"key": "Powerslave", "doc_count": 8,
								"a_0_0_0": {"value": 3061000.0}
							}
						]
					}
				}
			]
		}
	}`)
	opts := []*searchsvc.AggregationOption{
		{
			Field: "audio.artist",
			SubAggregations: []*searchsvc.AggregationOption{
				{
					Field: "audio.album",
					SubAggregations: []*searchsvc.AggregationOption{
						{Field: "audio.duration", MetricKind: searchsvc.MetricKind_METRIC_KIND_SUM},
					},
				},
			},
		},
	}
	out, err := Parse(raw, opts)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(out) != 1 || out[0].Field != "audio.artist" || len(out[0].Buckets) != 1 {
		t.Fatalf("top-level artist parsed wrong: %#v", out)
	}
	artistBucket := out[0].Buckets[0]
	if artistBucket.Key != "Iron Maiden" || artistBucket.Count != 300 {
		t.Fatalf("artist bucket wrong: %#v", artistBucket)
	}
	if len(artistBucket.SubAggregations) != 1 {
		t.Fatalf("want one sub-agg on artist bucket, got %d", len(artistBucket.SubAggregations))
	}
	albumAgg := artistBucket.SubAggregations[0]
	if albumAgg.Field != "audio.album" || len(albumAgg.Buckets) != 2 {
		t.Fatalf("album agg wrong: %#v", albumAgg)
	}
	nob := albumAgg.Buckets[0]
	if nob.Key != "The Number of the Beast" || nob.Count != 8 {
		t.Fatalf("album[0] wrong: %#v", nob)
	}
	if len(nob.SubAggregations) != 1 {
		t.Fatalf("album[0] should carry the sum metric, got %d sub-aggs", len(nob.SubAggregations))
	}
	metric := nob.SubAggregations[0]
	if metric.Field != "audio.duration" ||
		metric.MetricKind != searchsvc.MetricKind_METRIC_KIND_SUM ||
		metric.Value != 2756000.0 {
		t.Fatalf("metric wrong: %#v", metric)
	}
}

func TestParseAggregationsMetricsSumMinMax(t *testing.T) {
	raw := json.RawMessage(`{
		"a_0": {"value": 1234.5},
		"a_1": {"value": 10.0},
		"a_2": {"value": 99.0}
	}`)
	opts := []*searchsvc.AggregationOption{
		{Field: "audio.duration", MetricKind: searchsvc.MetricKind_METRIC_KIND_SUM},
		{Field: "audio.duration", MetricKind: searchsvc.MetricKind_METRIC_KIND_MIN},
		{Field: "audio.duration", MetricKind: searchsvc.MetricKind_METRIC_KIND_MAX},
	}
	out, err := Parse(raw, opts)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("want 3 metrics, got %d", len(out))
	}
	if out[0].MetricKind != searchsvc.MetricKind_METRIC_KIND_SUM || out[0].Value != 1234.5 {
		t.Fatalf("sum wrong: %#v", out[0])
	}
	if out[1].MetricKind != searchsvc.MetricKind_METRIC_KIND_MIN || out[1].Value != 10.0 {
		t.Fatalf("min wrong: %#v", out[1])
	}
	if out[2].MetricKind != searchsvc.MetricKind_METRIC_KIND_MAX || out[2].Value != 99.0 {
		t.Fatalf("max wrong: %#v", out[2])
	}
}

func TestParseAggregationsMetricNullValue(t *testing.T) {
	// OpenSearch returns value: null when a metric has no matching docs.
	raw := json.RawMessage(`{"a_0": {"value": null}}`)
	opts := []*searchsvc.AggregationOption{
		{Field: "audio.duration", MetricKind: searchsvc.MetricKind_METRIC_KIND_SUM},
	}
	out, err := Parse(raw, opts)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(out) != 1 || out[0].Value != 0 || out[0].MetricKind != searchsvc.MetricKind_METRIC_KIND_SUM {
		t.Fatalf("null metric should decode to zero value, got %#v", out)
	}
}

func TestParseAggregationsAvgFromStats(t *testing.T) {
	raw := json.RawMessage(`{
		"a_0": {"count": 100, "min": 30000.0, "max": 500000.0, "avg": 245000.0, "sum": 24500000.0}
	}`)
	opts := []*searchsvc.AggregationOption{
		{Field: "audio.duration", MetricKind: searchsvc.MetricKind_METRIC_KIND_AVG},
	}
	out, err := Parse(raw, opts)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 agg, got %d", len(out))
	}
	if out[0].MetricKind != searchsvc.MetricKind_METRIC_KIND_AVG {
		t.Fatalf("metric kind wrong: %v", out[0].MetricKind)
	}
	if out[0].Sum != 24500000.0 || out[0].Count != 100 {
		t.Fatalf("avg transport wrong: sum=%v count=%v", out[0].Sum, out[0].Count)
	}
}

func TestParseAggregationsEmpty(t *testing.T) {
	if got, err := Parse(nil, []*searchsvc.AggregationOption{{Field: "x"}}); err != nil || got != nil {
		t.Fatalf("want (nil, nil), got (%#v, %v)", got, err)
	}
	if got, err := Parse(json.RawMessage(`{}`), nil); err != nil || got != nil {
		t.Fatalf("want (nil, nil) for empty opts, got (%#v, %v)", got, err)
	}
}

func TestParseAggregationsError(t *testing.T) {
	got, err := Parse(json.RawMessage(`not-json`), []*searchsvc.AggregationOption{{Field: "x"}})
	if err == nil {
		t.Fatal("want error for malformed json")
	}
	if got != nil {
		t.Fatalf("want nil result on error, got %#v", got)
	}
}
