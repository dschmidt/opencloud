package svc

import (
	"context"
	"net/http"
	"path"
	"strings"
	"time"

	storageprovider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	"github.com/go-chi/render"
	libregraph "github.com/opencloud-eu/libre-graph-api-go"
	revaCtx "github.com/opencloud-eu/reva/v2/pkg/ctx"
	"github.com/opencloud-eu/reva/v2/pkg/storagespace"
	merrors "go-micro.dev/v4/errors"
	"go-micro.dev/v4/metadata"

	searchmsg "github.com/opencloud-eu/opencloud/protogen/gen/opencloud/messages/search/v0"
	searchsvc "github.com/opencloud-eu/opencloud/protogen/gen/opencloud/services/search/v0"
	"github.com/opencloud-eu/opencloud/services/graph/pkg/errorcode"
)

// SearchQuery runs one or more search requests and returns the results grouped
// by request. Inspired by the MS Graph searchQuery endpoint.
func (g Graph) SearchQuery(w http.ResponseWriter, r *http.Request) {
	var req libregraph.SearchQueryRequest
	if err := StrictJSONUnmarshal(r.Body, &req); err != nil {
		g.logger.Debug().Err(err).Msg("could not decode search query request")
		errorcode.InvalidRequest.Render(w, r, http.StatusBadRequest, "invalid body schema definition")
		return
	}

	if len(req.Requests) == 0 {
		errorcode.InvalidRequest.Render(w, r, http.StatusBadRequest, "requests array must not be empty")
		return
	}

	th := r.Header.Get(revaCtx.TokenHeader)
	ctx := revaCtx.ContextSetToken(r.Context(), th)
	ctx = metadata.Set(ctx, revaCtx.TokenHeader, th)

	responses := make([]libregraph.SearchResponse, 0, len(req.Requests))
	for _, sr := range req.Requests {
		sresp, err := g.runSingleSearch(ctx, sr)
		if err != nil {
			g.renderSearchError(w, r, err)
			return
		}
		responses = append(responses, sresp)
	}

	render.Status(r, http.StatusOK)
	render.JSON(w, r, libregraph.SearchQuery200Response{Value: responses})
}

func (g Graph) runSingleSearch(ctx context.Context, sr libregraph.SearchRequest) (libregraph.SearchResponse, error) {
	from := int32(0)
	if sr.From != nil {
		from = *sr.From
	}
	size := int32(25)
	if sr.Size != nil {
		size = *sr.Size
	}

	// the gRPC layer has no dedicated `from` field — ask for `from+size` matches
	// and slice client-side. good enough for early offsets; can be revisited when
	// paging perf matters.
	pageSize := from + size
	if size == 0 {
		pageSize = 0
	}

	rsp, err := g.searchService.Search(ctx, &searchsvc.SearchRequest{
		Query:        applyAggregationFilters(sr.Query.QueryString, sr.AggregationFilters),
		PageSize:     pageSize,
		Aggregations: libregraphAggregationsToSearch(sr.Aggregations),
	})
	if err != nil {
		return libregraph.SearchResponse{}, err
	}

	hits := make([]libregraph.SearchHit, 0)
	if size > 0 {
		start := min(int(from), len(rsp.Matches))
		end := min(start+int(size), len(rsp.Matches))
		for i := start; i < end; i++ {
			hits = append(hits, matchToSearchHit(rsp.Matches[i], int32(i+1)))
		}
	}

	total := int64(rsp.TotalMatches)
	more := int64(from+size) < total
	return libregraph.SearchResponse{
		SearchTerms: []string{sr.Query.QueryString},
		HitsContainers: []libregraph.SearchHitsContainer{{
			Hits:                 hits,
			Total:                &total,
			MoreResultsAvailable: &more,
			Aggregations:         searchAggregationsToLibregraph(rsp.Aggregations),
		}},
	}, nil
}

// applyAggregationFilters joins the query string with the aggregation filters
// from a previous search response. Each filter is a ready-made KQL snippet
// like `audio.artist:"Pink Floyd"`; multiple filters are AND-combined.
func applyAggregationFilters(q string, filters []string) string {
	if len(filters) == 0 {
		return q
	}
	parts := make([]string, 0, len(filters)+1)
	if q != "" {
		parts = append(parts, "("+q+")")
	}
	for _, f := range filters {
		if f == "" {
			continue
		}
		parts = append(parts, "("+f+")")
	}
	return strings.Join(parts, " AND ")
}

