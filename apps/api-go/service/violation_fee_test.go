package service

import (
	"errors"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/stretchr/testify/require"
)

func TestNormalizeViolationFeeErrorIsProviderAgnostic(t *testing.T) {
	err := types.NewErrorWithStatusCode(errors.New("Content violates usage guidelines"), types.ErrorCodeBadResponse, 400)
	normalized := NormalizeViolationFeeError(err)
	require.Equal(t, types.ErrorCodeViolationFeeUsagePolicy, normalized.GetErrorCode())
	require.True(t, IsViolationFeeCode(normalized.GetErrorCode()))
}
