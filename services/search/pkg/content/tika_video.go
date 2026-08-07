package content

import (
	"math"
	"strconv"
	"strings"

	libregraph "github.com/opencloud-eu/libre-graph-api-go"
)

func (t Tika) getVideo(meta map[string][]string) *libregraph.Video {
	// the tiff:/xmpDM: keys below are shared with images and audio; the file's
	// content type is what tells a video apart.
	if ct, err := getFirstValue(meta, "Content-Type"); err != nil || !strings.HasPrefix(ct, "video/") {
		return nil
	}

	var video *libregraph.Video
	initVideo := func() {
		if video == nil {
			video = libregraph.NewVideo()
		}
	}

	if v, err := getFirstValue(meta, "tiff:ImageWidth"); err == nil {
		if i, err := strconv.ParseInt(v, 0, 32); err == nil {
			initVideo()
			video.SetWidth(int32(i))
		}
	}

	if v, err := getFirstValue(meta, "tiff:ImageLength"); err == nil {
		if i, err := strconv.ParseInt(v, 0, 32); err == nil {
			initVideo()
			video.SetHeight(int32(i))
		}
	}

	if v, err := getFirstValue(meta, "xmpDM:duration"); err == nil {
		// Tika emits fractional seconds.
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			initVideo()
			video.SetDuration(int64(math.Round(f * 1000)))
		}
	}

	if v, err := getFirstValue(meta, "xmpDM:videoCompressor"); err == nil {
		initVideo()
		video.SetFourCC(v)
	}

	// TODO: video.Bitrate: not provided by tika (no bitrate tag; needs computing)
	// TODO: video.FrameRate: not provided by tika (metadata-extractor exposes no frame-rate tag)

	if v, err := getFirstValue(meta, "xmpDM:audioSampleRate"); err == nil {
		if i, err := strconv.ParseInt(v, 0, 32); err == nil {
			initVideo()
			video.SetAudioSamplesPerSecond(int32(i))
		}
	}

	if c, ok := audioChannelCount(meta); ok {
		initVideo()
		video.SetAudioChannels(c)
	}

	// TODO: video.AudioBitsPerSample: not provided by tika (Mp4SoundDirectory.TAG_AUDIO_SAMPLE_SIZE is unmapped)
	// TODO: video.AudioFormat: tika only sets xmpDM:audioCompressor for audio-typed
	// files (and to the container major brand, not the codec), so it never fires for
	// a video/* file. Needs tika to expose the audio track's codec.

	return video
}

// audioChannelCount maps tika's xmpDM:audioChannelType enum to a channel count.
// A numeric channel count from tika would drop this mapping and cover >2 channels.
func audioChannelCount(meta map[string][]string) (int32, bool) {
	v, err := getFirstValue(meta, "xmpDM:audioChannelType")
	if err != nil {
		return 0, false
	}
	switch v {
	case "Mono":
		return 1, true
	case "Stereo":
		return 2, true
	case "5.1":
		return 6, true
	case "7.1":
		return 8, true
	}
	return 0, false
}
