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
	entry, ok := res["audio.artist"].(map[string]any)
	if !ok {
		t.Fatalf("want entry for audio.artist, got %T", res["audio.artist"])
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
	entry := res["audio.year"].(map[string]any)
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

func TestParseAggregations(t *testing.T) {
	raw := json.RawMessage(`{
		"audio.artist": {
			"buckets": [
				{"key": "Pink Floyd", "doc_count": 42},
				{"key": "Motörhead", "doc_count": 35}
			]
		},
		"audio.year": {
			"buckets": [
				{"key": "1970-1980", "from": 1970.0, "to": 1980.0, "doc_count": 12}
			]
		},
		"audio.track": {
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
