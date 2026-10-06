package appcli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMerchantStoreWriterGateDispatchNeverFallsBackToServing(t *testing.T) {
	for _, args := range [][]string{{"merchant-store-writer-gate"}, {"merchant-store-writer-gate", "downgrade"}} {
		var out, errs bytes.Buffer
		result := Dispatch(args, "test", &out, &errs)
		require.Equal(t, ModeExit, result.Mode)
		require.Equal(t, ExitUsage, result.ExitCode)
		require.Empty(t, out.String())
	}
	for _, action := range []string{"status", "bootstrap", "activate", "activate-lifecycle", "activate-refunds", "prepare-schema", "activate-access"} {
		var out, errs bytes.Buffer
		result := Dispatch([]string{"merchant-store-writer-gate", action}, "test", &out, &errs)
		require.Equal(t, ModeMerchantStoreWriterGate, result.Mode)
		require.Equal(t, []string{action}, result.GateArgs)
		require.Empty(t, result.ServeArgs)
		require.Empty(t, out.String())
	}
}
