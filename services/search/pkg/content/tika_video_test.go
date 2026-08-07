package content

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	libregraph "github.com/opencloud-eu/libre-graph-api-go"
)

var _ = Describe("getVideo", func() {
	It("maps the video metadata to the video facet", func() {
		video := Tika{}.getVideo(map[string][]string{
			"Content-Type":           {"video/mp4"},
			"xmpDM:videoCompressor":  {"avc1"},
			"tiff:ImageWidth":        {"1920"},
			"tiff:ImageLength":       {"1080"},
			"xmpDM:duration":         {"12.5"},
			"xmpDM:audioSampleRate":  {"48000"},
			"xmpDM:audioChannelType": {"Stereo"},
		})
		Expect(video).ToNot(BeNil())

		Expect(video.Width).To(Equal(libregraph.PtrInt32(1920)))
		Expect(video.Height).To(Equal(libregraph.PtrInt32(1080)))
		Expect(video.Duration).To(Equal(libregraph.PtrInt64(12500)))
		Expect(video.FourCC).To(Equal(libregraph.PtrString("avc1")))
		Expect(video.AudioSamplesPerSecond).To(Equal(libregraph.PtrInt32(48000)))
		Expect(video.AudioChannels).To(Equal(libregraph.PtrInt32(2)))
		// not provided by tika for video/* files yet, see the TODOs in tika_video.go:
		// Expect(video.Bitrate).To(Equal(libregraph.PtrInt32(...)))
		// Expect(video.FrameRate).To(Equal(libregraph.PtrFloat64(...)))
		// Expect(video.AudioBitsPerSample).To(Equal(libregraph.PtrInt32(16)))
		// Expect(video.AudioFormat).To(Equal(libregraph.PtrString(...)))
	})

	It("returns nil for a non-video file (image content type)", func() {
		Expect(Tika{}.getVideo(map[string][]string{
			"Content-Type":     {"image/jpeg"},
			"tiff:ImageWidth":  {"1920"},
			"tiff:ImageLength": {"1080"},
		})).To(BeNil())
	})
})
