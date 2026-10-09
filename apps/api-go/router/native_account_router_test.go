package router

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNativeAccountRouterOptInAndSchemaGate(t *testing.T) {
	previous := model.DB
	model.DB = nil
	t.Cleanup(func() { model.DB = previous })
	t.Setenv("NATIVE_ACCOUNTS_ENABLED", "")
	r := gin.New()
	require.NoError(t, SetNativeAccountRouter(r))
	require.Empty(t, r.Routes())
	t.Setenv("NATIVE_ACCOUNTS_ENABLED", "false")
	require.NoError(t, SetNativeAccountRouter(r))
	require.Empty(t, r.Routes())
	t.Setenv("NATIVE_ACCOUNTS_ENABLED", "TRUE")
	require.Error(t, SetNativeAccountRouter(r))
	require.Empty(t, r.Routes())
	t.Setenv("NATIVE_ACCOUNTS_ENABLED", "true")
	require.Error(t, SetNativeAccountRouter(r))
	require.Empty(t, r.Routes(), "missing schema must not expose partial routes")
}
