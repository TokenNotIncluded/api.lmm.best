package controller

import (
	"errors"
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func walletTransferError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrWalletTransferBalance):
		common.ApiErrorMsg(c, "Insufficient wallet balance")
	case errors.Is(err, model.ErrWalletTransferInvalid):
		common.ApiErrorMsg(c, "Invalid transfer amount or request")
	case errors.Is(err, model.ErrWalletTransferUnavailable):
		common.ApiErrorMsg(c, "Transfer is unavailable or already claimed")
	case errors.Is(err, model.ErrWalletQuotaOutOfRange):
		common.ApiErrorMsg(c, "Wallet balance would exceed the safe range")
	default:
		common.SysError("wallet transfer database operation failed")
		common.ApiErrorMsg(c, "Unable to complete transfer. Please retry.")
	}
}

func CreateWalletTransfer(c *gin.Context) {
	var input struct {
		Quota      int    `json:"quota"`
		RequestKey string `json:"request_key"`
	}
	if c.ShouldBindJSON(&input) != nil {
		walletTransferError(c, model.ErrWalletTransferInvalid)
		return
	}
	transfer, err := model.CreateWalletTransfer(c.GetInt("id"), input.Quota, input.RequestKey)
	if err != nil {
		walletTransferError(c, err)
		return
	}
	common.ApiSuccess(c, transfer)
}

func ListWalletTransfers(c *gin.Context) {
	before, err := strconv.Atoi(c.DefaultQuery("before", "0"))
	if err != nil || before < 0 {
		walletTransferError(c, model.ErrWalletTransferInvalid)
		return
	}
	transfers, err := model.ListWalletTransfers(c.GetInt("id"), before)
	if err != nil {
		walletTransferError(c, err)
		return
	}
	common.ApiSuccess(c, transfers)
}

func walletTransferToken(c *gin.Context) (string, bool) {
	var input struct {
		Token string `json:"token"`
	}
	if c.ShouldBindJSON(&input) != nil {
		walletTransferError(c, model.ErrWalletTransferInvalid)
		return "", false
	}
	return input.Token, true
}

func walletTransferReceipt(c *gin.Context, transfer *model.WalletTransfer) {
	// Recipient identity and the bearer credential never leave owner history.
	common.ApiSuccess(c, gin.H{"quota": transfer.Quota, "status": transfer.Status, "created_at": transfer.CreatedAt, "claimed_at": transfer.ClaimedAt, "is_sender": transfer.SenderID == c.GetInt("id"), "claimed_by_me": transfer.RecipientID == c.GetInt("id")})
}

func InspectWalletTransfer(c *gin.Context) {
	token, ok := walletTransferToken(c)
	if !ok {
		return
	}
	transfer, err := model.InspectWalletTransfer(token)
	if err != nil {
		walletTransferError(c, err)
		return
	}
	walletTransferReceipt(c, transfer)
}

func ClaimWalletTransfer(c *gin.Context) {
	token, ok := walletTransferToken(c)
	if !ok {
		return
	}
	transfer, err := model.ClaimWalletTransfer(token, c.GetInt("id"))
	if err != nil {
		walletTransferError(c, err)
		return
	}
	walletTransferReceipt(c, transfer)
}

func CancelWalletTransfer(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		walletTransferError(c, model.ErrWalletTransferInvalid)
		return
	}
	if err := model.CancelWalletTransfer(id, c.GetInt("id")); err != nil {
		walletTransferError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
