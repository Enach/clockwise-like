package api

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	"github.com/Enach/paceday/backend/storage"
)

var jsonUnmarshalerType = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()

// settingsDecodeProblem is the 400 message for a PATCH /api/settings body that
// parsed as a JSON object but did not decode onto the settings record: a value
// of the wrong JSON type. It names the dotted path of JSON property names from
// the request, e.g. `workingHours.days.monday.start`, never a Go type or field
// name — the decoder's own text names both (PAC-21; PAC-55 promised that a
// non-string timeFormat is a 400 naming timeFormat). When no property can be
// pinned it is the fixed `invalid JSON`.
func settingsDecodeProblem(fields map[string]json.RawMessage) string {
	if path := jsonWrongTypePath(reflect.TypeOf(storage.Settings{}), fields, ""); path != "" {
		return "invalid JSON: " + path + " has the wrong type"
	}
	return "invalid JSON"
}

// jsonWrongTypePath returns the path of the first property, in sorted key
// order, whose value does not decode into its field of t, descending into
// nested objects (struct fields and map entries) to name the innermost one.
// It returns "" when every property decodes.
func jsonWrongTypePath(t reflect.Type, obj map[string]json.RawMessage, prefix string) string {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var ft reflect.Type
		switch t.Kind() {
		case reflect.Struct:
			f, ok := jsonField(t, k)
			if !ok {
				continue // the decoder ignores unknown keys inside objects
			}
			ft = f.Type
		case reflect.Map:
			ft = t.Elem()
		default:
			return ""
		}
		if p := jsonValueWrongType(ft, obj[k], prefix+k); p != "" {
			return p
		}
	}
	return ""
}

// jsonValueWrongType returns path when raw does not decode into t, or a deeper
// path when the culprit is a nested property.
func jsonValueWrongType(t reflect.Type, raw json.RawMessage, path string) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	custom := reflect.PointerTo(t).Implements(jsonUnmarshalerType)
	if !custom && (t.Kind() == reflect.Struct || t.Kind() == reflect.Map) {
		var nested map[string]json.RawMessage
		if json.Unmarshal(raw, &nested) == nil && nested != nil {
			if p := jsonWrongTypePath(t, nested, path+"."); p != "" {
				return p
			}
		}
	}
	if json.Unmarshal(raw, reflect.New(t).Interface()) != nil {
		return path
	}
	return ""
}

// jsonField finds the struct field encoding/json decodes key into: an exact
// tag (or field name) match first, then a case-insensitive one, as the
// decoder does.
func jsonField(t reflect.Type, key string) (reflect.StructField, bool) {
	var fold reflect.StructField
	found := false
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if name == key {
			return f, true
		}
		if !found && strings.EqualFold(name, key) {
			fold, found = f, true
		}
	}
	return fold, found
}
