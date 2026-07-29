package content

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	libregraph "github.com/opencloud-eu/libre-graph-api-go"
)

var _ = Describe("getMotionPhoto", func() {
	It("maps the current MotionPhoto XMP scheme (container item length)", func() {
		mp := Tika{}.getMotionPhoto(map[string][]string{
			"Camera:MotionPhotoVersion":                 {"1"},
			"Camera:MotionPhotoPresentationTimestampUs": {"1500000"},
			"Container:Directory/Item[2]/Item:Semantic": {"MotionPhoto"},
			"Container:Directory/Item[2]/Item:Length":   {"1048576"},
		})
		Expect(mp).ToNot(BeNil())
		Expect(mp.Version).To(Equal(libregraph.PtrInt32(1)))
		Expect(mp.PresentationTimestampUs).To(Equal(libregraph.PtrInt64(1500000)))
		Expect(mp.VideoSize).To(Equal(libregraph.PtrInt64(1048576)))
	})

	It("maps the legacy MicroVideo XMP scheme (offset is the length)", func() {
		mp := Tika{}.getMotionPhoto(map[string][]string{
			"Camera:MicroVideoVersion":                 {"1"},
			"Camera:MicroVideoPresentationTimestampUs": {"1500000"},
			"Camera:MicroVideoOffset":                  {"2097152"},
		})
		Expect(mp).ToNot(BeNil())
		Expect(mp.Version).To(Equal(libregraph.PtrInt32(1)))
		Expect(mp.PresentationTimestampUs).To(Equal(libregraph.PtrInt64(1500000)))
		Expect(mp.VideoSize).To(Equal(libregraph.PtrInt64(2097152)))
	})

	It("drops the facet without a video size", func() {
		Expect(Tika{}.getMotionPhoto(map[string][]string{
			"Camera:MotionPhotoVersion": {"1"},
		})).To(BeNil())
	})

	It("returns nil when no motion photo metadata is present", func() {
		Expect(Tika{}.getMotionPhoto(map[string][]string{})).To(BeNil())
	})
})
