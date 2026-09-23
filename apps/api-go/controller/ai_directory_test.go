/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetAIDirectoryReturnsConfiguredArray(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	previous, hadPrevious := common.OptionMap[model.AIDirectoryLinksOptionKey]
	common.OptionMap[model.AIDirectoryLinksOptionKey] = `[]`
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if hadPrevious {
			common.OptionMap[model.AIDirectoryLinksOptionKey] = previous
		} else {
			delete(common.OptionMap, model.AIDirectoryLinksOptionKey)
		}
	})

	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	GetAIDirectory(context)
	require.Equal(t, http.StatusOK, response.Code)
	var payload struct {
		Data struct {
			Links []any `json:"links"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.NotNil(t, payload.Data.Links)
	require.Empty(t, payload.Data.Links)
}
