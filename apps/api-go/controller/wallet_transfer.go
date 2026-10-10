package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func walletTransferError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errPublicCreditDenominationChanged):
		c.JSON(http.StatusConflict, gin.H{"success": false, "code": "CREDIT_DENOMINATION_CHANGED", "message": "Credit units changed; refresh before transferring"})
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
	var input walletTransferCreateInput
	if decodeStrictJSONRequest(c, &input) != nil {
		walletTransferError(c, model.ErrWalletTransferInvalid)
		return
	}
	basis, err := captureCreditBoundaryBasis()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "code": "CREDIT_UNITS_UNAVAILABLE", "message": "Credit units are unavailable"})
		return
	}
	quota, err := input.ledgerQuota(basis)
	if err != nil {
		walletTransferError(c, err)
		return
	}
	transfer, err := model.CreateWalletTransfer(c.GetInt("id"), quota, input.RequestKey)
	if err != nil {
		walletTransferError(c, err)
		return
	}
	walletTransferOwnerResponse(c, transfer, basis)
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
	basis, err := captureCreditBoundaryBasis()
	if err != nil {
		walletTransferError(c, err)
		return
	}
	responses := make([]walletTransferPublicResponse, 0, len(transfers))
	for i := range transfers {
		response, err := buildWalletTransferPublicResponse(&transfers[i], basis)
		if err != nil {
			walletTransferError(c, err)
			return
		}
		responses = append(responses, response)
	}
	common.ApiSuccess(c, responses)
}

type walletTransferPublicResponse struct {
	*model.WalletTransfer
	common.CreditDenomination
	PublicCreditAmount string `json:"public_credit_amount"`
}

func buildWalletTransferPublicResponse(transfer *model.WalletTransfer, basis creditBoundaryBasis) (walletTransferPublicResponse, error) {
	amount, err := basis.publicAmount(int64(transfer.Quota))
	if err != nil {
		return walletTransferPublicResponse{}, err
	}
	return walletTransferPublicResponse{WalletTransfer: transfer, CreditDenomination: basis.Metadata, PublicCreditAmount: amount.String()}, nil
}

func walletTransferOwnerResponse(c *gin.Context, transfer *model.WalletTransfer, basis creditBoundaryBasis) {
	response, err := buildWalletTransferPublicResponse(transfer, basis)
	if err != nil {
		walletTransferError(c, err)
		return
	}
	common.ApiSuccess(c, response)
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
	basis, err := captureCreditBoundaryBasis()
	if err != nil {
		walletTransferError(c, err)
		return
	}
	walletTransferReceiptWithBasis(c, transfer, basis)
}

func walletTransferReceiptWithBasis(c *gin.Context, transfer *model.WalletTransfer, basis creditBoundaryBasis) {
	amount, err := basis.publicAmount(int64(transfer.Quota))
	if err != nil {
		walletTransferError(c, err)
		return
	}
	data := gin.H{"quota": transfer.Quota, "public_credit_amount": amount.String(), "status": transfer.Status, "created_at": transfer.CreatedAt, "claimed_at": transfer.ClaimedAt, "is_sender": transfer.SenderID == c.GetInt("id"), "claimed_by_me": transfer.RecipientID == c.GetInt("id")}
	basis.addMetadata(data)
	common.ApiSuccess(c, data)
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
	basis, err := captureCreditBoundaryBasis()
	if err != nil {
		walletTransferError(c, err)
		return
	}
	transfer, err := model.ClaimWalletTransfer(token, c.GetInt("id"))
	if err != nil {
		walletTransferError(c, err)
		return
	}
	walletTransferReceiptWithBasis(c, transfer, basis)
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
