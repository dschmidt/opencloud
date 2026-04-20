package svc

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-micro.dev/v4/client"

	"github.com/opencloud-eu/opencloud/pkg/log"
	searchsvc "github.com/opencloud-eu/opencloud/protogen/gen/opencloud/services/search/v0"
)

type stubSearchService struct {
	search func(*searchsvc.SearchRequest) (*searchsvc.SearchResponse, error)
}

func (s stubSearchService) Search(_ context.Context, req *searchsvc.SearchRequest, _ ...client.CallOption) (*searchsvc.SearchResponse, error) {
	return s.search(req)
}

func (s stubSearchService) IndexSpace(_ context.Context, _ *searchsvc.IndexSpaceRequest, _ ...client.CallOption) (*searchsvc.IndexSpaceResponse, error) {
	return nil, nil
}

func TestSearchQueryHandlerForwardsAggregations(t *testing.T) {
	var captured *searchsvc.SearchRequest
	stub := stubSearchService{
		search: func(req *searchsvc.SearchRequest) (*searchsvc.SearchResponse, error) {
			captured = req
			return &searchsvc.SearchResponse{
				TotalMatches: 10,
				Aggregations: []*searchsvc.AggregationResult{{
					Field: "audio.artist",
					Buckets: []*searchsvc.Bucket{
						{Key: "Pink Floyd", Count: 7},
						{Key: "Motörhead", Count: 3},
					},
				}},
			}, nil
		},
	}

	logger := log.NewLogger()
	g := Graph{
		BaseGraphService: BaseGraphService{logger: &logger},
		searchService:    stub,
	}

	body := `{
		"requests": [{
			"entityTypes": ["driveItem"],
			"query": {"queryString": "mediatype:audio"},
			"size": 0,
			"aggregations": [{
				"field": "audio.artist",
				"size": 5,
				"bucketDefinition": {"sortBy": "count", "isDescending": true}
			}]
		}]
	}`

	req := httptest.NewRequest(http.MethodPost, "/search/query", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	g.SearchQuery(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}

	if captured == nil {
		t.Fatal("search service was not called")
	}
	if got := len(captured.Aggregations); got != 1 {
		t.Fatalf("want 1 aggregation forwarded, got %d", got)
	}
	if captured.Aggregations[0].Field != "audio.artist" {
		t.Fatalf("want field audio.artist, got %q", captured.Aggregations[0].Field)
	}

	var decoded struct {
		Value []struct {
			HitsContainers []struct {
				Aggregations []struct {
					Field   *string `json:"field"`
					Buckets []struct {
						Key   *string `json:"key"`
						Count *int64  `json:"count"`
					} `json:"buckets"`
				} `json:"aggregations"`
			} `json:"hitsContainers"`
		} `json:"value"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(decoded.Value) != 1 || len(decoded.Value[0].HitsContainers) != 1 {
		t.Fatalf("unexpected response shape: %s", rr.Body.String())
	}
	aggs := decoded.Value[0].HitsContainers[0].Aggregations
	if len(aggs) != 1 {
		t.Fatalf("want 1 aggregation in response, got %d (%s)", len(aggs), rr.Body.String())
	}
	if aggs[0].Field == nil || *aggs[0].Field != "audio.artist" {
		t.Fatalf("unexpected field: %+v", aggs[0].Field)
	}
	if len(aggs[0].Buckets) != 2 {
		t.Fatalf("want 2 buckets, got %d", len(aggs[0].Buckets))
	}
}

func TestClampPagination(t *testing.T) {
	ptr := func(v int32) *int32 { return &v }
	cases := []struct {
		name     string
		from, to *int32
		wantFrom int32
		wantSize int32
	}{
		{"defaults", nil, nil, 0, 25},
		{"zero size", ptr(5), ptr(0), 5, 0},
		{"negative from clamps to zero", ptr(-10), ptr(5), 0, 5},
		{"negative size clamps to zero", ptr(10), ptr(-1), 10, 0},
		{"oversized size clamps to max", ptr(0), ptr(1000), 0, 500},
		{"from+size overflow collapses", ptr(1<<31 - 1), ptr(500), 1<<31 - 1 - 500, 500},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotFrom, gotSize := clampPagination(c.from, c.to)
			if gotFrom != c.wantFrom || gotSize != c.wantSize {
				t.Fatalf("want from=%d size=%d, got from=%d size=%d", c.wantFrom, c.wantSize, gotFrom, gotSize)
			}
		})
	}
}

func TestSearchQueryRejectsTermsOnNumericField(t *testing.T) {
	stub := stubSearchService{
		search: func(req *searchsvc.SearchRequest) (*searchsvc.SearchResponse, error) {
			t.Fatal("search service must not be called when validation fails")
			return nil, nil
		},
	}
	logger := log.NewLogger()
	g := Graph{
		BaseGraphService: BaseGraphService{logger: &logger},
		searchService:    stub,
	}

	body := `{
		"requests": [{
			"entityTypes": ["driveItem"],
			"query": {"queryString": "mediatype:audio"},
			"size": 0,
			"aggregations": [{"field": "audio.year"}]
		}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/search/query", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	g.SearchQuery(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSearchQueryAllowsRangesOnNumericField(t *testing.T) {
	called := false
	stub := stubSearchService{
		search: func(req *searchsvc.SearchRequest) (*searchsvc.SearchResponse, error) {
			called = true
			return &searchsvc.SearchResponse{}, nil
		},
	}
	logger := log.NewLogger()
	g := Graph{
		BaseGraphService: BaseGraphService{logger: &logger},
		searchService:    stub,
	}

	body := `{
		"requests": [{
			"entityTypes": ["driveItem"],
			"query": {"queryString": "mediatype:audio"},
			"size": 0,
			"aggregations": [{
				"field": "audio.year",
				"bucketDefinition": {
					"sortBy": "keyAsString",
					"ranges": [{"from": "1970", "to": "1980"}]
				}
			}]
		}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/search/query", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	g.SearchQuery(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !called {
		t.Fatal("search service should have been called with a range aggregation")
	}
}
