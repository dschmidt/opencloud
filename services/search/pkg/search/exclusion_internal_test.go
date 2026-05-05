package search

import (
	"reflect"
	"sort"
	"testing"
)

func TestMarkerParentDir(t *testing.T) {
	cases := map[string]string{
		"":                              "",
		"./.nomedia":                    ".",
		"./photos/.nomedia":             "./photos",
		"./photos/private/.nomedia":     "./photos/private",
		"./a/b/c/d/.nomedia":            "./a/b/c/d",
		"./weird name with spaces/.no": "./weird name with spaces", // markerName here is just used for path
	}
	for in, want := range cases {
		if got := markerParentDir(in); got != want {
			t.Errorf("markerParentDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConsolidatePrefixes(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, nil},
		{"single", []string{"./a"}, []string{"./a"}},
		{
			"drops nested descendant",
			[]string{"./a", "./a/b"},
			[]string{"./a"},
		},
		{
			"keeps siblings",
			[]string{"./a", "./b"},
			[]string{"./a", "./b"},
		},
		{
			"does not confuse prefix names",
			[]string{"./a", "./ab"},
			[]string{"./a", "./ab"},
		},
		{
			"root swallows everything",
			[]string{"./a/b", ".", "./c"},
			[]string{"."},
		},
		{
			"deep duplicates collapse to shallowest",
			[]string{"./a/b/c", "./a/b", "./a"},
			[]string{"./a"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := consolidatePrefixes(append([]string{}, tc.in...))
			// consolidate sorts by length; normalize for comparison
			sort.Strings(got)
			want := append([]string{}, tc.want...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}
}
