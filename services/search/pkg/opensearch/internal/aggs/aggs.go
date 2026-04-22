// Package aggs translates the proto aggregation options into the OpenSearch
// aggregation DSL and parses the resulting response. It lives in an internal
// subpackage so the unit tests don't have to boot the Docker-based OpenSearch
// test container that the parent package's TestMain requires.
package aggs

import (
	"encoding/json"
	"fmt"
	"strconv"

	searchsvc "github.com/opencloud-eu/opencloud/protogen/gen/opencloud/services/search/v0"
)

// DefaultFacetSize matches the bleve backend: when no size is given we pull a
// generous amount of buckets per space and let the service-layer cross-space
// merger trim back down to the top N.
const DefaultFacetSize = 1000

// Build translates the request's AggregationOptions into the OpenSearch
// aggregation DSL. Terms, range, metric, and nested sub-aggregations are all
// supported: entries are named with an index-derived prefix so repeated aggs
// on the same field (e.g. two sum metrics over audio.duration) don't collide.
func Build(opts []*searchsvc.AggregationOption) map[string]any {
	return buildLevel(opts, "a")
}

func buildLevel(opts []*searchsvc.AggregationOption, prefix string) map[string]any {
	if len(opts) == 0 {
		return nil
	}
	aggs := map[string]any{}
	for i, opt := range opts {
		name := fmt.Sprintf("%s_%d", prefix, i)
		if entry := buildOne(opt, name); entry != nil {
			aggs[name] = entry
		}
	}
	if len(aggs) == 0 {
		return nil
	}
	return aggs
}

func buildOne(opt *searchsvc.AggregationOption, name string) map[string]any {
	field := opt.GetField()
	if mk := opt.GetMetricKind(); mk != searchsvc.MetricKind_METRIC_KIND_UNSPECIFIED {
		return buildMetric(field, mk)
	}
	var entry map[string]any
	if ranges := rangesOf(opt); len(ranges) > 0 {
		entry = map[string]any{
			"range": map[string]any{
				"field":  field,
				"ranges": buildRanges(ranges),
			},
		}
	} else {
		size := int(opt.GetSize())
		if size <= 0 {
			size = DefaultFacetSize
		}
		entry = map[string]any{
			"terms": map[string]any{
				"field": field,
				"size":  size,
			},
		}
	}
	if subs := opt.GetSubAggregations(); len(subs) > 0 {
		if nested := buildLevel(subs, name); nested != nil {
			entry["aggs"] = nested
		}
	}
	return entry
}

// buildMetric emits the native OpenSearch single-value metric for sum/min/max.
// AVG is emitted as a `stats` aggregation so we can transport (sum, count)
// back for the cross-space merge; the service layer collapses that into
// value = sum/count at emit time.
func buildMetric(field string, kind searchsvc.MetricKind) map[string]any {
	switch kind {
	case searchsvc.MetricKind_METRIC_KIND_SUM:
		return map[string]any{"sum": map[string]any{"field": field}}
	case searchsvc.MetricKind_METRIC_KIND_MIN:
		return map[string]any{"min": map[string]any{"field": field}}
	case searchsvc.MetricKind_METRIC_KIND_MAX:
		return map[string]any{"max": map[string]any{"field": field}}
	case searchsvc.MetricKind_METRIC_KIND_AVG:
		return map[string]any{"stats": map[string]any{"field": field}}
	}
	return nil
}

func rangesOf(opt *searchsvc.AggregationOption) []*searchsvc.BucketRange {
	bd := opt.GetBucketDefinition()
	if bd == nil {
		return nil
	}
	return bd.GetRanges()
}

func buildRanges(ranges []*searchsvc.BucketRange) []map[string]any {
	out := make([]map[string]any, 0, len(ranges))
	for _, r := range ranges {
		entry := map[string]any{
			"key": RangeKey(r),
		}
		if v, err := strconv.ParseFloat(r.GetFrom(), 64); err == nil {
			entry["from"] = v
		}
		if v, err := strconv.ParseFloat(r.GetTo(), 64); err == nil {
			entry["to"] = v
		}
		out = append(out, entry)
	}
	return out
}

// RangeKey mirrors the bleve backend so cross-space merging keys match.
func RangeKey(r *searchsvc.BucketRange) string {
	return r.GetFrom() + "-" + r.GetTo()
}

