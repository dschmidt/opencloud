package search

import "testing"

func TestIsNumericField(t *testing.T) {
	cases := []struct {
		field   string
		numeric bool
	}{
		// Top-level numeric fields on Resource / Document.
		{"Size", true}, // uint64 via embedded Document
		{"Type", true}, // uint64 on Resource
		// Top-level string fields.
		{"Name", false},
		{"Path", false},
		{"MimeType", false},
		// Nested audio.
		{"audio.artist", false},
		{"audio.album", false},
		{"audio.year", true},
		{"audio.bitrate", true},
		{"audio.track", true},
		{"audio.hasDrm", false}, // bool, not numeric
		// Nested image.
		{"image.width", true},
		{"image.height", true},
		// Nested photo.
		{"photo.cameraMake", false},
		{"photo.iso", true},
		{"photo.focalLength", true},        // float32
		{"photo.exposureDenominator", true}, // float32
		{"photo.takenDateTime", true},      // time.Time — treated as numeric
		// Nested location.
		{"location.altitude", true},
		{"location.latitude", true},
		{"location.longitude", true},
		// Completely unknown field — caller may still aggregate on it.
		{"nonexistent", false},
		{"audio.nonexistent", false},
	}
	for _, c := range cases {
		if got := IsNumericField(c.field); got != c.numeric {
			t.Errorf("IsNumericField(%q) = %v, want %v", c.field, got, c.numeric)
		}
	}
}
