package model

// The one-time balance correction preserves AmountUsed as historical usage.
// AmountTotal therefore includes that history until the next reset. ResetAmount
// records the finite grant to use after resetting or renewing that sold contract.
func (sub *UserSubscription) hasFiniteQuota() bool {
	return sub.AmountTotal > 0 || sub.ResetAmount != nil || sub.RenewalAmount != nil
}

func (sub *UserSubscription) resetRestoredQuota() int64 {
	if sub.ResetAmount == nil {
		return sub.AmountUsed
	}
	remaining := sub.AmountTotal - sub.AmountUsed
	if remaining < 0 {
		remaining = 0
	}
	restored := *sub.ResetAmount - remaining
	if restored < 0 {
		return 0
	}
	return restored
}

func sameSubscriptionResetAmount(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// A paid renewal restores the purchased contract, while resets within the paid
// period retain partial-refund reductions of ResetAmount.
func (sub *UserSubscription) applyRenewalGrant(snapshotGrant int64) {
	if sub.RenewalAmount != nil {
		grant := *sub.RenewalAmount
		sub.AmountTotal = grant
		sub.ResetAmount = &grant
	} else if sub.ResetAmount != nil {
		// Keep the corrected grant even if an incomplete migration omitted the
		// renewal snapshot; never resurrect the old unconverted plan snapshot.
		sub.AmountTotal = *sub.ResetAmount
	} else if snapshotGrant > 0 {
		sub.AmountTotal = snapshotGrant
	}
}
