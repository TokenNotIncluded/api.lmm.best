package model

import (
	"errors"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/pkg/account"
)

func teamAccessTestPolicy() DeveloperAccessPolicy {
	config := legacyTrustLevelConfiguration()
	config.PaidActivationEnabled = true
	for i, credits := range []int64{0, 10, 100, 500, 1000} {
		config.Tiers[i].MinPaidCredits = credits
	}
	return DeveloperAccessPolicy{paidActivationEnabled: true, trustConfiguration: config}
}

func TestTeamAccountAccessSharedPolicy(t *testing.T) {
	const now int64 = 2_000_000_000
	policy := teamAccessTestPolicy()
	base := TeamAccountFacts{Account: account.Ref{Kind: account.Team, ID: 42}, ProjectionAvailable: true, ActivityAnchor: now}
	for _, tc := range []struct {
		name          string
		credits, rows int64
		activated     bool
		level         int
	}{
		{"new team", 0, 0, false, 0},
		{"below configured activation", 9, 1, false, 0},
		{"paid activation", 10, 1, false, 1},
		{"configured L2", 100, 1, false, 2},
		{"configured L3", 500, 1, false, 3},
		{"configured L4", 1000, 1, false, 4},
		{"maximum credits never grant L5", common.MaxWalletQuota, 1, false, 4},
		{"explicit account activation", 0, 0, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := base
			facts.NetPaidCredits, facts.PaidRows, facts.ConsoleActivated = tc.credits, tc.rows, tc.activated
			s, err := evaluateTeamAccountAccess(facts, now, policy)
			if err != nil {
				t.Fatal(err)
			}
			if s.Account != base.Account || s.TrustLevel.Level != tc.level || s.DeveloperAccess.Granted != (tc.level > 0) || s.TrustLevel.LevelSource == "role" {
				t.Fatalf("wrong team policy: %+v", s)
			}
			if s.TrustLevel.DiscountRatio != policy.trustConfiguration.Tiers[tc.level].DiscountRatio {
				t.Fatal("team did not use shared tier discount")
			}
		})
	}
	base.NetPaidCredits, base.PaidRows = 500, 1
	policy.trustConfiguration.Tiers[3].DiscountRatio = 0.83
	policy.trustConfiguration.RoleTiers[0].DiscountRatio = 0.01
	policy.trustConfiguration.RoleTiers[1].DiscountRatio = 0.02
	s, err := evaluateTeamAccountAccess(base, now, policy)
	if err != nil || s.TrustLevel.DiscountRatio != 0.83 {
		t.Fatalf("role discount leaked or tier configuration ignored: %+v %v", s, err)
	}
	base.ActivityAnchor = now - int64(policy.trustConfiguration.DecayPeriodDays)*86400
	s, err = evaluateTeamAccountAccess(base, now, policy)
	if err != nil || s.TrustLevel.Level != 2 || s.TrustLevel.InactivityDecaySteps != 1 {
		t.Fatalf("decay not reused: %+v %v", s, err)
	}
	policy.paidActivationEnabled = false
	s, err = evaluateTeamAccountAccess(base, now, policy)
	if err != nil || s.DeveloperAccess.Granted || s.TrustLevel.Level != 0 {
		t.Fatal("disabled activation still granted access")
	}
	// Existing personal role mapping remains separate and unchanged.
	for role, level := range map[int]int{common.RoleAdminUser: 5, common.RoleRootUser: 6} {
		info := evaluateTrustLevelCredits(role, nil, 0, 0, false, now, now, policy.trustConfiguration)
		if info.Level != level || info.LevelSource != "role" {
			t.Fatalf("personal platform role changed: %+v", info)
		}
	}
}

func TestTeamAccountAccessInvalidFactsAndOverrides(t *testing.T) {
	base := TeamAccountFacts{Account: account.Ref{Kind: account.Team, ID: 42}, ProjectionAvailable: true}
	policy := teamAccessTestPolicy()
	for _, mutate := range []func(*TeamAccountFacts){
		func(f *TeamAccountFacts) { f.Account.Kind = account.Personal },
		func(f *TeamAccountFacts) { f.Account.ID = 0 },
		func(f *TeamAccountFacts) { f.NetPaidCredits = -1 },
		func(f *TeamAccountFacts) { f.PaidRows = -1 },
		func(f *TeamAccountFacts) { f.NetPaidCredits = 1 },
		func(f *TeamAccountFacts) { f.PaidRows = 1 },
		func(f *TeamAccountFacts) { f.ActivityAnchor = -1 },
	} {
		facts := base
		mutate(&facts)
		s, err := evaluateTeamAccountAccess(facts, 2_000_000_000, policy)
		if !errors.Is(err, ErrInvalidTeamAccountFacts) || s.Account.Valid() || s.DeveloperAccess.Granted {
			t.Fatalf("invalid facts accepted: %+v %v", s, err)
		}
	}
	for _, level := range []int{-1, 5, 6, 100} {
		facts := base
		facts.OverrideLevel = &level
		if _, err := evaluateTeamAccountAccess(facts, 2_000_000_000, policy); !errors.Is(err, ErrInvalidTeamAccountFacts) {
			t.Fatalf("invalid team override %d", level)
		}
	}
	for _, level := range []int{0, 1, 4} {
		facts := base
		value := level
		facts.OverrideLevel = &value
		s, err := evaluateTeamAccountAccess(facts, 2_000_000_000, policy)
		value = 6
		if err != nil || s.TrustLevel.Level != level || *s.TrustLevel.OverrideLevel != level || s.DeveloperAccess.Granted != (level > 0) {
			t.Fatalf("override or snapshot alias error: %+v %v", s, err)
		}
	}
	base.ProjectionAvailable = false
	if _, err := evaluateTeamAccountAccess(base, 2_000_000_000, policy); !errors.Is(err, ErrPaidCreditProjectionUnavailable) {
		t.Fatal("unknown credits treated as zero")
	}
	// Exported adapter reads the same current server policy; no database access.
	base.ProjectionAvailable = true
	if s, err := EvaluateTeamAccountAccess(base, 2_000_000_000); err != nil || s.TrustLevel.Level != 0 {
		t.Fatalf("current policy adapter: %+v %v", s, err)
	}
}
