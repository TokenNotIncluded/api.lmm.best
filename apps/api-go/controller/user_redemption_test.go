package controller

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedeemFailureLogOmitsSubmittedKey(t *testing.T) {
	const submittedKey = "redeem-secret-123456789" // gitleaks:allow

	logLine := redeemFailureLog(42, errors.New("invalid redemption code: "+submittedKey))

	require.Contains(t, logLine, "user 42")
	require.NotContains(t, logLine, submittedKey)
	require.NotContains(t, logLine, "key "+submittedKey)
}
