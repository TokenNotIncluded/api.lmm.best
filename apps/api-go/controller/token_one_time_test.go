// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package controller

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTokenOneTimeCreationRevealBoundary(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	user := model.User{Username: "one-time-owner", Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&user).Error)
	create, response := newAuthenticatedContext(t, http.MethodPost, "/api/token/", map[string]any{"name": "first device", "unlimited_quota": true, "expired_time": -1, "group": "default", "one_time_reveal": true}, user.Id)
	AddToken(create)
	require.True(t, decodeAPIResponse(t, response).Success)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	var token model.Token
	require.NoError(t, db.Where("user_id = ?", user.Id).First(&token).Error)
	require.True(t, token.OneTimeReveal)
	require.Contains(t, response.Body.String(), token.Key)
	require.Empty(t, token.GetFullKey())
	for _, uid := range []int{user.Id, user.Id + 1} {
		ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/token/key", nil, uid)
		ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(token.Id)}}
		GetTokenKey(ctx)
		require.False(t, decodeAPIResponse(t, rec).Success)
		require.NotContains(t, rec.Body.String(), token.Key)
	}
	old := seedToken(t, db, user.Id, "legacy", "legacy-one-time-fixture")
	keys, err := model.GetTokenKeysByIds([]int{token.Id, old.Id}, user.Id)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.Equal(t, old.Id, keys[0].Id)
	batch, batchResponse := newAuthenticatedContext(t, http.MethodPost, "/api/token/batch/keys", map[string]any{"ids": []int{token.Id, old.Id}}, user.Id)
	GetTokenKeysBatch(batch)
	require.True(t, decodeAPIResponse(t, batchResponse).Success)
	require.NotContains(t, batchResponse.Body.String(), token.Key)
	require.Contains(t, batchResponse.Body.String(), old.Key)
	authToken, err := model.GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	require.Equal(t, token.Id, authToken.Id)

	ctx, rec := newAuthenticatedContext(t, http.MethodGet, "/api/token/detail", nil, user.Id)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(token.Id)}}
	GetToken(ctx)
	require.Contains(t, rec.Body.String(), `"one_time_reveal":true`)
	require.NotContains(t, rec.Body.String(), token.Key)
	// Existing mutation APIs cannot turn the permanent reveal boundary off.
	update, upd := newAuthenticatedContext(t, http.MethodPut, "/api/token/", map[string]any{"id": token.Id, "name": "renamed", "expired_time": -1, "unlimited_quota": true, "one_time_reveal": false}, user.Id)
	UpdateToken(update)
	require.True(t, decodeAPIResponse(t, upd).Success)
	require.NoError(t, db.First(&token, token.Id).Error)
	require.True(t, token.OneTimeReveal)

	denied, deniedResponse := newAuthenticatedContext(t, http.MethodDelete, "/api/token/", nil, user.Id+1)
	denied.Params = gin.Params{{Key: "id", Value: strconv.Itoa(token.Id)}}
	DeleteToken(denied)
	require.False(t, decodeAPIResponse(t, deniedResponse).Success)
	revoke, revoked := newAuthenticatedContext(t, http.MethodDelete, "/api/token/", nil, user.Id)
	revoke.Params = gin.Params{{Key: "id", Value: strconv.Itoa(token.Id)}}
	DeleteToken(revoke)
	require.True(t, decodeAPIResponse(t, revoked).Success)

	_, err = model.GetTokenByKey(token.Key, true)
	require.Error(t, err)
}