// Parse reads the `aggregations` block on an OpenSearch search response and
// converts it into the proto representation. It preserves the order of the
// original request's AggregationOptions and recurses into sub-aggregations.
//
// Returns an error when the response block is syntactically invalid — the
// caller is expected to log and either surface or swallow the failure.
// Empty input is not an error: (nil, nil) is returned when no aggregations
// were asked for or OpenSearch did not include a block.
func Parse(raw json.RawMessage, opts []*searchsvc.AggregationOption) ([]*searchsvc.AggregationResult, error) {
	if len(raw) == 0 || len(opts) == 0 {
		return nil, nil
	}
	node, err := parseNode(raw)
	if err != nil {
		return nil, err
	}
	return parseLevel(node, opts, "a"), nil
}

// aggNode is a minimally-typed cursor over one level of the aggs response.
// Nested shapes (buckets, sub-agg subtrees) are decoded lazily once we know
// which shape the matching AggregationOption implies.
type aggNode map[string]json.RawMessage

func parseNode(raw json.RawMessage) (aggNode, error) {
	var m aggNode
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode opensearch aggregations: %w", err)
	}
	return m, nil
}

func parseLevel(node aggNode, opts []*searchsvc.AggregationOption, prefix string) []*searchsvc.AggregationResult {
	out := make([]*searchsvc.AggregationResult, 0, len(opts))
	for i, opt := range opts {
		name := fmt.Sprintf("%s_%d", prefix, i)
		raw, ok := node[name]
		if !ok {
			continue
		}
		if res := parseOne(raw, opt, name); res != nil {
			out = append(out, res)
		}
	}
	return out
}

func parseOne(raw json.RawMessage, opt *searchsvc.AggregationOption, name string) *searchsvc.AggregationResult {
	field := opt.GetField()
	if mk := opt.GetMetricKind(); mk != searchsvc.MetricKind_METRIC_KIND_UNSPECIFIED {
		return parseMetric(raw, field, mk)
	}
	var body struct {
		Buckets []json.RawMessage `json:"buckets"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil
	}
	buckets := make([]*searchsvc.Bucket, 0, len(body.Buckets))
	for _, b := range body.Buckets {
		if bucket := parseBucket(b, opt.GetSubAggregations(), name); bucket != nil {
			buckets = append(buckets, bucket)
		}
	}
	return &searchsvc.AggregationResult{
		Field:   field,
		Buckets: buckets,
	}
}

func parseBucket(raw json.RawMessage, subs []*searchsvc.AggregationOption, prefix string) *searchsvc.Bucket {
	var head struct {
		Key      any   `json:"key"`
		DocCount int64 `json:"doc_count"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil
	}
	b := &searchsvc.Bucket{
		Key:   bucketKeyToString(head.Key),
		Count: head.DocCount,
	}
	if len(subs) > 0 {
		node, err := parseNode(raw)
		if err == nil {
			b.SubAggregations = parseLevel(node, subs, prefix)
		}
	}
	return b
}

func parseMetric(raw json.RawMessage, field string, kind searchsvc.MetricKind) *searchsvc.AggregationResult {
	switch kind {
	case searchsvc.MetricKind_METRIC_KIND_SUM,
		searchsvc.MetricKind_METRIC_KIND_MIN,
		searchsvc.MetricKind_METRIC_KIND_MAX:
		var body struct {
			Value *float64 `json:"value"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil
		}
		res := &searchsvc.AggregationResult{
			Field:      field,
			MetricKind: kind,
		}
		if body.Value != nil {
			res.Value = *body.Value
		}
		return res
	case searchsvc.MetricKind_METRIC_KIND_AVG:
		var body struct {
			Sum   float64 `json:"sum"`
			Count int64   `json:"count"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil
		}
		return &searchsvc.AggregationResult{
			Field:      field,
			Sum:        body.Sum,
			Count:      body.Count,
			MetricKind: kind,
		}
	}
	return nil
}

// bucketKeyToString normalises the OpenSearch response key to a string. Term
// buckets come back as strings already, range buckets as the explicit "from-to"
// key we set in the request, and numeric term buckets (e.g. int year fields
// asked as terms) come back as JSON numbers that we stringify.
func bucketKeyToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		// Integers round-trip through float64 in encoding/json; format without
		// trailing zeros so keys match what a caller would send in a filter.
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return ""
	default:
		return ""
	}
}
