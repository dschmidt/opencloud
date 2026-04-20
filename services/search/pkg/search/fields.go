package search

import (
	"reflect"
	"strings"
	"time"
)

// IsNumericField reports whether the indexed field at the given dotted path
// holds a numeric or time-typed value.
//
// Callers use this to reject terms aggregations on numeric fields. Bleve
// stores numbers as prefix-coded binary at multiple shift levels, so a
// terms facet on a numeric field produces many buckets per real value and
// binary keys — useless in that backend. Clients should use range
// aggregations instead, which work consistently across backends.
//
// The lookup table is derived at package init by walking the Resource type
// (embedded Document plus its nested Audio/Image/Photo/Location facets), so
// adding a new numeric field anywhere along that tree picks it up without
// any additional maintenance.
func IsNumericField(dottedPath string) bool {
	return numericFields[dottedPath]
}

var numericFields = buildNumericFieldSet()

var timeType = reflect.TypeOf(time.Time{})

func buildNumericFieldSet() map[string]bool {
	out := map[string]bool{}
	walkStruct(out, "", reflect.TypeOf(Resource{}))
	return out
}

func walkStruct(out map[string]bool, prefix string, t reflect.Type) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if f.Anonymous {
			// embedded — promote its fields into the current prefix, just like
			// encoding/json would.
			walkStruct(out, prefix, f.Type)
			continue
		}
		path := prefix + jsonFieldName(f)
		ft := f.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		switch ft.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
			out[path] = true
		case reflect.Struct:
			if ft == timeType {
				// time.Time values round-trip as RFC3339 strings; terms
				// aggregation on one makes as little sense as on a number.
				out[path] = true
				continue
			}
			walkStruct(out, path+".", ft)
		}
	}
}

func jsonFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name
	}
	return strings.Split(tag, ",")[0]
}
