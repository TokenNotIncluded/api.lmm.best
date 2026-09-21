package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDeleteSelfPreservesNilUserLookupError(t *testing.T) {
	// The real loader returns a nil user and a non-not-found error for ID zero.
	user, err := model.GetUserById(0, false)
	require.Nil(t, user)
	require.Error(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/user/self", nil)
	c.Set("id", 0)
	DeleteSelf(c)
	require.Equal(t, http.StatusOK, recorder.Code) // Existing ApiError envelope.
	require.Contains(t, recorder.Body.String(), err.Error())
	require.Contains(t, recorder.Body.String(), `"success":false`)
}
