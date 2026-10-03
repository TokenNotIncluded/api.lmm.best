// Copyright (C) 2023-2026 QuantumNous
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeSelfOAuthBindingType(t *testing.T) {
	t.Parallel()

	for _, bindingType := range []string{
		"github", "discord", "oidc", "wechat", "telegram", "linuxdo",
	} {
		bindingType := bindingType
		t.Run(bindingType, func(t *testing.T) {
			t.Parallel()
			got, ok := normalizeSelfOAuthBindingType("  " + bindingType + "  ")
			if !ok || got != bindingType {
				t.Fatalf("normalizeSelfOAuthBindingType() = %q, %v; want %q, true", got, ok, bindingType)
			}
		})
	}
	if got, ok := normalizeSelfOAuthBindingType(" GITHUB "); !ok || got != "github" {
		t.Fatalf("normalizeSelfOAuthBindingType() = %q, %v; want %q, true", got, ok, "github")
	}

	for _, bindingType := range []string{"", "email", "github_id", "password", "../github"} {
		bindingType := bindingType
		t.Run("reject_"+bindingType, func(t *testing.T) {
			t.Parallel()
			if got, ok := normalizeSelfOAuthBindingType(bindingType); ok {
				t.Fatalf("normalizeSelfOAuthBindingType(%q) = %q, true; want rejection", bindingType, got)
			}
		})
	}
}

func performClearSelfOAuthBindingRequest(t *testing.T, userId int, bindingType string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodDelete, "/api/user/bindings/"+bindingType, nil)
	context.Params = gin.Params{{Key: "binding_type", Value: bindingType}}
	context.Set("id", userId)
	ClearSelfOAuthBinding(context)
	return recorder
}

func TestClearSelfOAuthBindingOnlyClearsAllowedOAuthFields(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.ExternalIdentityClaim{}))
	user := model.User{
		Username: "self-oauth-unbind",
		Password: "password",
		Email:    "user@example.test",
		GitHubId: "github-subject",
	}
	require.NoError(t, db.Create(&user).Error)

	response := performClearSelfOAuthBindingRequest(t, user.Id, "github")
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"success":true`)

	var updated model.User
	require.NoError(t, db.First(&updated, user.Id).Error)
	assert.Empty(t, updated.GitHubId)
	assert.Equal(t, user.Email, updated.Email)

	response = performClearSelfOAuthBindingRequest(t, user.Id, "email")
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"success":false`)
	require.NoError(t, db.First(&updated, user.Id).Error)
	assert.Equal(t, user.Email, updated.Email)
}