func libregraphAggregationsToSearch(in []libregraph.AggregationOption) []*searchsvc.AggregationOption {
	if len(in) == 0 {
		return nil
	}
	out := make([]*searchsvc.AggregationOption, 0, len(in))
	for _, a := range in {
		agg := &searchsvc.AggregationOption{Field: a.Field}
		if a.Size != nil {
			agg.Size = *a.Size
		}
		if a.BucketDefinition != nil {
			agg.BucketDefinition = libregraphBucketDefinitionToSearch(*a.BucketDefinition)
		}
		out = append(out, agg)
	}
	return out
}

func libregraphBucketDefinitionToSearch(in libregraph.BucketDefinition) *searchsvc.BucketDefinition {
	bd := &searchsvc.BucketDefinition{SortBy: in.SortBy}
	if in.IsDescending != nil {
		bd.IsDescending = *in.IsDescending
	}
	if in.MinimumCount != nil {
		bd.MinimumCount = *in.MinimumCount
	}
	if len(in.Ranges) > 0 {
		bd.Ranges = make([]*searchsvc.BucketRange, 0, len(in.Ranges))
		for _, r := range in.Ranges {
			br := &searchsvc.BucketRange{}
			if r.From != nil {
				br.From = *r.From
			}
			if r.To != nil {
				br.To = *r.To
			}
			bd.Ranges = append(bd.Ranges, br)
		}
	}
	return bd
}

func searchAggregationsToLibregraph(in []*searchsvc.AggregationResult) []libregraph.SearchAggregation {
	if len(in) == 0 {
		return nil
	}
	out := make([]libregraph.SearchAggregation, 0, len(in))
	for _, a := range in {
		field := a.GetField()
		buckets := make([]libregraph.SearchBucket, 0, len(a.GetBuckets()))
		for _, b := range a.GetBuckets() {
			key := b.GetKey()
			count := b.GetCount()
			// aggregationFilterToken intentionally left unset: the MS Graph
			// round-trip ("use this token in a subsequent aggregationFilters")
			// would require opaque server-side tokens to stay correct for
			// values containing KQL specials, empty strings or range buckets.
			// Returning broken tokens is worse than returning none, so until
			// we wire up proper opaque-token encoding the field stays empty.
			buckets = append(buckets, libregraph.SearchBucket{
				Key:   &key,
				Count: &count,
			})
		}
		out = append(out, libregraph.SearchAggregation{
			Field:   &field,
			Buckets: buckets,
		})
	}
	return out
}

func (g Graph) renderSearchError(w http.ResponseWriter, r *http.Request, err error) {
	e := merrors.Parse(err.Error())
	switch e.Code {
	case http.StatusBadRequest:
		errorcode.InvalidRequest.Render(w, r, http.StatusBadRequest, e.Detail)
	default:
		g.logger.Error().Err(err).Msg("search service call failed")
		errorcode.GeneralException.Render(w, r, http.StatusInternalServerError, err.Error())
	}
}

func matchToSearchHit(m *searchmsg.Match, rank int32) libregraph.SearchHit {
	hit := libregraph.SearchHit{
		HitId: libregraph.PtrString(searchEntityHitID(m.GetEntity())),
		Rank:  &rank,
	}
	if h := m.GetEntity().GetHighlights(); h != "" {
		hit.Summary = libregraph.PtrString(h)
	}
	di := searchEntityToDriveItem(m.GetEntity())
	hit.Resource = di
	return hit
}

func searchEntityHitID(e *searchmsg.Entity) string {
	return storagespace.FormatResourceID(&storageprovider.ResourceId{
		StorageId: e.GetId().GetStorageId(),
		SpaceId:   e.GetId().GetSpaceId(),
		OpaqueId:  e.GetId().GetOpaqueId(),
	})
}

