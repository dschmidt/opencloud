package svc

import (
	"fmt"
	"math"
	"net/http"
	"strings"

	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	libregraph "github.com/opencloud-eu/libre-graph-api-go"
	"github.com/opencloud-eu/reva/v2/pkg/storagespace"

	"github.com/opencloud-eu/opencloud/pkg/conversions"
	"github.com/opencloud-eu/opencloud/services/thumbnails/pkg/thumbnail"
)

func shouldExpand(r *http.Request, relation string) bool {
	return strings.Contains(r.URL.Query().Get("$expand"), relation)
}

// setDriveItemsThumbnails expands the thumbnails relationship on driveItems that
// are 1:1 with infos.
func (g Graph) setDriveItemsThumbnails(r *http.Request, items []*libregraph.DriveItem, infos []*provider.ResourceInfo) {
	if !shouldExpand(r, "thumbnails") {
		return
	}
	for i := range items {
		if i < len(infos) {
			setDriveItemThumbnails(items[i], infos[i], g.config.Commons.OpenCloudURL)
		}
	}
}

// Requested box sizes. The reported dimensions are aspect-correct for the box,
// not the ClosestMatch-served resolution (that would need THUMBNAILS_RESOLUTIONS
// here; follow-up).
const (
	thumbnailBoxSmall  = 36
	thumbnailBoxMedium = 48
	thumbnailBoxLarge  = 96
)

func setDriveItemThumbnails(item *libregraph.DriveItem, res *provider.ResourceInfo, baseURL string) {
	if set := previewThumbnailSet(res, baseURL); set != nil {
		item.SetThumbnails([]libregraph.ThumbnailSet{*set})
	}
}

// previewThumbnailSet returns nil when no preview is available. Dimensions are
// aspect-correct when the source size is known, plus a source (original) entry.
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

// previewSourceDimensions: audio cover from oc.preview, images from the image
// facet. Zero when unknown.
func previewSourceDimensions(res *provider.ResourceInfo) (int32, int32) {
	if w, h := thumbnail.PreviewDimensions(res); w > 0 && h > 0 {
		return w, h
	}
	meta := res.GetArbitraryMetadata().GetMetadata()
	w, _ := conversions.StringToInt32(meta["libre.graph.image.width"])
	h, _ := conversions.StringToInt32(meta["libre.graph.image.height"])
	return w, h
}

// fitBox scales (w, h) into a box×box square, preserving aspect and never upscaling.
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
