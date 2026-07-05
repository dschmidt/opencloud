package content

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	gateway "github.com/cs3org/go-cs3apis/cs3/gateway/v1beta1"
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	"github.com/google/go-tika/tika"
	libregraph "github.com/opencloud-eu/libre-graph-api-go"
	"github.com/opencloud-eu/reva/v2/pkg/rgrpc/todo/pool"

	"github.com/opencloud-eu/opencloud/pkg/log"
	"github.com/opencloud-eu/opencloud/services/search/pkg/config"
)

// Tika is used to extract content from a resource,
// it uses apache tika to retrieve all the data.
type Tika struct {
	*Basic
	Retriever
	tika                       *tika.Client
	ContentExtractionSizeLimit uint64
	CleanStopWords             bool
}

// NewTikaExtractor creates a new Tika instance.
func NewTikaExtractor(gatewaySelector pool.Selectable[gateway.GatewayAPIClient], logger log.Logger, cfg *config.Config) (*Tika, error) {
	basic, err := NewBasicExtractor(logger)
	if err != nil {
		return nil, err
	}

	tk := tika.NewClient(nil, cfg.Extractor.Tika.TikaURL)
	tkv, err := tk.Version(context.Background())
	if err != nil {
		return nil, err
	}
	logger.Info().Msgf("Tika version: %s", tkv)

	return &Tika{
		Basic:                      basic,
		Retriever:                  newCS3Retriever(gatewaySelector, logger, cfg.Extractor.CS3AllowInsecure),
		tika:                       tika.NewClient(nil, cfg.Extractor.Tika.TikaURL),
		ContentExtractionSizeLimit: cfg.ContentExtractionSizeLimit,
		CleanStopWords:             cfg.Extractor.Tika.CleanStopWords,
	}, nil
}

// Extract loads a resource from its underlying storage, passes it to tika and processes the result into a Document.
func (t Tika) Extract(ctx context.Context, ri *provider.ResourceInfo) (Document, error) {
	doc, err := t.Basic.Extract(ctx, ri)
	if err != nil {
		return doc, err
	}

	if ri.Size == 0 {
		return doc, nil
	}

	if ri.Size > t.ContentExtractionSizeLimit {
		t.logger.Info().Interface("ResourceID", ri.Id).Str("Name", ri.Name).Msg("file exceeds content extraction size limit. skipping.")
		return doc, nil
	}

	if ri.Type != provider.ResourceType_RESOURCE_TYPE_FILE {
		return doc, nil
	}

	data, err := t.Retrieve(ctx, ri.Id)
	if err != nil {
		return doc, err
	}
	defer data.Close()

	// Motion photos keep their metadata in vendor XMP that stock Tika does not
	// expose (see getMotionPhoto). For images, buffer the head so we can read
	// that XMP ourselves as a fallback; the rest still streams to Tika.
	var xmpHead []byte
	stream := io.Reader(data)
	if strings.HasPrefix(ri.GetMimeType(), "image/") {
		xmpHead = make([]byte, motionPhotoXMPScanLimit)
		n, _ := io.ReadFull(data, xmpHead)
		xmpHead = xmpHead[:n]
		stream = io.MultiReader(bytes.NewReader(xmpHead), data)
	}

	metas, err := t.tika.MetaRecursive(ctx, stream)
	if err != nil {
		return doc, err
	}

	for _, meta := range metas {
		if title, err := getFirstValue(meta, "title"); err == nil {
			doc.Title = strings.TrimSpace(fmt.Sprintf("%s %s", doc.Title, title))
		}

		if content, err := getFirstValue(meta, "X-TIKA:content"); err == nil {
			doc.Content = strings.TrimSpace(fmt.Sprintf("%s %s", doc.Content, content))
		}

		doc.Location = t.getLocation(meta)
		doc.Image = t.getImage(meta)
		doc.Photo = t.getPhoto(meta)
		doc.MotionPhoto = t.getMotionPhoto(meta)

		if contentType, err := getFirstValue(meta, "Content-Type"); err == nil && strings.HasPrefix(contentType, "audio/") {
			doc.Audio = t.getAudio(meta)
		}
	}

	// Tika did not surface the motion photo XMP: read it from the buffered head.
	if doc.MotionPhoto == nil && xmpHead != nil {
		doc.MotionPhoto = motionPhotoFromXMP(xmpHead)
	}

	if langCode, _ := t.tika.LanguageString(ctx, doc.Content); langCode != "" && t.CleanStopWords {
		doc.Content = CleanString(doc.Content, langCode)
	}

	return doc, nil
}

