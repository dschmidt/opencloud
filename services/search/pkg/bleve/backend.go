package bleve

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/blevesearch/bleve/v2"
	bleveSearch "github.com/blevesearch/bleve/v2/search"
	"github.com/blevesearch/bleve/v2/search/query"
	storageProvider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	"github.com/opencloud-eu/reva/v2/pkg/errtypes"
	"github.com/opencloud-eu/reva/v2/pkg/storagespace"
	"github.com/opencloud-eu/reva/v2/pkg/utils"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/opencloud-eu/opencloud/pkg/log"
	"github.com/opencloud-eu/opencloud/services/search/pkg/search"

	searchMessage "github.com/opencloud-eu/opencloud/protogen/gen/opencloud/messages/search/v0"
	searchService "github.com/opencloud-eu/opencloud/protogen/gen/opencloud/services/search/v0"
	searchQuery "github.com/opencloud-eu/opencloud/services/search/pkg/query"
)

const defaultBatchSize = 50

var _ search.Engine = (*Backend)(nil) // ensure Backend implements Engine

type Backend struct {
	index        bleve.Index
	queryCreator searchQuery.Creator[query.Query]
	log          log.Logger
}

func NewBackend(index bleve.Index, queryCreator searchQuery.Creator[query.Query], log log.Logger) *Backend {
	return &Backend{
		index:        index,
		queryCreator: queryCreator,
		log:          log,
	}
}

// Search executes a search request operation within the index.
// Returns a SearchIndexResponse object or an error.
func (b *Backend) Search(_ context.Context, sir *searchService.SearchIndexRequest) (*searchService.SearchIndexResponse, error) {
	createdQuery, err := b.queryCreator.Create(sir.Query)
	if err != nil {
		if searchQuery.IsValidationError(err) {
			return nil, errtypes.BadRequest(err.Error())
		}
		return nil, err
	}

	q := bleve.NewConjunctionQuery(
		// Skip documents that have been marked as deleted
		&query.BoolFieldQuery{
			Bool:     false,
			FieldVal: "Deleted",
		},
		createdQuery,
	)

	if sir.Ref != nil {
		q.Conjuncts = append(
			q.Conjuncts,
			&query.TermQuery{
				FieldVal: "RootID",
				Term: storagespace.FormatResourceID(
					&storageProvider.ResourceId{
						StorageId: sir.Ref.GetResourceId().GetStorageId(),
						SpaceId:   sir.Ref.GetResourceId().GetSpaceId(),
						OpaqueId:  sir.Ref.GetResourceId().GetOpaqueId(),
					},
				),
			},
		)
	}

	bleveReq := bleve.NewSearchRequest(q)
	bleveReq.Highlight = bleve.NewHighlight()

	switch {
	case sir.PageSize == -1:
		bleveReq.Size = math.MaxInt
	case sir.PageSize == 0:
		bleveReq.Size = 200
	default:
		bleveReq.Size = int(sir.PageSize)
	}

	for _, agg := range sir.GetAggregations() {
		bleveReq.AddFacet(agg.GetField(), newBleveFacetRequest(agg))
	}

	// Sub-aggregations on bleve need the matched hit set to fold
	// through, not just the count-only term facets. If any requested
	// aggregation asks for sub-aggregations, widen the page so the
	// emulator has enough docs to work with. The caller's requested
	// PageSize wins if it's already bigger.
	if needsSubAggScan(sir.GetAggregations()) && bleveReq.Size < subAggScanSize {
		bleveReq.Size = subAggScanSize
	}

	bleveReq.Fields = []string{"*"}
	res, err := b.index.Search(bleveReq)
	if err != nil {
		return nil, err
	}

	matches := make([]*searchMessage.Match, 0, len(res.Hits))
	totalMatches := res.Total
	for _, hit := range res.Hits {
		if sir.Ref != nil {
			hitPath := strings.TrimSuffix(getFieldValue[string](hit.Fields, "Path"), "/")
			requestedPath := utils.MakeRelativePath(sir.Ref.Path)
			isRoot := hitPath == requestedPath

			if !isRoot && requestedPath != "." && !strings.HasPrefix(hitPath, requestedPath+"/") {
				totalMatches--
				continue
			}
		}

		rootID, err := storagespace.ParseID(getFieldValue[string](hit.Fields, "RootID"))
		if err != nil {
			return nil, err
		}

		rID, err := storagespace.ParseID(getFieldValue[string](hit.Fields, "ID"))
		if err != nil {
			return nil, err
		}

		pID, _ := storagespace.ParseID(getFieldValue[string](hit.Fields, "ParentID"))
		match := &searchMessage.Match{
			Score: float32(hit.Score),
			Entity: &searchMessage.Entity{
				Ref: &searchMessage.Reference{
					ResourceId: resourceIDtoSearchID(rootID),
					Path:       getFieldValue[string](hit.Fields, "Path"),
				},
				Id:         resourceIDtoSearchID(rID),
				Name:       getFieldValue[string](hit.Fields, "Name"),
				ParentId:   resourceIDtoSearchID(pID),
				Size:       uint64(getFieldValue[float64](hit.Fields, "Size")),
				Type:       uint64(getFieldValue[float64](hit.Fields, "Type")),
				MimeType:   getFieldValue[string](hit.Fields, "MimeType"),
				Deleted:    getFieldValue[bool](hit.Fields, "Deleted"),
				Tags:       getFieldSliceValue[string](hit.Fields, "Tags"),
				Favorites:  getFieldSliceValue[string](hit.Fields, "Favorites"),
				Highlights: getFragmentValue(hit.Fragments, "Content", 0),
				Audio:      getAudioValue[searchMessage.Audio](hit.Fields),
				Image:      getImageValue[searchMessage.Image](hit.Fields),
				Location:   getLocationValue[searchMessage.GeoCoordinates](hit.Fields),
				Photo:      getPhotoValue[searchMessage.Photo](hit.Fields),
			},
		}

		if mtime, err := time.Parse(time.RFC3339, getFieldValue[string](hit.Fields, "Mtime")); err == nil {
			match.Entity.LastModifiedTime = &timestamppb.Timestamp{Seconds: mtime.Unix(), Nanos: int32(mtime.Nanosecond())}
		}

		matches = append(matches, match)
	}

	return &searchService.SearchIndexResponse{
		Matches:      matches,
		TotalMatches: int32(totalMatches),
		Aggregations: extractBleveAggregations(res, sir.GetAggregations()),
	}, nil
}

