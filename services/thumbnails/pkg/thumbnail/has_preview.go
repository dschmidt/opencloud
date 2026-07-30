package thumbnail

import (
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"

	"github.com/opencloud-eu/opencloud/pkg/conversions"
)

// Arbitrary-metadata keys for an embedded preview's dimensions, written at index
// time; their presence signals a preview exists.
const (
	PreviewWidthKey  = "oc.preview.width"
	PreviewHeightKey = "oc.preview.height"
)

// HasPreview reports whether a preview can be produced for the resource.
func HasPreview(md *provider.ResourceInfo) bool {
	w, h := PreviewDimensions(md)
	return HasPreviewForMimeType(md.GetMimeType(), w > 0 && h > 0)
}

// HasPreviewForMimeType is HasPreview for callers with only the mimetype and a
// presence signal (e.g. search results).
func HasPreviewForMimeType(mimeType string, hasEmbeddedPreview bool) bool {
	if _, ok := UnconditionalPreviewMimeTypes[mimeType]; ok {
		return true
	}
	_, embedded := EmbeddedPreviewMimeTypes[mimeType]
	return embedded && hasEmbeddedPreview
}

// PreviewDimensions returns the stored embedded-preview dimensions, or (0, 0).
func PreviewDimensions(md *provider.ResourceInfo) (width, height int32) {
	meta := md.GetArbitraryMetadata().GetMetadata()
	return conversions.StringToInt32(meta[PreviewWidthKey]), conversions.StringToInt32(meta[PreviewHeightKey])
}
