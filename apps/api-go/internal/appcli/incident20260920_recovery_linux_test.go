//go:build linux

package appcli

import (
	"encoding/json"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"strings"
	"testing"
)

func TestIncident20260920OnlyRepairsExactDefaultGrants(t *testing.T) {
	rows := []map[string]string{{"ptype": "p", "v0": "role:admin", "v1": "acquisition", "v2": "read", "v3": "allow", "v4": "", "v5": ""}, {"ptype": "p", "v0": "role:admin", "v1": "acquisition", "v2": "write", "v3": "allow", "v4": "", "v5": ""}}
	raw, _ := json.Marshal(rows)
	if err := validateIncident20260920Policies(raw); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"ptype", "v0", "v1", "v2", "v3", "v4", "v5"} {
		t.Run(field, func(t *testing.T) {
			var copy []map[string]string
			_ = json.Unmarshal(raw, &copy)
			copy[0][field] = "unexpected"
			b, _ := json.Marshal(copy)
			if validateIncident20260920Policies(b) == nil {
				t.Fatal("accepted unexpected policy")
			}
		})
	}
	if validateIncident20260920Policies([]byte("[]")) == nil {
		t.Fatal("accepted absent audit grants")
	}
	if !strings.Contains(incident20260920RepairSQL("public"), "5498135663004418049") {
		t.Fatal("migration lock contract changed")
	}
	for _, forbidden := range []string{"users", "tokens", "subscription_pre_consume_records", "DROP TABLE", "ALTER TABLE"} {
		if strings.Contains(incident20260920RepairSQL("public"), forbidden) {
			t.Fatal("repair touches business data")
		}
	}
}

func TestIncident20260920RejectsUnboundState(t *testing.T) {
	if validateIncident20260920(productionManifest{}, productionStatus{}) == nil {
		t.Fatal("accepted unbound incident")
	}
}

func TestIncident20260920UsesAuthoritativeTableName(t *testing.T) {
	if !strings.Contains(incident20260920RepairSQL("public"), "public."+(model.CasbinRule{}).TableName()+" WHERE") {
		t.Fatal("wrong authorization table")
	}
}
