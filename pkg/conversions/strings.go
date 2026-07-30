package conversions

import (
	"strconv"
	"strings"
)

// StringToSliceString splits a string into a slice string according to separator
func StringToSliceString(src string, sep string) []string {
	parsed := strings.Split(src, sep)
	parts := make([]string, 0, len(parsed))
	for _, v := range parsed {
		parts = append(parts, strings.TrimSpace(v))
	}

	return parts
}

// StringToInt32 parses s as a base-10 int32, returning 0 for empty or invalid input.
func StringToInt32(s string) int32 {
	v, _ := strconv.ParseInt(s, 10, 32)
	return int32(v)
}
