package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	qrcode "github.com/skip2/go-qrcode"
)

const walletMCPMaxTopupAmount = 1_000_000

type walletMCPOutput struct {
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type walletMCPTopupInput struct {
	Amount int `json:"amount" jsonschema:"Positive whole platform-credit amount, at most 1000000. This only prefills the official wallet; the user must choose and confirm payment there."`
}

type walletMCPTransferInput struct {
	Quota int `json:"quota" jsonschema:"Positive integer wallet quota units to hold for the recipient. Inspect wallet.balance for quota_per_platform_credit. The exact amount requires user confirmation."`
}

type walletMCPCancelInput struct {
	TransferID int `json:"transfer_id" jsonschema:"Identifier of your own pending wallet transfer to cancel and refund."`
}

type walletMCPListInput struct {
	BeforeID int `json:"before_id,omitempty" jsonschema:"Optional positive cursor; return at most 50 of your own transfers."`
}

type walletMCPTransferView struct {
	ID          int    `json:"id"`
	Quota       int    `json:"quota"`
	Status      string `json:"status"`
	CreatedAt   int64  `json:"created_at"`
	ClaimedAt   int64  `json:"claimed_at,omitempty"`
	CancelledAt int64  `json:"cancelled_at,omitempty"`
	ShareURL    string `json:"share_url,omitempty"`
}

func walletMCPActor(request *mcp.CallToolRequest, write bool) (*model.User, error) {
	userID, err := bountyMCPUserId(request)
	if err != nil {
		return nil, errors.New("wallet MCP authentication is required")
	}
	extra := request.Extra.TokenInfo.Extra
	builtin, _ := extra["market_builtin"].(bool)
	granted, _ := extra["market_tool_grant"].(bool)
	walletWrite, _ := extra["wallet_write"].(bool)
	if !builtin || !granted || (write && !walletWrite) {
		return nil, errors.New("authorize this exact wallet tool in the tool market before calling it")
	}
	var user model.User
	if model.DB == nil || model.DB.Select("id", "quota", "auth_version").Where("id = ? AND status = ?", userID, common.UserStatusEnabled).First(&user).Error != nil {
		return nil, errors.New("wallet account is unavailable")
	}
	return &user, nil
}

func walletMCPError(err error) error {
	if err == nil {
		return nil
	}
	for _, safe := range []error{model.ErrWalletTransferInvalid, model.ErrWalletTransferUnavailable, model.ErrWalletTransferBalance, model.ErrWalletQuotaOutOfRange} {
		if errors.Is(err, safe) {
			return errors.New(safe.Error())
		}
	}
	return errors.New("wallet operation could not be completed; check the amount, confirmation and current account access")
}

// Only the configured console origin may receive a wallet credential. Never
// derive URLs from request Host, arguments, an upstream tool or a QR service.
func walletMCPURL(path string, query url.Values, fragment string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(system_setting.ServerAddress))
	if err != nil || base.Scheme != "https" || base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") || len(base.String()) > 512 {
		return "", errors.New("configure a valid HTTPS console origin before generating wallet links")
	}
	base.Path, base.RawPath, base.RawQuery, base.Fragment = path, "", query.Encode(), fragment
	return base.String(), nil
}

func walletMCPView(transfer model.WalletTransfer) (walletMCPTransferView, error) {
	view := walletMCPTransferView{ID: transfer.Id, Quota: transfer.Quota, Status: transfer.Status, CreatedAt: transfer.CreatedAt, ClaimedAt: transfer.ClaimedAt, CancelledAt: transfer.CancelledAt}
	if transfer.Status == "pending" {
		var err error
		view.ShareURL, err = walletMCPURL("/transfer", nil, transfer.Token)
		if err != nil {
			return walletMCPTransferView{}, err
		}
	}
	return view, nil
}

func walletMCPConfirmationPayload(request *mcp.CallToolRequest, user *model.User, input any) (map[string]any, string, error) {
	extra := request.Extra.TokenInfo.Extra
	requestID, _ := extra["market_request_id"].(string)
	clientID, _ := extra["market_client_id"].(string)
	if len(requestID) < 16 || len(requestID) > 64 || strings.TrimSpace(requestID) != requestID || clientID == "" || len(clientID) > 128 {
		return nil, "", errors.New("wallet operation requires a verified market request and client")
	}
	payload := map[string]any{"input": input, "user_id": user.Id, "auth_version": user.AuthVersion, "request_id": requestID, "client_id": clientID}
	for _, key := range []string{"market_tool_id", "market_version_id", "market_grant_id"} {
		value, _ := extra[key].(string)
		if value == "" {
			return nil, "", errors.New("wallet operation requires a verified tool version and grant")
		}
		payload[key] = value
	}
	return payload, requestID, nil
}

