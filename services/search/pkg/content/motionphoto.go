package content

import (
	"context"
	"io"
	"strconv"
	"strings"

	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	libregraph "github.com/opencloud-eu/libre-graph-api-go"
)

// motionPhotoVideoSignatureLen is the number of trailing bytes we read to confirm
// an actual video is present. Enough to cover the ISO base media (MP4) box size
// and the "ftyp" box type at bytes [4:8].
const motionPhotoVideoSignatureLen = 12

// firstValue returns the first metadata value present among keys.
func firstValue(meta map[string][]string, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, err := getFirstValue(meta, k); err == nil {
			return v, true
		}
	}
	return "", false
}

// getMotionPhoto reads Google Motion Photo XMP, which Tika exposes under the
// canonical Camera/Container prefixes. It covers both the current MotionPhoto
// scheme and the legacy MicroVideo scheme. videoSize (the embedded video's byte
// length, needed to range-fetch it) is required, so the facet is dropped without it.
func (t Tika) getMotionPhoto(meta map[string][]string) *libregraph.MotionPhoto {
	var motionPhoto *libregraph.MotionPhoto
	initMotionPhoto := func() {
		if motionPhoto == nil {
			motionPhoto = libregraph.NewMotionPhoto()
		}
	}

	if v, ok := firstValue(meta, "Camera:MotionPhotoVersion", "Camera:MicroVideoVersion"); ok {
		if i, err := strconv.ParseInt(v, 0, 32); err == nil {
			initMotionPhoto()
			motionPhoto.SetVersion(int32(i))
		}
	}

	if v, ok := firstValue(meta, "Camera:MotionPhotoPresentationTimestampUs", "Camera:MicroVideoPresentationTimestampUs"); ok {
		if i, err := strconv.ParseInt(v, 0, 64); err == nil {
			initMotionPhoto()
			motionPhoto.SetPresentationTimestampUs(i)
		}
	}

	if size, ok := motionPhotoVideoSize(meta); ok {
		initMotionPhoto()
		motionPhoto.SetVideoSize(size)
	}

	if motionPhoto == nil || !motionPhoto.HasVideoSize() {
		return nil
	}
	return motionPhoto
}

// motionPhotoVideoSize returns the embedded video's byte length. Legacy files
// carry it as the MicroVideo offset (bytes from EOF to the video start, which
// equals its length); current files expose it as the length of the Container
// item whose semantic is "MotionPhoto".
func motionPhotoVideoSize(meta map[string][]string) (int64, bool) {
	if v, ok := firstValue(meta, "Camera:MicroVideoOffset"); ok {
		if i, err := strconv.ParseInt(v, 0, 64); err == nil {
			return i, true
		}
	}
	for k, vals := range meta {
		if !strings.HasSuffix(k, "/Item:Semantic") || len(vals) == 0 || vals[0] != "MotionPhoto" {
			continue
		}
		if v, ok := firstValue(meta, strings.TrimSuffix(k, "/Item:Semantic")+"/Item:Length"); ok {
			if i, err := strconv.ParseInt(v, 0, 64); err == nil {
				return i, true
			}
		}
	}
	return 0, false
}

// looksLikeMP4 reports whether buf begins with an ISO base media (MP4/QuickTime)
// "ftyp" box, which Google Motion Photo and legacy MicroVideo clips start with.
func looksLikeMP4(buf []byte) bool {
	return len(buf) >= 8 && string(buf[4:8]) == "ftyp"
}

// motionPhotoHasVideo confirms that the file actually contains the embedded video
// the XMP advertises. A photos.google.com share strips the appended video but
// keeps the XMP, which would otherwise make us expose an unplayable facet. The
// video is appended at the end, so it starts at fileSize-videoSize; we read a few
// bytes there and require an MP4 signature.
func (t Tika) motionPhotoHasVideo(ctx context.Context, ri *provider.ResourceInfo, videoSize int64) bool {
	size := int64(ri.GetSize())
	if videoSize <= 0 || videoSize >= size {
		return false
	}

	rc, err := t.RetrieveRange(ctx, ri.GetId(), size-videoSize, motionPhotoVideoSignatureLen)
	if err != nil {
		t.logger.Debug().Err(err).Interface("ResourceID", ri.GetId()).Msg("could not read motion photo video header, dropping facet")
		return false
	}
	defer rc.Close()

	buf := make([]byte, motionPhotoVideoSignatureLen)
	n, _ := io.ReadFull(rc, buf)
	return looksLikeMP4(buf[:n])
}