// subAggScanSize caps how many matched hits we walk when emulating
// sub-aggregations. Higher is slower per query but keeps deeper
// buckets visible. math.MaxInt instructs bleve to return everything.
const subAggScanSize = math.MaxInt

func needsSubAggScan(aggs []*searchService.AggregationOption) bool {
	for _, agg := range aggs {
		if len(agg.GetSubAggregations()) > 0 {
			return true
		}
	}
	return false
}

// defaultFacetSize is used when the request does not specify a size. Bleve
// requires a positive integer; we collect up to this many buckets per space
// and let the service layer trim after cross-space merging.
const defaultFacetSize = 1000

func newBleveFacetRequest(agg *searchService.AggregationOption) *bleve.FacetRequest {
	size := int(agg.GetSize())
	if size <= 0 {
		size = defaultFacetSize
	}
	fr := bleve.NewFacetRequest(agg.GetField(), size)
	for _, r := range aggregationRanges(agg) {
		minP := parseFloatPtr(r.GetFrom())
		maxP := parseFloatPtr(r.GetTo())
		fr.AddNumericRange(rangeBucketKey(r), minP, maxP)
	}
	return fr
}

func extractBleveAggregations(res *bleve.SearchResult, aggs []*searchService.AggregationOption) []*searchService.AggregationResult {
	if len(aggs) == 0 || len(res.Facets) == 0 {
		return nil
	}
	out := make([]*searchService.AggregationResult, 0, len(aggs))
	for _, agg := range aggs {
		fr, ok := res.Facets[agg.GetField()]
		if !ok {
			continue
		}
		buckets := make([]*searchService.Bucket, 0)
		if len(aggregationRanges(agg)) > 0 {
			for _, nr := range fr.NumericRanges {
				buckets = append(buckets, &searchService.Bucket{
					Key:   nr.Name,
					Count: int64(nr.Count),
				})
			}
		} else {
			for _, t := range fr.Terms.Terms() {
				buckets = append(buckets, &searchService.Bucket{
					Key:   t.Term,
					Count: int64(t.Count),
				})
			}
		}
		if subAggs := agg.GetSubAggregations(); len(subAggs) > 0 {
			attachSubAggregations(res, agg.GetField(), subAggs, buckets)
		}
		out = append(out, &searchService.AggregationResult{
			Field:   agg.GetField(),
			Buckets: buckets,
		})
	}
	return out
}

