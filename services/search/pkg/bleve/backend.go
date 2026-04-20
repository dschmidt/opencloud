package bleve

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/blevesearch/bleve/v2"
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

// defaultFacetSize is used when the request does not specify a size. Bleve
// requires a positive integer; we collect up to this many buckets per space
// and let the service layer trim after cross-space merging.
const defaultFacetSize = 1000

func newBleveFacetRequest(agg *searchService.AggregationOption) *bleve.FacetRequest {
	size := int(agg.GetSize())
	if size <= 0 {
		size = defaultFacetSize
	}
	return bleve.NewFacetRequest(agg.GetField(), size)
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
		for _, t := range fr.Terms.Terms() {
			buckets = append(buckets, &searchService.Bucket{
				Key:                    t.Term,
				Count:                  int64(t.Count),
				AggregationFilterToken: t.Term,
			})
		}
		out = append(out, &searchService.AggregationResult{
			Field:   agg.GetField(),
			Buckets: buckets,
		})
	}
	return out
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
