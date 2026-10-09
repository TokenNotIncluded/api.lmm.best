package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetStatusAllowsConcurrentOptionsRefresh(t *testing.T) {
	// A regression deadlocks the global mutex, so isolate the real handlers in
	// a bounded child process rather than leaving the rest of the suite stuck.
	const childEnv = "API_STATUS_LOCK_REENTRY_TEST_CHILD"
	if os.Getenv(childEnv) != "1" {
		binary, err := os.Executable()
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestGetStatusAllowsConcurrentOptionsRefresh$", "-test.v")
		cmd.Env = append(os.Environ(), childEnv+"=1")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "status/options child must finish without a lock cycle: %s", output)
		return
	}

	installStatusCurrencyFixture(t)
	preserveCacheRuntimeHooks(t)
	cacheReadinessError = func() error { return nil }
	token := "status-lock-fixture-token"
	user := model.User{Username: "status-lock-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AccessToken: &token}
	require.NoError(t, model.DB.Create(&user).Error)
	require.NoError(t, model.DB.Create(&[]model.Option{
		{Key: "SystemName", Value: "updated-system"},
		{Key: "HeaderNavModules", Value: "updated-navigation"},
	}).Error)
	common.OptionMapRWMutex.Lock()
	previousMap, previousName := common.OptionMap, common.SystemName
	common.OptionMap = map[string]string{"HeaderNavModules": "original-navigation"}
	common.SystemName = "original-system"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap, common.SystemName = previousMap, previousName
		common.OptionMapRWMutex.Unlock()
	})

	enteredRoute, releaseRoute := make(chan struct{}), make(chan struct{})
	assistantConfiguredRouteResolver = func(settings setting.AssistantSettings) (string, string, error) {
		close(enteredRoute)
		<-releaseRoute
		return settings.Group, settings.Model, nil
	}
	status := httptest.NewRecorder()
	statusContext, _ := gin.CreateTestContext(status)
	statusContext.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	statusContext.Request.Header.Set("Authorization", "Bearer "+token)
	statusDone := make(chan struct{})
	go func() { GetStatus(statusContext); close(statusDone) }()
	select {
	case <-enteredRoute:
	case <-time.After(3 * time.Second):
		t.Fatal("status did not reach its route lookup")
	}

	options := httptest.NewRecorder()
	optionsContext, _ := gin.CreateTestContext(options)
	optionsContext.Request = httptest.NewRequest(http.MethodGet, "/api/option/", nil)
	optionsDone := make(chan struct{})
	go func() { GetOptions(optionsContext); close(optionsDone) }()
	// Release the route only after the real options writer has either finished
	// or queued on the mutex. TryRLock fails when a writer is waiting.
	deadline := time.Now().Add(3 * time.Second)
	writerFinished := false
	for {
		select {
		case <-optionsDone:
			writerFinished = true
		default:
		}
		if writerFinished || !common.OptionMapRWMutex.TryRLock() {
			break
		}
		common.OptionMapRWMutex.RUnlock()
		if time.Now().After(deadline) {
			t.Fatal("options refresh neither finished nor queued its writer")
		}
		time.Sleep(time.Millisecond)
	}
	close(releaseRoute)
	select {
	case <-statusDone:
	case <-time.After(3 * time.Second):
		t.Fatal("authenticated status deadlocked with a queued options writer")
	}
	if !writerFinished {
		select {
		case <-optionsDone:
		case <-time.After(3 * time.Second):
			t.Fatal("options refresh remained blocked after status")
		}
	}
	require.Equal(t, http.StatusOK, status.Code)
	require.Equal(t, http.StatusOK, options.Code)
	response := decodeCacheRuntimeResponse(t, status)
	require.True(t, response.Success)
	require.Equal(t, true, response.Data["docs_access"], "the authenticated root still receives its trust-level decision")
	require.Equal(t, "original-system", response.Data["system_name"])
	require.Equal(t, "original-navigation", response.Data["HeaderNavModules"], "configuration fields retain their coherent pre-refresh snapshot")
	require.Contains(t, options.Body.String(), "updated-system")
}
