package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemInstanceSlotsListAndCleanUpAsIndependentReporters(t *testing.T) {
	require.NoError(t, DB.Exec("DELETE FROM system_instances").Error)
	t.Cleanup(func() { _ = DB.Exec("DELETE FROM system_instances").Error })

	blueInfo := map[string]any{
		"schema_version": 2,
		"reporter":       map[string]any{"id": "forge-host-1@blue", "slot": "blue"},
		"node":           map[string]any{"name": "forge-host-1"},
		"runtime":        map[string]any{"instance_slot": "blue"},
	}
	greenInfo := map[string]any{
		"schema_version": 2,
		"reporter":       map[string]any{"id": "forge-host-1@green", "slot": "green"},
		"node":           map[string]any{"name": "forge-host-1"},
		"runtime":        map[string]any{"instance_slot": "green"},
	}
	legacyInfo := map[string]any{
		"schema_version": 1,
		"node":           map[string]any{"name": "legacy-host"},
	}

	require.NoError(t, UpsertSystemInstance("forge-host-1@blue", blueInfo, 10, 100))
	require.NoError(t, UpsertSystemInstance("forge-host-1@green", greenInfo, 20, 150))
	require.NoError(t, UpsertSystemInstance("legacy-host", legacyInfo, 30, 160))

	instances, err := ListSystemInstances()
	require.NoError(t, err)
	require.Len(t, instances, 3)

	responses := make(map[string]SystemInstanceResponse, len(instances))
	for _, instance := range instances {
		response := instance.ToResponse(200)
		responses[response.ReporterID] = response
	}

	assert.Equal(t, "forge-host-1", responses["forge-host-1@blue"].PhysicalNodeName)
	assert.Equal(t, "blue", responses["forge-host-1@blue"].InstanceSlot)
	assert.Equal(t, SystemInstanceStatusStale, responses["forge-host-1@blue"].Status)
	assert.Equal(t, "forge-host-1", responses["forge-host-1@green"].PhysicalNodeName)
	assert.Equal(t, "green", responses["forge-host-1@green"].InstanceSlot)
	assert.Equal(t, SystemInstanceStatusOnline, responses["forge-host-1@green"].Status)
	assert.Equal(t, "legacy-host", responses["legacy-host"].NodeName)
	assert.Equal(t, "legacy-host", responses["legacy-host"].ReporterID)
	assert.Equal(t, "legacy-host", responses["legacy-host"].PhysicalNodeName)
	assert.Empty(t, responses["legacy-host"].InstanceSlot)

	deleted, err := DeleteStaleSystemInstance("forge-host-1@blue", 200)
	require.NoError(t, err)
	assert.True(t, deleted)

	deleted, err = DeleteStaleSystemInstance("forge-host-1@green", 200)
	require.NoError(t, err)
	assert.False(t, deleted, "an online sibling slot must not be deleted")

	instances, err = ListSystemInstances()
	require.NoError(t, err)
	require.Len(t, instances, 2)
	assert.Equal(t, "legacy-host", instances[0].NodeName)
	assert.Equal(t, "forge-host-1@green", instances[1].NodeName)
}
