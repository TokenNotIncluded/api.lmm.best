package common

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveSystemInstanceReporterIDPreservesUnslottedIdentity(t *testing.T) {
	reporterID, err := DeriveSystemInstanceReporterID("forge-host-1", "")
	require.NoError(t, err)
	assert.Equal(t, "forge-host-1", reporterID)
}

func TestDeriveSystemInstanceReporterIDDistinguishesRuntimeSlots(t *testing.T) {
	blue, err := DeriveSystemInstanceReporterID("forge-host-1", "blue")
	require.NoError(t, err)
	green, err := DeriveSystemInstanceReporterID("forge-host-1", "green")
	require.NoError(t, err)

	assert.Equal(t, "forge-host-1@blue", blue)
	assert.Equal(t, "forge-host-1@green", green)
	assert.NotEqual(t, blue, green)
}

func TestValidateAPIInstanceSlotRejectsUnsafeValues(t *testing.T) {
	for _, slot := range []string{
		"Blue",
		"blue/green",
		" blue",
		"blue ",
		"-blue",
		"green-",
		"green.",
		strings.Repeat("a", APIInstanceSlotMaxLength+1),
	} {
		t.Run(slot, func(t *testing.T) {
			assert.Error(t, ValidateAPIInstanceSlot(slot))
		})
	}

	for _, slot := range []string{"", "blue", "green", "preview-2", "canary_1"} {
		t.Run("valid_"+slot, func(t *testing.T) {
			assert.NoError(t, ValidateAPIInstanceSlot(slot))
		})
	}
}

func TestDeriveSystemInstanceReporterIDRejectsOversizedDatabaseKey(t *testing.T) {
	_, err := DeriveSystemInstanceReporterID(
		strings.Repeat("n", systemInstanceReporterIDMaxLen),
		"blue",
	)
	assert.ErrorContains(t, err, "reporter identity exceeds")
}

func TestInitNodeNameIdentityReadsGoSpecificSlot(t *testing.T) {
	originalNodeName := NodeName
	originalSource := NodeNameSource
	originalManual := NodeNameManuallyConfigured
	originalSlot := APIInstanceSlot
	t.Cleanup(func() {
		NodeName = originalNodeName
		NodeNameSource = originalSource
		NodeNameManuallyConfigured = originalManual
		APIInstanceSlot = originalSlot
	})

	t.Setenv("NODE_NAME", "forge-host-1")
	t.Setenv(APIInstanceSlotEnv, "blue")
	t.Setenv("LMM_RS_SLOT", "green")

	require.NoError(t, initNodeNameIdentity())
	assert.Equal(t, "forge-host-1", NodeName)
	assert.Equal(t, "blue", APIInstanceSlot)
}

func TestInitNodeNameIdentityRejectsInvalidSlot(t *testing.T) {
	t.Setenv("NODE_NAME", "forge-host-1")
	t.Setenv(APIInstanceSlotEnv, "blue/green")

	err := initNodeNameIdentity()
	assert.ErrorContains(t, err, APIInstanceSlotEnv)
}
