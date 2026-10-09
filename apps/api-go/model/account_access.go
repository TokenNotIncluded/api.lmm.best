package model

import (
	"errors"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/pkg/account"
)

// AccountAccessSnapshot keeps resource ownership separate from the authenticated
// user's platform role. It describes product access, not membership or budgets.
type AccountAccessSnapshot struct {
	Account         account.Ref          `json:"account"`
	TrustLevel      TrustLevelInfo       `json:"trust_level_info"`
	DeveloperAccess DeveloperAccessState `json:"developer_access"`
}

// TeamAccountFacts must come from the team's durable payment/activation records.
// NetPaidCredits excludes internal transfers, grants, and refunded paid credits.
// A creator's personal history, level, or platform role is never an input.
// Loading these records under account ownership is still WIP; no route uses
// this adapter until the account storage and authorization migrations exist.
type TeamAccountFacts struct {
	Account             account.Ref
	NetPaidCredits      int64
	PaidRows            int64
	ProjectionAvailable bool
	ConsoleActivated    bool
	OverrideLevel       *int
	ActivityAnchor      int64
}

var ErrInvalidTeamAccountFacts = errors.New("invalid team account access facts")

// EvaluateTeamAccountAccess reuses the existing tier configuration, activation,
// decay, override, and discount rules. It never evaluates platform role tiers.
// Callers must separately check actor restrictions, membership, and spending
// authorization in the same transaction used to reserve money and budgets.
func EvaluateTeamAccountAccess(facts TeamAccountFacts, now int64) (AccountAccessSnapshot, error) {
	return evaluateTeamAccountAccess(facts, now, CurrentDeveloperAccessPolicy())
}

func evaluateTeamAccountAccess(facts TeamAccountFacts, now int64, policy DeveloperAccessPolicy) (AccountAccessSnapshot, error) {
	if !facts.Account.Valid() || facts.Account.Kind != account.Team || facts.NetPaidCredits < 0 ||
		facts.NetPaidCredits > common.MaxWalletQuota || facts.PaidRows < 0 || facts.ActivityAnchor < 0 ||
		(facts.PaidRows == 0) != (facts.NetPaidCredits == 0) {
		return AccountAccessSnapshot{}, ErrInvalidTeamAccountFacts
	}
	if !facts.ProjectionAvailable {
		return AccountAccessSnapshot{}, ErrPaidCreditProjectionUnavailable
	}
	var override *int
	if facts.OverrideLevel != nil {
		value := *facts.OverrideLevel
		if value < TrustLevelMinUser || value > TrustLevelMaxUser {
			return AccountAccessSnapshot{}, ErrInvalidTeamAccountFacts
		}
		override = &value
	}
	aggregate := paidTopUpAggregate{PaidCredits: facts.NetPaidCredits, PaidRows: facts.PaidRows}
	paid := aggregate.paidActivationComplete(policy)
	access, explicit := explicitDeveloperAccessDecision(common.RoleCommonUser, override)
	if !explicit {
		access = ordinaryDeveloperAccessStateWithPolicy(paid, facts.ConsoleActivated, policy)
	}
	return AccountAccessSnapshot{
		Account: facts.Account,
		// The common-user branch is the shared L0-L4 product policy, not a
		// synthetic team User row. Existing personal L5/L6 behavior is unchanged.
		TrustLevel: evaluateTrustLevelCredits(common.RoleCommonUser, override, facts.NetPaidCredits,
			float64(facts.NetPaidCredits)/500000, paid || facts.ConsoleActivated,
			facts.ActivityAnchor, now, policy.trustConfiguration),
		DeveloperAccess: access,
	}, nil
}
