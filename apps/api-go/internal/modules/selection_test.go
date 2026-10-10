package modules

import (
	"reflect"
	"testing"
)

func TestModuleSelection(t *testing.T) {
	defaults := []string{"identity"}
	available := []string{"identity", "catalog"}
	for _, tc := range []struct {
		config string
		want   []string
		bad    bool
	}{
		{"", []string{"identity"}, false},
		{"none", []string{}, false},
		{"catalog, identity", []string{"catalog", "identity"}, false},
		{"missing", nil, true},
		{"identity,identity", nil, true},
		{"identity,", nil, true},
		{"all", nil, true},
	} {
		got, err := Select(tc.config, defaults, available)
		if (err != nil) != tc.bad || (!tc.bad && !reflect.DeepEqual(got, tc.want)) {
			t.Fatalf("config=%q got=%v err=%v", tc.config, got, err)
		}
	}
	got, err := Select("", defaults, available)
	if err != nil {
		t.Fatal(err)
	}
	got[0] = "catalog"
	if defaults[0] != "identity" {
		t.Fatal("selection mutated defaults")
	}
	if _, err := Select("", nil, []string{"identity", "identity"}); err == nil {
		t.Fatal("duplicate available module accepted")
	}
}
