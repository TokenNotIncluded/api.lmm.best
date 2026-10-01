package controller

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWalletTransferReceiptDoesNotExposeIdentityOrBearerToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(writer)
	context.Set("id", 7)
	walletTransferReceipt(context, &model.WalletTransfer{SenderID: 1, RecipientID: 7, Token: "private-credential", Quota: 123, Status: "claimed", RecipientEmail: "private@example.test", RecipientName: "Private", RecipientUsername: "private-user"})
	var body struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(writer.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.Equal(t, true, body.Data["claimed_by_me"])
	require.Equal(t, false, body.Data["is_sender"])
	for _, field := range []string{"token", "sender_id", "recipient_id", "recipient_email", "recipient_name", "recipient_username"} {
		require.NotContains(t, body.Data, field)
	}
	require.NotContains(t, writer.Body.String(), "private")
}
