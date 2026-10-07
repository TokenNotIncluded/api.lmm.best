package service

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestRetiredAssistantReportIsNotRegisteredAlongsideCurrentTasks(t *testing.T) {
	types := map[string]bool{}
	for _, handler := range registeredSystemTaskHandlers() {
		types[handler.Type()] = true
	}
	require.False(t, types["assistant_review"])
	require.True(t, types[model.SystemTaskTypeLogCleanup])
	require.True(t, types[model.SystemTaskTypeAssistantRetention])
}
