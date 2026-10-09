package accountfunding

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

// Both runtimes consume the same versioned cases. This preserves the current
// team's payer-order boundary while the core is rebuilt independently.
func TestCoreFundingContract(t *testing.T) {
	raw, err := os.ReadFile("../../../../contracts/core/v1/funding-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name       string    `json:"name"`
		Owner      Account   `json:"owner"`
		Configured []Account `json:"configured"`
		Grants     []int64   `json:"grants"`
		Expected   []Account `json:"expected"`
		Error      string    `json:"error"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 20 {
		t.Fatal("shared contract unexpectedly empty")
	}
	errorKinds := map[string]error{"invalid_owner": ErrInvalidOwner, "invalid_order": ErrInvalidOrder, "not_authorized": ErrAccountNotAuthorized}
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			grants := make(map[int64]bool, len(test.Grants))
			for _, id := range test.Grants {
				grants[id] = true
			}
			got, err := ResolveOrder(test.Owner, test.Configured, grants)
			if test.Error != "" {
				expected, ok := errorKinds[test.Error]
				if !ok || !errors.Is(err, expected) {
					t.Fatalf("error=%v expected=%q", err, test.Error)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, test.Expected) {
				t.Fatalf("order=%v error=%v expected=%v", got, err, test.Expected)
			}
		})
	}
}