func searchEntityToDriveItem(e *searchmsg.Entity) *libregraph.DriveItem {
	size := int64(e.GetSize())
	di := &libregraph.DriveItem{
		Id:   libregraph.PtrString(searchEntityHitID(e)),
		Name: libregraph.PtrString(e.GetName()),
		Size: &size,
	}
	if etag := e.GetEtag(); etag != "" {
		di.ETag = &etag
	}
	if mt := e.GetLastModifiedTime(); mt != nil {
		lm := time.Unix(mt.GetSeconds(), int64(mt.GetNanos())).UTC()
		di.LastModifiedDateTime = &lm
	}
	if e.GetType() == uint64(storageprovider.ResourceType_RESOURCE_TYPE_FILE) && e.GetMimeType() != "" {
		mt := e.GetMimeType()
		di.File = &libregraph.OpenGraphFile{MimeType: &mt}
	}
	if e.GetType() == uint64(storageprovider.ResourceType_RESOURCE_TYPE_CONTAINER) {
		di.Folder = &libregraph.Folder{}
	}
	if p := e.GetParentId(); p != nil {
		ref := libregraph.NewItemReference()
		ref.SetDriveId(storagespace.FormatStorageID(p.GetStorageId(), p.GetSpaceId()))
		ref.SetId(storagespace.FormatResourceID(&storageprovider.ResourceId{
			StorageId: p.GetStorageId(),
			SpaceId:   p.GetSpaceId(),
			OpaqueId:  p.GetOpaqueId(),
		}))
		if refPath := e.GetRef().GetPath(); refPath != "" {
			ref.SetName(path.Base(path.Dir(refPath)))
			ref.SetPath(path.Dir(refPath))
		}
		di.ParentReference = ref
	}
	di.Audio = searchAudioToLibregraph(e.GetAudio())
	di.Image = searchImageToLibregraph(e.GetImage())
	di.Photo = searchPhotoToLibregraph(e.GetPhoto())
	di.Location = searchLocationToLibregraph(e.GetLocation())
	return di
}

func searchAudioToLibregraph(a *searchmsg.Audio) *libregraph.Audio {
	if a == nil {
		return nil
	}
	out := &libregraph.Audio{
		Album:             a.Album,
		AlbumArtist:       a.AlbumArtist,
		Artist:            a.Artist,
		Bitrate:           a.Bitrate,
		Composers:         a.Composers,
		Copyright:         a.Copyright,
		Disc:              a.Disc,
		DiscCount:         a.DiscCount,
		Duration:          a.Duration,
		Genre:             a.Genre,
		HasDrm:            a.HasDrm,
		IsVariableBitrate: a.IsVariableBitrate,
		Title:             a.Title,
		Track:             a.Track,
		TrackCount:        a.TrackCount,
		Year:              a.Year,
	}
	return out
}

func searchImageToLibregraph(i *searchmsg.Image) *libregraph.Image {
	if i == nil {
		return nil
	}
	return &libregraph.Image{Width: i.Width, Height: i.Height}
}

func searchPhotoToLibregraph(p *searchmsg.Photo) *libregraph.Photo {
	if p == nil {
		return nil
	}
	out := &libregraph.Photo{
		CameraMake:          p.CameraMake,
		CameraModel:         p.CameraModel,
		ExposureDenominator: f32ToF64(p.ExposureDenominator),
		ExposureNumerator:   f32ToF64(p.ExposureNumerator),
		FNumber:             f32ToF64(p.FNumber),
		FocalLength:         f32ToF64(p.FocalLength),
		Iso:                 p.Iso,
		Orientation:         p.Orientation,
	}
	if p.TakenDateTime != nil {
		t := time.Unix(p.TakenDateTime.GetSeconds(), int64(p.TakenDateTime.GetNanos())).UTC()
		out.TakenDateTime = &t
	}
	return out
}

func searchLocationToLibregraph(l *searchmsg.GeoCoordinates) *libregraph.GeoCoordinates {
	if l == nil {
		return nil
	}
	return &libregraph.GeoCoordinates{
		Altitude:  l.Altitude,
		Latitude:  l.Latitude,
		Longitude: l.Longitude,
	}
}

func f32ToF64(v *float32) *float64 {
	if v == nil {
		return nil
	}
	f := float64(*v)
	return &f
}