func (t Tika) getImage(meta map[string][]string) *libregraph.Image {
	var image *libregraph.Image
	initImage := func() {
		if image == nil {
			image = libregraph.NewImage()
		}
	}

	if v, err := getFirstValue(meta, "tiff:ImageWidth"); err == nil {
		if i, err := strconv.ParseInt(v, 0, 32); err == nil {
			initImage()
			image.SetWidth(int32(i))
		}
	}

	if v, err := getFirstValue(meta, "tiff:ImageLength"); err == nil {
		if i, err := strconv.ParseInt(v, 0, 32); err == nil {
			initImage()
			image.SetHeight(int32(i))
		}
	}

	return image
}

func (t Tika) getLocation(meta map[string][]string) *libregraph.GeoCoordinates {
	var location *libregraph.GeoCoordinates
	initLocation := func() {
		if location == nil {
			location = libregraph.NewGeoCoordinates()
		}
	}

	// TODO: location.Altitute: transform the following data to … feet above sea level.
	// "GPS:GPS Altitude":                          []string{"227.4 metres"},
	// "GPS:GPS Altitude Ref":                      []string{"Sea level"},

	if v, err := getFirstValue(meta, "geo:lat"); err == nil {
		if i, err := strconv.ParseFloat(v, 64); err == nil {
			initLocation()
			location.SetLatitude(i)
		}
	}

	if v, err := getFirstValue(meta, "geo:long"); err == nil {
		if i, err := strconv.ParseFloat(v, 64); err == nil {
			initLocation()
			location.SetLongitude(i)
		}
	}

	return location
}

func (t Tika) getPhoto(meta map[string][]string) *libregraph.Photo {
	var photo *libregraph.Photo
	initPhoto := func() {
		if photo == nil {
			photo = libregraph.NewPhoto()
		}
	}

	if v, err := getFirstValue(meta, "tiff:Make"); err == nil {
		initPhoto()
		photo.SetCameraMake(v)
	}

	if v, err := getFirstValue(meta, "tiff:Model"); err == nil {
		initPhoto()
		photo.SetCameraModel(v)
	}

	if v, err := getFirstValue(meta, "exif:FNumber"); err == nil {
		if i, err := strconv.ParseFloat(v, 64); err == nil {
			initPhoto()
			photo.SetFNumber(i)
		}
	}

	if v, err := getFirstValue(meta, "exif:FocalLength"); err == nil {
		if i, err := strconv.ParseFloat(v, 64); err == nil {
			initPhoto()
			photo.SetFocalLength(i)
		}
	}

	if v, err := getFirstValue(meta, "Base ISO"); err == nil {
		if i, err := strconv.ParseInt(v, 0, 32); err == nil {
			initPhoto()
			photo.SetIso(int32(i))
		}
	}

	if v, err := getFirstValue(meta, "tiff:Orientation"); err == nil {
		if i, err := strconv.ParseInt(v, 0, 32); err == nil {
			initPhoto()
			photo.SetOrientation(int32(i))
		}
	}

	if v, err := getFirstValue(meta, "exif:DateTimeOriginal"); err == nil {
		layout := "2006-01-02T15:04:05"
		if t, err := time.Parse(layout, v); err == nil {
			initPhoto()
			photo.SetTakenDateTime(t)
		}
	}

	if v, err := getFirstValue(meta, "exif:ExposureTime"); err == nil {
		if i, err := strconv.ParseFloat(v, 64); err == nil {
			initPhoto()
			photo.SetExposureNumerator(1)
			photo.SetExposureDenominator(math.Round(1 / i))
		}
	}

	return photo
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

const motionPhotoXMPScanLimit = 256 * 1024

// motionPhotoFromXMP parses Google Motion Photo metadata directly from a file's
// XMP packet, covering the current MotionPhoto scheme and the legacy MicroVideo
// scheme. It matches on namespace URIs, so the declared prefix does not matter.
// videoSize is required, so the facet is dropped without it.
func motionPhotoFromXMP(data []byte) *libregraph.MotionPhoto {
	const cameraNS = "http://ns.google.com/photos/1.0/camera/"
	const itemNS = "http://ns.google.com/photos/1.0/container/item/"

	packet := extractXMPPacket(data)
	if packet == nil {
		return nil
	}

	var motionPhoto *libregraph.MotionPhoto
	initMotionPhoto := func() {
		if motionPhoto == nil {
			motionPhoto = libregraph.NewMotionPhoto()
		}
	}

	decoder := xml.NewDecoder(bytes.NewReader(packet))
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		element, ok := token.(xml.StartElement)
		if !ok {
			continue
		}

		var itemSemantic, itemLength string
		for _, attr := range element.Attr {
			switch {
			case attr.Name.Space == cameraNS && (attr.Name.Local == "MotionPhotoVersion" || attr.Name.Local == "MicroVideoVersion"):
				if i, err := strconv.ParseInt(attr.Value, 10, 32); err == nil {
					initMotionPhoto()
					motionPhoto.SetVersion(int32(i))
				}
			case attr.Name.Space == cameraNS && (attr.Name.Local == "MotionPhotoPresentationTimestampUs" || attr.Name.Local == "MicroVideoPresentationTimestampUs"):
				if i, err := strconv.ParseInt(attr.Value, 10, 64); err == nil {
					initMotionPhoto()
					motionPhoto.SetPresentationTimestampUs(i)
				}
			case attr.Name.Space == cameraNS && attr.Name.Local == "MicroVideoOffset":
				if i, err := strconv.ParseInt(attr.Value, 10, 64); err == nil {
					initMotionPhoto()
					motionPhoto.SetVideoSize(i)
				}
			case attr.Name.Space == itemNS && attr.Name.Local == "Semantic":
				itemSemantic = attr.Value
			case attr.Name.Space == itemNS && attr.Name.Local == "Length":
				itemLength = attr.Value
			}
		}
		// current format: the video is the container item whose semantic is MotionPhoto.
		if itemSemantic == "MotionPhoto" && itemLength != "" {
			if i, err := strconv.ParseInt(itemLength, 10, 64); err == nil {
				initMotionPhoto()
				motionPhoto.SetVideoSize(i)
			}
		}
	}

	if motionPhoto == nil || !motionPhoto.HasVideoSize() {
		return nil
	}
	return motionPhoto
}

