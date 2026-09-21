/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package controller

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestConfiguredPaymentMethodMaxTopUpUsesMostRestrictiveDuplicate(t *testing.T) {
	withPaymentMethods(t, []map[string]string{
		{"name": "LinuxDO A", "type": "epay", "max_topup": "20"},
		{"name": "LinuxDO B", "type": "epay", "max_topup": "7.5"},
	})

	limit, configured, err := configuredPaymentMethodMaxTopUp("epay")
	require.NoError(t, err)
	assert.True(t, configured)
	assert.Equal(t, "7.5", limit.String())
}

func TestConfiguredPaymentMethodMaxTopUpRejectsInvalidLimit(t *testing.T) {
	withPaymentMethods(t, []map[string]string{{
		"name": "LinuxDO", "type": "epay", "max_topup": "0",
	}})

	_, configured, err := configuredPaymentMethodMaxTopUp("epay")
	assert.True(t, configured)
	assert.Error(t, err)
}

func TestConfiguredPaymentMethodMinTopUpUsesStrictestDuplicate(t *testing.T) {
	withPaymentMethods(t, []map[string]string{
		{"name": "Card A", "type": "epay", "min_topup": "2"},
		{"name": "Card B", "type": "epay", "min_topup": "7.5"},
	})

	minimum, configured, err := configuredPaymentMethodMinTopUp("epay")
	require.NoError(t, err)
	assert.True(t, configured)
	assert.Equal(t, "7.5", minimum.String())
}

func withTopUpPricing(t *testing.T, cnyPerUSD, platformUnitsPerCNY float64) {
	t.Helper()
	originalCNYPerUSD := operation_setting.USDExchangeRate
	originalPlatformUnitsPerCNY := operation_setting.TopUpPlatformUnitsPerCNY
	operation_setting.USDExchangeRate = cnyPerUSD
	operation_setting.TopUpPlatformUnitsPerCNY = platformUnitsPerCNY
	t.Cleanup(func() {
		operation_setting.USDExchangeRate = originalCNYPerUSD
		operation_setting.TopUpPlatformUnitsPerCNY = originalPlatformUnitsPerCNY
	})
}

func TestRequirePaymentMethodTopUpWithinLimitEnforcesMinimum(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withTopUpPricing(t, 1, 1)
	withPaymentMethods(t, []map[string]string{{
		"name": "LinuxDO", "type": "epay", "min_topup": "5",
	}})

	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	assert.False(t, requirePaymentMethodTopUpWithinLimit(context, "epay", 4))
	assert.Contains(t, response.Body.String(), "5")
}

func TestRequirePaymentMethodTopUpWithinLimitUsesCreditedUSD(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withTopUpPricing(t, 6.8, 1)
	withPaymentMethods(t, []map[string]string{{
		"name": "LinuxDO", "type": "epay", "max_topup": "2.5",
	}})
	previousDisplayType := operation_setting.GetGeneralSetting().QuotaDisplayType
	t.Cleanup(func() {
		operation_setting.GetGeneralSetting().QuotaDisplayType = previousDisplayType
	})

	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	allowedResponse := httptest.NewRecorder()
	allowedContext, _ := gin.CreateTestContext(allowedResponse)
	assert.True(t, requirePaymentMethodTopUpWithinLimit(allowedContext, "epay", 17))

	blockedResponse := httptest.NewRecorder()
	blockedContext, _ := gin.CreateTestContext(blockedResponse)
	assert.False(t, requirePaymentMethodTopUpWithinLimit(blockedContext, "epay", 18))
	assert.Contains(t, blockedResponse.Body.String(), "2.5")

	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	tokenResponse := httptest.NewRecorder()
	tokenContext, _ := gin.CreateTestContext(tokenResponse)
	overLimitTokens := int64(18 * common.QuotaPerUnit)
	assert.False(t, requirePaymentMethodTopUpWithinLimit(tokenContext, "epay", overLimitTokens))
}