// subAcc is the recursive accumulator used by attachSubAggregations
// to emulate composite aggregations on bleve. Each node in the tree
// represents the state for one sub-aggregation under a given parent
// bucket (either the top-level term bucket or a nested term value).
type subAcc struct {
	// Terms sub-aggregation state:
	// - count per distinct child value
	// - recursive accumulators for this sub-agg's own sub-aggregations,
	//   indexed by (child-value, sub-sub-agg-position).
	termCount map[string]int64
	termSubs  map[string][]*subAcc

	// Metric sub-aggregation state:
	metricVal float64 // SUM/MIN/MAX
	sum       float64 // AVG transport: numerator
	count     int64   // AVG transport: denominator

	seen bool // at least one hit contributed
}

// newSubAcc allocates an accumulator tailored to the given sub-agg.
func newSubAcc(sa *searchService.AggregationOption) *subAcc {
	a := &subAcc{}
	if sa.GetMetricKind() == searchService.MetricKind_METRIC_KIND_UNSPECIFIED {
		a.termCount = map[string]int64{}
		if len(sa.GetSubAggregations()) > 0 {
			a.termSubs = map[string][]*subAcc{}
		}
	}
	return a
}

// accumulateHit folds one hit's fields into a single sub-agg's
// accumulator, recursing into any grand-sub-aggregations.
func accumulateHit(a *subAcc, sa *searchService.AggregationOption, hit *bleveSearch.DocumentMatch) {
	switch sa.GetMetricKind() {
	case searchService.MetricKind_METRIC_KIND_UNSPECIFIED:
		val, ok := hit.Fields[sa.GetField()].(string)
		if !ok || val == "" {
			return
		}
		a.termCount[val]++
		a.seen = true
		if subs := sa.GetSubAggregations(); len(subs) > 0 {
			childAccs, ok := a.termSubs[val]
			if !ok {
				childAccs = make([]*subAcc, len(subs))
				for i, ssa := range subs {
					childAccs[i] = newSubAcc(ssa)
				}
				a.termSubs[val] = childAccs
			}
			for i, ssa := range subs {
				accumulateHit(childAccs[i], ssa, hit)
			}
		}
	default:
		v, ok := numericFieldValue(hit.Fields[sa.GetField()])
		if !ok {
			return
		}
		switch sa.GetMetricKind() {
		case searchService.MetricKind_METRIC_KIND_SUM:
			a.metricVal += v
		case searchService.MetricKind_METRIC_KIND_MIN:
			if !a.seen || v < a.metricVal {
				a.metricVal = v
			}
		case searchService.MetricKind_METRIC_KIND_MAX:
			if !a.seen || v > a.metricVal {
				a.metricVal = v
			}
		case searchService.MetricKind_METRIC_KIND_AVG:
			a.sum += v
			a.count++
		}
		a.seen = true
	}
}

// emitAcc materialises a sub-agg accumulator into the proto result
// type, recursing for terms with their own sub-aggregations.
func emitAcc(a *subAcc, sa *searchService.AggregationOption) *searchService.AggregationResult {
	if sa.GetMetricKind() != searchService.MetricKind_METRIC_KIND_UNSPECIFIED {
		if !a.seen {
			return nil
		}
		r := &searchService.AggregationResult{
			Field:      sa.GetField(),
			MetricKind: sa.GetMetricKind(),
		}
		if sa.GetMetricKind() == searchService.MetricKind_METRIC_KIND_AVG {
			r.Sum = a.sum
			r.Count = a.count
		} else {
			r.Value = a.metricVal
		}
		return r
	}

	subs := sa.GetSubAggregations()
	childBuckets := make([]*searchService.Bucket, 0, len(a.termCount))
	for term, count := range a.termCount {
		b := &searchService.Bucket{Key: term, Count: count}
		if len(subs) > 0 {
			if childAccs, ok := a.termSubs[term]; ok {
				for i, ssa := range subs {
					if sub := emitAcc(childAccs[i], ssa); sub != nil {
						b.SubAggregations = append(b.SubAggregations, sub)
					}
				}
			}
		}
		childBuckets = append(childBuckets, b)
	}
	if sz := int(sa.GetSize()); sz > 0 && len(childBuckets) > sz {
		childBuckets = childBuckets[:sz]
	}
	return &searchService.AggregationResult{
		Field:   sa.GetField(),
		Buckets: childBuckets,
	}
}

