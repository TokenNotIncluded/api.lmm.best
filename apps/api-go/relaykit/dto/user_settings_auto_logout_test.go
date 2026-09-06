package dto

import (
	"encoding/json"
	"testing"
)

func TestSessionAutoLogoutDefaultsAndExplicitOptOut(t *testing.T) {
	for _, test := range []struct {
		settings string
		enabled  bool
	}{
		{`{}`, true},
		{`{"language":"zh"}`, true},
		{`{"session_auto_logout":null}`, true},
		{`{"session_auto_logout":true}`, true},
		{`{"session_auto_logout":false}`, false},
	} {
		t.Run(test.settings, func(t *testing.T) {
			var setting UserSetting
			if err := json.Unmarshal([]byte(test.settings), &setting); err != nil {
				t.Fatal(err)
			}
			if got := setting.IsSessionAutoLogoutEnabled(); got != test.enabled {
				t.Fatalf("IsSessionAutoLogoutEnabled() = %t, want %t", got, test.enabled)
			}
		})
	}
}