// extractXMPPacket returns the <x:xmpmeta> element from a file's leading bytes,
// or nil when none is present.
func extractXMPPacket(data []byte) []byte {
	start := bytes.Index(data, []byte("<x:xmpmeta"))
	if start < 0 {
		return nil
	}
	end := bytes.Index(data[start:], []byte("</x:xmpmeta>"))
	if end < 0 {
		return nil
	}
	return data[start : start+end+len("</x:xmpmeta>")]
}

// firstValue returns the first metadata value present among keys.
func firstValue(meta map[string][]string, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, err := getFirstValue(meta, k); err == nil {
			return v, true
		}
	}
	return "", false
}

func (t Tika) getAudio(meta map[string][]string) *libregraph.Audio {
	var audio *libregraph.Audio
	initAudio := func() {
		if audio == nil {
			audio = libregraph.NewAudio()
		}
	}

	if v, err := getFirstValue(meta, "xmpDM:album"); err == nil {
		initAudio()
		audio.SetAlbum(v)
	}

	if v, err := getFirstValue(meta, "xmpDM:albumArtist"); err == nil {
		initAudio()
		audio.SetAlbumArtist(v)
	}

	if v, err := getFirstValue(meta, "xmpDM:artist"); err == nil {
		initAudio()
		audio.SetArtist(v)
	}

	// TODO: audio.Bitrate: not provided by tika
	// TODO: audio.Composers: not provided by tika
	// TODO: audio.Copyright: not provided by tika for audio files?

	if v, err := getFirstValue(meta, "xmpDM:discNumber"); err == nil {
		if i, err := strconv.ParseInt(v, 10, 32); err == nil {
			initAudio()
			audio.SetDisc(int32(i))
		}

	}

	//  TODO: audio.DiscCount: not provided by tika

	if v, err := getFirstValue(meta, "xmpDM:duration"); err == nil {
		// Tika emits fractional seconds.
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			initAudio()
			audio.SetDuration(int64(math.Round(f * 1000)))
		}
	}

	if v, err := getFirstValue(meta, "xmpDM:genre"); err == nil {
		initAudio()
		audio.SetGenre(v)
	}

	// TODO: audio.HasDrm: not provided by tika
	// TODO: audio.IsVariableBitrate: not provided by tika

	if v, err := getFirstValue(meta, "dc:title"); err == nil {
		initAudio()
		audio.SetTitle(v)
	}

	if v, err := getFirstValue(meta, "xmpDM:trackNumber"); err == nil {
		if i, err := strconv.ParseInt(v, 10, 32); err == nil {
			initAudio()
			audio.SetTrack(int32(i))
		}
	}

	// TODO: audio.TrackCount: not provided by tika

	if v, err := getFirstValue(meta, "xmpDM:releaseDate"); err == nil {
		if i, err := strconv.ParseInt(v, 10, 32); err == nil {
			initAudio()
			audio.SetYear(int32(i))
		}
	}

	return audio
}
