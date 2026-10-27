package api

import (
	"encoding/json"
	"reflect"
	"testing"
)

func decodeFields(t *testing.T, body string) map[string]json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

func TestSettingsDecodeProblem(t *testing.T) {
	cases := map[string]string{
		`{"timeFormat":12}`:                           "invalid JSON: timeFormat has the wrong type",
		`{"focusMinBlockMinutes":1.5}`:                "invalid JSON: focusMinBlockMinutes has the wrong type",
		`{"protectLunch":"yes"}`:                      "invalid JSON: protectLunch has the wrong type",
		`{"workingHours":[]}`:                         "invalid JSON: workingHours has the wrong type",
		`{"workingHours":{"default":{"end":1}}}`:      "invalid JSON: workingHours.default.end has the wrong type",
		`{"lunchBreaks":{"friday":{"enabled":0}}}`:    "invalid JSON: lunchBreaks.friday.enabled has the wrong type",
		`{"workingHours":{"days":{"tuesday":"9-5"}}}`: "invalid JSON: workingHours.days.tuesday has the wrong type",
		`{"updatedAt":"yesterday"}`:                   "invalid JSON: updatedAt has the wrong type",
		// The first wrong property in key order is named.
		`{"workStart":1,"timeFormat":2}`: "invalid JSON: timeFormat has the wrong type",
		// Nothing pins a property: the fixed message.
		`{"timeFormat":"24h"}`: "invalid JSON",
	}
	for body, want := range cases {
		if got := settingsDecodeProblem(decodeFields(t, body)); got != want {
			t.Errorf("%s: got %q, want %q", body, got, want)
		}
	}
}

func TestJSONFieldMatchesTheDecoder(t *testing.T) {
	type s struct {
		Exact   string `json:"exactName"`
		Untag   string
		Skipped string `json:"-"`
	}
	typ := reflect.TypeOf(s{})
	for key, want := range map[string]string{"exactName": "Exact", "EXACTNAME": "Exact", "Untag": "Untag", "untag": "Untag"} {
		f, ok := jsonField(typ, key)
		if !ok || f.Name != want {
			t.Errorf("%q: got %v %v, want %s", key, f.Name, ok, want)
		}
	}
	for _, key := range []string{"Skipped", "-", "nope"} {
		if f, ok := jsonField(typ, key); ok {
			t.Errorf("%q: matched %s, want no field", key, f.Name)
		}
	}
}
