package svc

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	libregraph "github.com/opencloud-eu/libre-graph-api-go"
	"github.com/opencloud-eu/reva/v2/pkg/storagespace"

	"github.com/opencloud-eu/opencloud/services/thumbnails/pkg/thumbnail"
)

// thumbnailsExpanded reports whether the request asked for the thumbnails
// relationship to be expanded ($expand=thumbnails).
func thumbnailsExpanded(r *http.Request) bool {
	return strings.Contains(r.URL.Query().Get("$expand"), "thumbnails")
}

// setDriveItemsThumbnails populates the thumbnails relationship on driveItems
// (1:1 with infos) when the request asked for it. Resource infos carry the
// mimetype and the arbitrary metadata (image / oc.preview dimensions) that drive
// the decision, so no extra stat is needed.
func (g Graph) setDriveItemsThumbnails(r *http.Request, items []*libregraph.DriveItem, infos []*provider.ResourceInfo) {
	if !thumbnailsExpanded(r) {
		return
	}
	for i := range items {
		if i < len(infos) {
			setDriveItemThumbnails(items[i], infos[i], g.config.Commons.OpenCloudURL)
		}
	}
}

// Bounding boxes for the driveItem thumbnails relationship. The thumbnails
// endpoint scales the source to fit these preserving aspect ratio (scalingup=0).
//
// Note: the endpoint rounds a requested box up to the nearest configured
// THUMBNAILS_RESOLUTIONS via ClosestMatch, so the reported dimensions are
// aspect-correct for the requested box but not necessarily the exact served
// resolution. Making them exact would require the resolution list to be
// available in the graph service; this is a possible future refinement and is
// no worse than the previous behaviour (which reported no dimensions at all).
const (
	thumbnailBoxSmall  = 36
	thumbnailBoxMedium = 48
	thumbnailBoxLarge  = 96
)

// setDriveItemThumbnails populates the thumbnails relationship of a driveItem
// from its resource info when a preview is available. It is a no-op when no
// preview exists (for example audio without embedded cover art), which keeps the
// relationship honest instead of advertising a preview that fails to render.
func setDriveItemThumbnails(item *libregraph.DriveItem, res *provider.ResourceInfo, baseURL string) {
	if set := previewThumbnailSet(res, baseURL); set != nil {
		item.SetThumbnails([]libregraph.ThumbnailSet{*set})
	}
}

// previewThumbnailSet builds the thumbnails relationship entry for a resource,
// or nil when no preview is available. When the source dimensions are known the
// reported thumbnail dimensions are aspect-correct rather than square, and a
// source (original) thumbnail carrying the native dimensions is included.
func previewThumbnailSet(res *provider.ResourceInfo, baseURL string) *libregraph.ThumbnailSet {
	if !thumbnail.HasPreview(res) {
		return nil
	}

	base := fmt.Sprintf("%s/dav/spaces/%s?scalingup=0&preview=1&processor=thumbnail",
		baseURL, storagespace.FormatResourceID(res.GetId()))
	srcW, srcH := previewSourceDimensions(res)

	set := &libregraph.ThumbnailSet{
		Small:  previewThumbnail(base, thumbnailBoxSmall, srcW, srcH),
		Medium: previewThumbnail(base, thumbnailBoxMedium, srcW, srcH),
		Large:  previewThumbnail(base, thumbnailBoxLarge, srcW, srcH),
	}
	if srcW > 0 && srcH > 0 {
		url := fmt.Sprintf("%s&x=%d&y=%d", base, srcW, srcH)
		set.Source = &libregraph.Thumbnail{Url: &url, Width: &srcW, Height: &srcH}
	}
	return set
}

func previewThumbnail(base string, box, srcW, srcH int32) *libregraph.Thumbnail {
	url := fmt.Sprintf("%s&x=%d&y=%d", base, box, box)
	t := &libregraph.Thumbnail{Url: &url}
	if srcW > 0 && srcH > 0 {
		w, h := fitBox(srcW, srcH, box)
		t.Width, t.Height = &w, &h
	}
	return t
}

// previewSourceDimensions returns the native dimensions of a resource's preview:
// the embedded cover dimensions for audio (from oc.preview), or the image facet
// dimensions for images. Zero when unknown.
func previewSourceDimensions(res *provider.ResourceInfo) (int32, int32) {
	if w, h := thumbnail.PreviewDimensions(res); w > 0 && h > 0 {
		return w, h
	}
	meta := res.GetArbitraryMetadata().GetMetadata()
	return parsePreviewInt(meta["libre.graph.image.width"]), parsePreviewInt(meta["libre.graph.image.height"])
}

// fitBox scales (w, h) to fit within a box×box square preserving aspect ratio,
// never upscaling, matching the thumbnails endpoint (scalingup=0).
func fitBox(w, h, box int32) (int32, int32) {
	if w <= box && h <= box {
		return w, h
	}
	scale := math.Min(float64(box)/float64(w), float64(box)/float64(h))
	rw := int32(math.Round(float64(w) * scale))
	rh := int32(math.Round(float64(h) * scale))
	if rw < 1 {
		rw = 1
	}
	if rh < 1 {
		rh = 1
	}
	return rw, rh
}

func parsePreviewInt(s string) int32 {
	v, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0
	}
	return int32(v)
}