func TestGetTopUpInfoMaxTopUpAmountMatchesEnforcedLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	confirmPaymentComplianceForTest(t)
	preserveChannelPricing(t)
	setupTopupInfoUser(t, 304, "default")
	previousQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = previousQuotaPerUnit })
	operation_setting.USDExchangeRate = 6.8
	operation_setting.PayMethods = []map[string]string{
		{
			"name": "Custom gateway", "type": "custom", "max_topup": "2.5",
			"settlement_currency": "USD", "platform_units_per_usd": "100",
			"settlement_units_per_usd": "1", "max_topup_amount": "999999",
		},
		{"name": "Duplicate gateway", "type": "custom", "max_topup": "10"},
	}

	for _, test := range []struct {
		displayType   string
		purchaseRatio float64
		expected      string
	}{
		{operation_setting.QuotaDisplayTypeUSD, 1, "17"},
		{operation_setting.QuotaDisplayTypeCNY, 1.1, "18.7"},
		{operation_setting.QuotaDisplayTypeTokens, 1, "8500000"},
	} {
		t.Run(test.displayType, func(t *testing.T) {
			operation_setting.GetGeneralSetting().QuotaDisplayType = test.displayType
			operation_setting.TopUpPlatformUnitsPerCNY = test.purchaseRatio
			response := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(response)
			context.Set("id", 304)
			GetTopUpInfo(context)
			var payload struct {
				Data struct {
					PayMethods []map[string]string `json:"pay_methods"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
			require.Len(t, payload.Data.PayMethods, 2)
			for _, method := range payload.Data.PayMethods {
				assert.Equal(t, test.expected, method["max_topup_amount"])
			}
			assert.Equal(t, "2.5", payload.Data.PayMethods[0]["max_topup"])
			assert.Equal(t, "10", payload.Data.PayMethods[1]["max_topup"])
			assert.Equal(t, "100", payload.Data.PayMethods[0]["platform_units_per_usd"])
			assert.Equal(t, "999999", operation_setting.PayMethods[0]["max_topup_amount"])
			// Filtering the catalog must not hide a stricter policy duplicate.
			visible := sanitizedPaymentMethods(operation_setting.PayMethods[1:])
			require.Len(t, visible, 1)
			assert.Equal(t, test.expected, visible[0]["max_topup_amount"])
			maximum := decimal.RequireFromString(test.expected)
			allowed, _ := gin.CreateTestContext(httptest.NewRecorder())
			assert.True(t, requirePaymentMethodTopUpDecimalWithinLimit(allowed, "custom", maximum))
			blocked, _ := gin.CreateTestContext(httptest.NewRecorder())
			assert.False(t, requirePaymentMethodTopUpDecimalWithinLimit(blocked, "custom", maximum.Add(decimal.NewFromInt(1))))
		})
	}
}

func TestSanitizedPaymentMethodsOmitsUnreliableMaxTopUpAmounts(t *testing.T) {
	preserveChannelPricing(t)
	previousQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = previousQuotaPerUnit })
	for _, test := range []struct {
		name          string
		maximum       string
		fx            float64
		purchaseRatio float64
		quotaPerUnit  float64
	}{
		{"missing limit", "", 6.8, 1, 500_000},
		{"zero limit", "0", 6.8, 1, 500_000},
		{"negative limit", "-1", 6.8, 1, 500_000},
		{"malformed limit", "2.5 USD", 6.8, 1, 500_000},
		{"zero FX", "2.5", 0, 1, 500_000},
		{"negative purchase ratio", "2.5", 6.8, -1, 500_000},
		{"zero quota unit", "2.5", 6.8, 1, 0},
		{"nonfinite quota unit", "2.5", 6.8, 1, math.NaN()},
	} {
		t.Run(test.name, func(t *testing.T) {
			operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
			operation_setting.USDExchangeRate = test.fx
			operation_setting.TopUpPlatformUnitsPerCNY = test.purchaseRatio
			common.QuotaPerUnit = test.quotaPerUnit
			operation_setting.PayMethods = []map[string]string{{
				"name": "Gateway", "type": "custom", "max_topup": test.maximum,
				"max_topup_amount": "999999",
			}}
			public := sanitizedPaymentMethods(operation_setting.PayMethods)
			require.Len(t, public, 1)
			assert.NotContains(t, public[0], "max_topup_amount")
			if test.maximum != "" {
				// A valid duplicate cannot override a malformed entry.
				operation_setting.PayMethods = append(operation_setting.PayMethods,
					map[string]string{"name": "Duplicate", "type": "custom", "max_topup": "10"})
				public = sanitizedPaymentMethods(operation_setting.PayMethods)
				for _, method := range public {
					assert.NotContains(t, method, "max_topup_amount")
				}
				blocked, _ := gin.CreateTestContext(httptest.NewRecorder())
				assert.False(t, requirePaymentMethodTopUpWithinLimit(blocked, "custom", 1))
			}
		})
	}
}

func TestRequirePaymentMethodCreditedQuotaWithinLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withTopUpPricing(t, 6.8, 1)
	withPaymentMethods(t, []map[string]string{{
		"name": "Creem", "type": "creem", "max_topup": "5",
	}})

	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	assert.False(t, requirePaymentMethodCreditedQuotaWithinLimit(
		context,
		"creem",
		int64(35*common.QuotaPerUnit),
	))
	assert.Contains(t, response.Body.String(), "5")
}

func TestRequireTopUpAmountCapacityUsesStoredCreditConversion(t *testing.T) {
	previousDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	require.NoError(t, db.Create(&model.User{
		Id:       7001,
		Username: "topup-capacity-preview",
		Quota:    common.MaxWalletQuota,
		Status:   common.UserStatusEnabled,
	}).Error)

	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	assert.False(t, requireTopUpAmountCapacity(context, 7001, 1))
	assert.Contains(t, response.Body.String(), "上限")
}