func walletMCPLinkResult(output walletMCPOutput, link string) (*mcp.CallToolResult, walletMCPOutput, error) {
	png, err := qrcode.Encode(link, qrcode.Medium, 512)
	if err != nil {
		return nil, walletMCPOutput{}, errors.New("wallet link QR generation failed")
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return nil, walletMCPOutput{}, errors.New("wallet link result encoding failed")
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}, &mcp.ImageContent{Data: png, MIMEType: "image/png"}}}, output, nil
}

func registerWalletMCPTools(server *mcp.Server) {
	addToolMarketBuiltinMCPTool(server, bountyMCPTool("wallet.balance", "Read your wallet balance", "Read only the authenticated account's available wallet quota. This MCP tool costs zero; transfers and drawing-model usage are separate.", true, false, true),
		func(ctx context.Context, request *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, walletMCPOutput, error) {
			user, err := walletMCPActor(request, false)
			if err != nil {
				return nil, walletMCPOutput{}, err
			}
			if math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) || common.QuotaPerUnit <= 0 {
				return nil, walletMCPOutput{}, errors.New("wallet unit configuration is unavailable")
			}
			return nil, walletMCPOutput{Message: "Current available wallet balance. No charge.", Data: map[string]any{"available_quota": user.Quota, "quota_per_platform_credit": common.QuotaPerUnit, "tool_price_quota": 0}}, nil
		})

	addToolMarketBuiltinMCPTool(server, bountyMCPTool("wallet.topup_link", "Generate an official top-up link and QR", "Open the official wallet with a bounded whole platform-credit amount prefilled. The user chooses a payment method and confirms there; this is not a payment-provider checkout, successful payment or balance credit. The MCP call is free.", true, false, true),
		func(ctx context.Context, request *mcp.CallToolRequest, input walletMCPTopupInput) (*mcp.CallToolResult, walletMCPOutput, error) {
			if _, err := walletMCPActor(request, false); err != nil {
				return nil, walletMCPOutput{}, err
			}
			if input.Amount < 1 || input.Amount > walletMCPMaxTopupAmount {
				return nil, walletMCPOutput{}, model.ErrWalletTransferInvalid
			}
			link, err := walletMCPURL("/wallet", url.Values{"topup_amount": {strconv.Itoa(input.Amount)}}, "")
			if err != nil {
				return nil, walletMCPOutput{}, err
			}
			return walletMCPLinkResult(walletMCPOutput{Message: "Review the amount and choose a payment method in your wallet. No payment has been created or charged.", Data: map[string]any{"url": link, "platform_credit_amount": input.Amount, "payment_confirmation_required": true, "tool_price_quota": 0}}, link)
		})

	addToolMarketBuiltinMCPTool(server, bountyMCPTool("wallet.transfers.list", "Read your wallet transfers", "Read up to 50 of your own transfer statuses. Pending links and QR codes are bearer credentials: share only with the intended recipient. Recipient contact details are not returned.", true, false, true),
		func(ctx context.Context, request *mcp.CallToolRequest, input walletMCPListInput) (*mcp.CallToolResult, walletMCPOutput, error) {
			user, err := walletMCPActor(request, false)
			if err != nil {
				return nil, walletMCPOutput{}, err
			}
			if input.BeforeID < 0 {
				return nil, walletMCPOutput{}, model.ErrWalletTransferInvalid
			}
			rows, err := model.ListWalletTransfers(user.Id, input.BeforeID)
			if err != nil {
				return nil, walletMCPOutput{}, walletMCPError(err)
			}
			views := make([]walletMCPTransferView, 0, len(rows))
			for _, row := range rows {
				view, err := walletMCPView(row)
				if err != nil {
					return nil, walletMCPOutput{}, err
				}
				views = append(views, view)
			}
			return nil, walletMCPOutput{Message: "Your own wallet transfers. Share pending links privately.", Data: views}, nil
		})

	addToolMarketBuiltinMCPTool(server, bountyMCPTool("wallet.transfer.create", "Create a confirmed transfer link and QR", "After an exact tool grant and explicit user confirmation, hold the specified amount from your wallet and create a private recipient link. Anyone with the link can claim it; share only with the intended recipient. The MCP fee is zero; the held transfer amount is real wallet balance. Reuse only the original market request to retry.", false, true, true),
		func(ctx context.Context, request *mcp.CallToolRequest, input walletMCPTransferInput) (*mcp.CallToolResult, walletMCPOutput, error) {
			user, err := walletMCPActor(request, true)
			if err != nil {
				return nil, walletMCPOutput{}, err
			}
			if input.Quota <= 0 || common.ValidateWalletQuota(input.Quota) != nil {
				return nil, walletMCPOutput{}, model.ErrWalletTransferInvalid
			}
			// Validate the console origin before creating a hold.
			if _, err := walletMCPURL("/transfer", nil, ""); err != nil {
				return nil, walletMCPOutput{}, err
			}
			payload, requestKey, err := walletMCPConfirmationPayload(request, user, input)
			if err != nil {
				return nil, walletMCPOutput{}, err
			}
			message := fmt.Sprintf("Hold exactly %d wallet quota units from account %d and create a private transfer link? Available balance: %d quota. Anyone with this link or QR can claim the held amount. The MCP tool fee is 0; the transfer amount is held until claimed or cancelled.", input.Quota, user.Id, user.Quota)
			pending, operation, err := bountyMCPConfirmedOperation(request, user.Id, "wallet.transfer.create", payload, message)
			if err != nil || pending != nil {
				return pending, walletMCPOutput{}, err
			}
			transfer, err := model.CreateWalletTransferWithMCPConfirmation(user.Id, input.Quota, requestKey, user.AuthVersion, *operation)
			if err != nil {
				return nil, walletMCPOutput{}, walletMCPError(err)
			}
			view, err := walletMCPView(*transfer)
			if err != nil {
				return nil, walletMCPOutput{}, err
			}
			output := walletMCPOutput{Message: "Transfer status shown below. Share a pending link privately; the wallet has not been credited to a recipient until claimed.", Data: view}
			if view.ShareURL == "" {
				return nil, output, nil
			}
			return walletMCPLinkResult(output, view.ShareURL)
		})

	addToolMarketBuiltinMCPTool(server, bountyMCPTool("wallet.transfer.cancel", "Cancel and refund your pending transfer", "After explicit user confirmation, cancel only your own unclaimed transfer and return the held amount to your wallet. Claimed transfers cannot be cancelled. The MCP tool is free.", false, true, true),
		func(ctx context.Context, request *mcp.CallToolRequest, input walletMCPCancelInput) (*mcp.CallToolResult, walletMCPOutput, error) {
			user, err := walletMCPActor(request, true)
			if err != nil {
				return nil, walletMCPOutput{}, err
			}
			var transfer model.WalletTransfer
			if input.TransferID <= 0 || model.DB.Select("id", "sender_id", "quota", "created_at").Where("id = ? AND sender_id = ?", input.TransferID, user.Id).First(&transfer).Error != nil {
				return nil, walletMCPOutput{}, model.ErrWalletTransferUnavailable
			}
			payload, _, err := walletMCPConfirmationPayload(request, user, map[string]any{"input": input, "quota": transfer.Quota, "created_at": transfer.CreatedAt})
			if err != nil {
				return nil, walletMCPOutput{}, err
			}
			message := fmt.Sprintf("Cancel your transfer %d and refund exactly %d wallet quota units to account %d? This succeeds only if it is still unclaimed.", transfer.Id, transfer.Quota, user.Id)
			pending, operation, err := bountyMCPConfirmedOperation(request, user.Id, "wallet.transfer.cancel", payload, message)
			if err != nil || pending != nil {
				return pending, walletMCPOutput{}, err
			}
			err = model.CancelWalletTransferWithMCPConfirmation(transfer.Id, user.Id, user.AuthVersion, *operation)
			return nil, walletMCPOutput{Message: "Your transfer was cancelled and its held amount refunded.", Data: map[string]any{"transfer_id": transfer.Id, "refunded_quota": transfer.Quota, "tool_price_quota": 0}}, walletMCPError(err)
		})
}