// attachSubAggregations folds the matched hit set into nested
// aggregation results scoped to each parent bucket. Runs a single
// hit walk and dispatches each hit into a recursive accumulator tree
// mirroring the requested sub-aggregation shape — so terms nested
// inside terms, metrics nested inside terms, and any depth thereof
// all work.
func attachSubAggregations(res *bleve.SearchResult, parentField string, subAggs []*searchService.AggregationOption, buckets []*searchService.Bucket) {
	bucketByKey := make(map[string]*searchService.Bucket, len(buckets))
	for _, b := range buckets {
		bucketByKey[b.GetKey()] = b
	}

	perParent := make(map[string][]*subAcc, len(buckets))
	for _, b := range buckets {
		accs := make([]*subAcc, len(subAggs))
		for i, sa := range subAggs {
			accs[i] = newSubAcc(sa)
		}
		perParent[b.GetKey()] = accs
	}

	for _, hit := range res.Hits {
		parentVal, ok := hit.Fields[parentField].(string)
		if !ok || parentVal == "" {
			continue
		}
		accs, ok := perParent[parentVal]
		if !ok {
			continue
		}
		for i, sa := range subAggs {
			accumulateHit(accs[i], sa, hit)
		}
	}

	for key, accs := range perParent {
		b := bucketByKey[key]
		for i, sa := range subAggs {
			if r := emitAcc(accs[i], sa); r != nil {
				b.SubAggregations = append(b.SubAggregations, r)
			}
		}
	}
}

// numericFieldValue coerces a bleve stored-field value into float64.
// Bleve surfaces numeric fields as float64 directly; we also accept
// a string representation for robustness against docvalue quirks.
func numericFieldValue(raw interface{}) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

func aggregationRanges(agg *searchService.AggregationOption) []*searchService.BucketRange {
	bd := agg.GetBucketDefinition()
	if bd == nil {
		return nil
	}
	return bd.GetRanges()
}

// rangeBucketKey formats a numeric/date range as a single string of the form
// "from-to" so that the service-layer merge can key by it and the response
// carries a stable identifier. Open-ended sides render as "-N" or "N-".
func rangeBucketKey(r *searchService.BucketRange) string {
	return r.GetFrom() + "-" + r.GetTo()
}

func parseFloatPtr(s string) *float64 {
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}

func (b *Backend) DocCount() (uint64, error) {
	return b.index.DocCount()
}

func (b *Backend) Upsert(id string, r search.Resource) error {
	batch, err := b.NewBatch(defaultBatchSize)
	if err != nil {
		return err
	}

	if err := batch.Upsert(id, r); err != nil {
		return err
	}

	return batch.Push()
}

func (b *Backend) Move(rootID, parentID, location string) error {
	batch, err := b.NewBatch(defaultBatchSize)
	if err != nil {
		return err
	}

	if err := batch.Move(rootID, parentID, location); err != nil {
		return err
	}

	return batch.Push()
}

func (b *Backend) Delete(id string) error {
	batch, err := b.NewBatch(defaultBatchSize)
	if err != nil {
		return err
	}

	if err := batch.Delete(id); err != nil {
		return err
	}

	return batch.Push()
}

func (b *Backend) Restore(id string) error {
	batch, err := b.NewBatch(defaultBatchSize)
	if err != nil {
		return err
	}

	if err := batch.Restore(id); err != nil {
		return err
	}

	return batch.Push()
}

func (b *Backend) Purge(id string, onlyDeleted bool) error {
	batch, err := b.NewBatch(defaultBatchSize)
	if err != nil {
		return err
	}

	if err := batch.Purge(id, onlyDeleted); err != nil {
		return err
	}

	return batch.Push()
}

func (b *Backend) NewBatch(size int) (search.BatchOperator, error) {
	return NewBatch(b.index, size)
}
