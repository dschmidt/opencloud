package svc

import (
	"fmt"
	"net/http"
	"strings"

	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	libregraph "github.com/opencloud-eu/libre-graph-api-go"
	"github.com/opencloud-eu/reva/v2/pkg/storagespace"

	"github.com/opencloud-eu/opencloud/pkg/conversions"
	"github.com/opencloud-eu/opencloud/services/thumbnails/pkg/thumbnail"
)

func shouldExpand(r *http.Request, relationship string) bool {
	return strings.Contains(r.URL.Query().Get("$expand"), relationship)
}

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

// Requested box sizes for the thumbnail URLs. Small/medium/large carry no
// dimensions: the endpoint rounds the box to a configured resolution and fits
// within it, so the served size is not known here. Only the source dimensions
// (below) are exact.
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

func previewThumbnailSet(res *provider.ResourceInfo, baseURL string) *libregraph.ThumbnailSet {
	if !thumbnail.HasPreview(res) {
		return nil
	}
	w, h := previewSourceDimensions(res)
	return buildThumbnailSet(previewBaseURL(baseURL, storagespace.FormatResourceID(res.GetId())), w, h)
}

func previewBaseURL(baseURL, resourceID string) string {
	return fmt.Sprintf("%s/dav/spaces/%s?scalingup=0&preview=1&processor=thumbnail", baseURL, resourceID)
}

// buildThumbnailSet assembles the small/medium/large thumbnails plus, when the
// source dimensions are known, the exact-size source thumbnail.
func buildThumbnailSet(base string, w, h int32) *libregraph.ThumbnailSet {
	set := &libregraph.ThumbnailSet{
		Small:  previewThumbnail(base, thumbnailBoxSmall),
		Medium: previewThumbnail(base, thumbnailBoxMedium),
		Large:  previewThumbnail(base, thumbnailBoxLarge),
	}
	if w > 0 && h > 0 {
		url := fmt.Sprintf("%s&x=%d&y=%d", base, w, h)
		set.Source = &libregraph.Thumbnail{Url: &url, Width: &w, Height: &h}
	}
	return set
}

func previewThumbnail(base string, box int32) *libregraph.Thumbnail {
	url := fmt.Sprintf("%s&x=%d&y=%d", base, box, box)
	return &libregraph.Thumbnail{Url: &url}
}

// previewSourceDimensions: audio cover from oc.preview, images from the image facet.
func previewSourceDimensions(res *provider.ResourceInfo) (int32, int32) {
	if w, h := thumbnail.PreviewDimensions(res); w > 0 && h > 0 {
		return w, h
	}
	meta := res.GetArbitraryMetadata().GetMetadata()
	w := conversions.StringToInt32(meta["libre.graph.image.width"], 0)
	h := conversions.StringToInt32(meta["libre.graph.image.height"], 0)
	return w, h
}
