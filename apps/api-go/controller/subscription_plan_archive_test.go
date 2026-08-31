package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminListSubscriptionPlansExcludesArchivedByDefault(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionPlan{}))
	require.NoError(t, db.Create(&[]model.SubscriptionPlan{
		{Id: 1, Title: "Active", DurationUnit: model.SubscriptionDurationMonth, DurationValue: 1},
		{Id: 2, Title: "Archived", DurationUnit: model.SubscriptionDurationMonth, DurationValue: 1, ArchivedAt: 123},
	}).Error)

	list := func(query string) []SubscriptionPlanDTO {
		t.Helper()
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodGet, "/?"+query, nil)
		AdminListSubscriptionPlans(context)

		var response struct {
			Success bool                  `json:"success"`
			Data    []SubscriptionPlanDTO `json:"data"`
		}
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		require.True(t, response.Success)
		return response.Data
	}

	plans := list("")
	require.Len(t, plans, 1)
	require.Zero(t, plans[0].Plan.ArchivedAt)

	plans = list("include_archived=true")
	require.Len(t, plans, 2)
	require.NotZero(t, plans[0].Plan.ArchivedAt)
}

func TestAdminSubscriptionBindHandlersRejectArchivedPlanWithoutMutation(t *testing.T) {
	paymentSetting := operation_setting.GetPaymentSetting()
	original := *paymentSetting
	t.Cleanup(func() { *paymentSetting = original })
	paymentSetting.ComplianceConfirmed = true
	paymentSetting.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion

	for _, test := range []struct {
		name string
		path string
		body string
		run  func(*gin.Context)
	}{
		{
			name: "admin bind",
			path: "/api/subscription/admin/bind",
			body: `{"user_id":7,"plan_id":3}`,
			run:  AdminBindSubscription,
		},
		{
			name: "admin user subscription create",
			path: "/api/subscription/admin/users/7/subscriptions",
			body: `{"plan_id":3}`,
			run: func(context *gin.Context) {
				context.Params = gin.Params{{Key: "id", Value: "7"}}
				AdminCreateUserSubscription(context)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := setupTokenControllerTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.SubscriptionPlan{}, &model.UserSubscription{}))
			require.NoError(t, db.Create(&model.User{
				Id: 7, Username: "archive-bind-user", Password: "password", Status: 1, Group: "default",
			}).Error)
			require.NoError(t, db.Create(&model.SubscriptionPlan{
				Id: 3, Title: "Archived", DurationUnit: model.SubscriptionDurationMonth,
				DurationValue: 1, TotalAmount: 100, UpgradeGroup: "pro", Enabled: true, ArchivedAt: 123,
			}).Error)

			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, test.path, bytes.NewBufferString(test.body))
			context.Request.Header.Set("Content-Type", "application/json")
			test.run(context)

			var response struct {
				Success bool `json:"success"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			require.False(t, response.Success)
			var subscriptionCount int64
			require.NoError(t, db.Model(&model.UserSubscription{}).Count(&subscriptionCount).Error)
			require.Zero(t, subscriptionCount)
			var user model.User
			require.NoError(t, db.First(&user, 7).Error)
			require.Equal(t, "default", user.Group)
		})
	}
}
