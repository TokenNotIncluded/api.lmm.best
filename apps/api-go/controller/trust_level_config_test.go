package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTrustConfigurationOptionsExposeNormalizedRoleOnlyDefaults(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	common.OptionMapRWMutex.Lock()
	previous := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() { common.OptionMapRWMutex.Lock(); common.OptionMap = previous; common.OptionMapRWMutex.Unlock() })
	config := model.GetTrustLevelConfiguration()
	config.RoleTiers = nil // Existing configurations need not know the new role section.
	config.Tiers[4].DiscountRatio = 0.5
	raw, err := json.Marshal(config)
	require.NoError(t, err)
	stored := model.Option{Key: model.TrustLevelBenefitsOptionKey, Value: string(raw)}
	require.NoError(t, db.Create(&stored).Error)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/option/", nil)
	GetOptions(c)
	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Success bool           `json:"success"`
		Data    []model.Option `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.True(t, body.Success)
	found := false
	for _, option := range body.Data {
		if option.Key != model.TrustLevelBenefitsOptionKey {
			continue
		}
		found = true
		var exposed model.TrustLevelConfiguration
		require.NoError(t, json.Unmarshal([]byte(option.Value), &exposed))
		require.Len(t, exposed.Tiers, 5)
		require.Len(t, exposed.RoleTiers, 2)
		require.Equal(t, 0.5, exposed.Tiers[4].DiscountRatio)
		require.Equal(t, 0.9, exposed.RoleTiers[0].DiscountRatio, "administrator discount is independent of L4")
		require.Equal(t, 0.9, exposed.RoleTiers[1].DiscountRatio)
	}
	require.True(t, found)
	invalid := httptest.NewRecorder()
	c, _ = gin.CreateTestContext(invalid)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(`{"key":"TrustLevelBenefits","value":"{\"version\":1,\"tiers\":[]}"}`))
	UpdateOption(c)
	var rejected struct {
		Success bool `json:"success"`
	}
	require.NoError(t, json.Unmarshal(invalid.Body.Bytes(), &rejected))
	require.False(t, rejected.Success)
	var after model.Option
	require.NoError(t, db.Where("key = ?", stored.Key).First(&after).Error)
	require.Equal(t, stored.Value, after.Value, "invalid HTTP configuration cannot alter persisted thresholds")
}
