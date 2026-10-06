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
	} {
		var out, errs bytes.Buffer
		require.Equal(t, appcli.ExitUsage, runMerchantStoreWriterGateCommand(args, &out, &errs), args)
		require.Empty(t, out.String())
		require.NotContains(t, errs.String(), "private-password")
	}
}

func TestMerchantStoreWriterGateCommandRequiresExplicitDatabaseWithoutServerStartup(t *testing.T) {
	t.Setenv("SQL_DSN", "")
	for _, action := range []string{"status", "bootstrap"} {
		var out, errs bytes.Buffer
		require.Equal(t, appcli.ExitError, runMerchantStoreWriterGateCommand([]string{action}, &out, &errs))
		require.Empty(t, out.String())
		require.Empty(t, errs.String())
	}
}
