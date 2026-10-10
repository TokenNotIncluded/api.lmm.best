package model

import "errors"

// ErrAssistantL1ReviewRequired is deliberately independent of model output,
// conversation history, risk scores and confirmation credentials.
var ErrAssistantL1ReviewRequired = errors.New("assistant output cannot authorize L1 access; an independent authorized decision or qualifying payment is required")

// GrantAssistantDeveloperAccess is a fail-closed compatibility boundary for
// callers from the retired direct-grant workflow. It does not read or mutate
// the database, consume confirmations, change account state, or rewrite audit
// history. A customer assistant is not an independent access reviewer.
//
// Keep returning a non-nil error: old callers interpret a nil error as an
// authoritative activation receipt, not as a pending recommendation. User
// confirmation only permits submission; it can never turn this into approval.
// Paid activation and separately authorized administrative actions do not use
// this function and retain their own server-side authorization checks.
func GrantAssistantDeveloperAccess(userID int, conversationID int64, reason string, recommendation string) (*AssistantDeveloperAccessGrant, error) {
	return nil, ErrAssistantL1ReviewRequired
}
