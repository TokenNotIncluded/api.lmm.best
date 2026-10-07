package main

import (
	"bytes"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/internal/appcli"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreWriterGateCommandRejectsFlagsBeforeDatabaseAccess(t *testing.T) {
	t.Setenv("SQL_DSN", "postgres://private-password@127.0.0.1:1/must-never-connect?sslmode=disable")
	for _, args := range [][]string{
		{"prepare-fixed-content"}, {"activate-fixed-content"},
		{"prepare-fixed-content", "--expected-current=5", "--reviewed-fixed-content-ready"},
		{"activate-fixed-content", "--expected-current=6"},
		{"activate-fixed-content", "--expected-current=6", "--reviewed-fixed-content-ready", "--require-writable"},
		{"activate-fixed-content", "--expected-current=6", "--reviewed-fixed-content-ready", "--reviewed-phase-six-ready"},
		{"verify-fixed-content", "--expected-current=6"}, {"verify-fixed-content", "--reviewed-fixed-content-ready"},
		{"status", "--reviewed-fixed-content-ready"},
		{"activate-phase-six"}, {"activate-phase-six", "--expected-current=4", "--reviewed-phase-six-ready"},
		{"activate-phase-six", "--expected-current=5"}, {"activate-phase-six", "--expected-current=5", "--reviewed-phase-six-ready", "--require-writable"},
		{"status", "--reviewed-phase-six-ready"}, {"prepare-schema", "--expected-current=5", "--reviewed-store-schema-ready", "--reviewed-phase-six-ready"},
		nil, {"unknown"}, {"status", "--reviewed-variants-ready"}, {"bootstrap", "--require-writable"},
		{"activate"}, {"activate", "--expected-current=1"}, {"activate", "--reviewed-variants-ready"},
		{"activate", "--expected-current=0", "--reviewed-variants-ready"},
		{"activate", "--expected-current=1", "--reviewed-variants-ready", "--require-writable"},
		{"status", "extra-positional"}, {"activate", "--to=1"},
		{"status", "--reviewed-lifecycle-ready"}, {"activate-lifecycle"},
		{"activate-lifecycle", "--expected-current=1", "--reviewed-lifecycle-ready"},
		{"activate-lifecycle", "--expected-current=2"},
		{"activate-lifecycle", "--expected-current=2", "--reviewed-lifecycle-ready", "--require-writable"},
		{"activate-lifecycle", "--expected-current=2", "--reviewed-lifecycle-ready", "--reviewed-variants-ready"},
		{"status", "--reviewed-refunds-ready"}, {"activate-refunds"},
		{"prepare-schema"}, {"prepare-schema", "--expected-current=4"},
		{"prepare-schema", "--expected-current=0", "--reviewed-store-schema-ready"},
		{"prepare-schema", "--expected-current=4", "--reviewed-store-schema-ready", "--reviewed-access-ready"},
		{"prepare-schema", "--expected-current=4", "--reviewed-store-schema-ready", "--require-writable"},
		{"activate-access"}, {"activate-access", "--expected-current=3", "--reviewed-access-ready"},
		{"activate-access", "--expected-current=4"},
		{"activate-access", "--expected-current=4", "--reviewed-access-ready", "--reviewed-refunds-ready"},
		{"status", "--reviewed-access-ready"}, {"bootstrap", "--reviewed-store-schema-ready"},

		{"activate-refunds", "--expected-current=2", "--reviewed-refunds-ready"},
		{"activate-refunds", "--expected-current=3"},
		{"activate-refunds", "--expected-current=3", "--reviewed-refunds-ready", "--require-writable"},
		{"activate-refunds", "--expected-current=3", "--reviewed-refunds-ready", "--reviewed-lifecycle-ready"},
	} {
		var out, errs bytes.Buffer
		require.Equal(t, appcli.ExitUsage, runMerchantStoreWriterGateCommand(args, &out, &errs), args)
		require.Empty(t, out.String())
		require.NotContains(t, errs.String(), "private-password")
	}
}

func TestMerchantStoreWriterGateCommandRequiresExplicitDatabaseWithoutServerStartup(t *testing.T) {
	t.Setenv("SQL_DSN", "")
	for _, args := range [][]string{
		{"status"}, {"bootstrap"}, {"verify-fixed-content"},
		{"prepare-fixed-content", "--expected-current=6", "--reviewed-fixed-content-ready"},
		{"activate-fixed-content", "--expected-current=6", "--reviewed-fixed-content-ready"},
		{"prepare-schema", "--expected-current=5", "--reviewed-store-schema-ready"},
		{"activate-phase-six", "--expected-current=5", "--reviewed-phase-six-ready"},
	} {
		var out, errs bytes.Buffer
		result := appcli.Dispatch(append([]string{"merchant-store-writer-gate"}, args...), "test", &out, &errs)
		require.Equal(t, appcli.ModeMerchantStoreWriterGate, result.Mode)
		require.Equal(t, args, result.GateArgs)
		require.Equal(t, appcli.ExitError, runMerchantStoreWriterGateCommand(result.GateArgs, &out, &errs))
		require.Empty(t, out.String())
		require.Empty(t, errs.String())
	}
}
