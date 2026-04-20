// Package aggs translates the proto aggregation options into the OpenSearch
// aggregation DSL and parses the resulting response. It lives in an internal
// subpackage so the unit tests don't have to boot the Docker-based OpenSearch
// test container that the parent package's TestMain requires.
package aggs

import (
	"encoding/json"
	"strconv"

	searchsvc "github.com/opencloud-eu/opencloud/protogen/gen/opencloud/services/search/v0"
)

// DefaultFacetSize matches the bleve backend: when no size is given we pull a
// generous amount of buckets per space and let the service-layer cross-space
// merger trim back down to the top N.
const DefaultFacetSize = 1000

// Build translates the request's AggregationOptions into the OpenSearch
// aggregation DSL. If an option carries ranges we emit a range aggregation,
// otherwise a terms aggregation. The resulting map goes straight into the
// request body's "aggs" block.
func Build(opts []*searchsvc.AggregationOption) map[string]any {
	if len(opts) == 0 {
		return nil
	}
	aggs := map[string]any{}
	for _, opt := range opts {
		field := opt.GetField()
		if ranges := rangesOf(opt); len(ranges) > 0 {
			aggs[field] = map[string]any{
				"range": map[string]any{
					"field":  field,
					"ranges": buildRanges(ranges),
				},
			}
			continue
		}
		size := int(opt.GetSize())
		if size <= 0 {
			size = DefaultFacetSize
		}
		aggs[field] = map[string]any{
			"terms": map[string]any{
				"field": field,
				"size":  size,
			},
		}
	}
	return aggs
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
// original request's AggregationOptions.
func Parse(raw json.RawMessage, opts []*searchsvc.AggregationOption) []*searchsvc.AggregationResult {
	if len(raw) == 0 || len(opts) == 0 {
		return nil
	}
	var byField map[string]struct {
		Buckets []struct {
			Key      any   `json:"key"`
			DocCount int64 `json:"doc_count"`
		} `json:"buckets"`
	}
	if err := json.Unmarshal(raw, &byField); err != nil {
		return nil
	}
	out := make([]*searchsvc.AggregationResult, 0, len(opts))
	for _, opt := range opts {
		field := opt.GetField()
		resp, ok := byField[field]
		if !ok {
			continue
		}
		buckets := make([]*searchsvc.Bucket, 0, len(resp.Buckets))
		for _, b := range resp.Buckets {
			buckets = append(buckets, &searchsvc.Bucket{
				Key:   bucketKeyToString(b.Key),
				Count: b.DocCount,
			})
		}
		out = append(out, &searchsvc.AggregationResult{
			Field:   field,
			Buckets: buckets,
		})
	}
	return out
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
