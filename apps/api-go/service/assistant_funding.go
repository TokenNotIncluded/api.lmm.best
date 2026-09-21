package service

import (
	"errors"
	"fmt"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"gorm.io/gorm"
)

var ErrAssistantBalanceInsufficient = errors.New("super administrator balance is insufficient for AI assistant service")
var ErrAssistantBillingAccountUnavailable = errors.New("enabled super administrator billing account is unavailable")

// AssistantFunding charges every customer-service model call to the enabled
// super administrator selected by the controller.
type AssistantFunding struct {
	userId   int
	consumed int
}

func NewAssistantFunding(userId int) *AssistantFunding {
	return &AssistantFunding{userId: userId}
}

func (a *AssistantFunding) Source() string { return BillingSourceAssistant }

func (a *AssistantFunding) PreConsume(amount int) error {
	return a.reserve(amount, true)
}

func (a *AssistantFunding) Reserve(amount int) error {
	return a.reserve(amount, true)
}

func (a *AssistantFunding) reserve(amount int, enforceWalletBalance bool) error {
	if amount <= 0 {
		return nil
	}
	if model.DB == nil || a.userId <= 0 {
		return ErrAssistantBillingAccountUnavailable
	}

	query := model.DB.Model(&model.User{}).
		Where("id = ? AND role = ? AND status = ? AND deleted_at IS NULL", a.userId, common.RoleRootUser, common.UserStatusEnabled)
	if enforceWalletBalance {
		query = query.Where("quota >= ?", amount)
	}
	result := model.UpdateWalletQuotaByDelta(query, -amount)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		var billingUser model.User
		err := model.DB.Select("id", "quota").
			Where("id = ? AND role = ? AND status = ? AND deleted_at IS NULL", a.userId, common.RoleRootUser, common.UserStatusEnabled).
			First(&billingUser).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAssistantBillingAccountUnavailable
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("%w: remaining=%d required=%d", ErrAssistantBalanceInsufficient, billingUser.Quota, amount)
	}
	if err := model.InvalidateUserCache(a.userId); err != nil {
		common.SysLog("failed to invalidate assistant billing account cache: " + err.Error())
	}
	a.consumed += amount
	return nil
}

func (a *AssistantFunding) Settle(delta int) error {
	if delta > 0 {
		// Post-settlement follows normal relay semantics and records overage even
		// when the wallet crosses zero, rather than dropping already-served usage.
		return a.reserve(delta, false)
	}
	if delta < 0 {
		return a.release(-delta)
	}
	return nil
}

func (a *AssistantFunding) Refund() error {
	return a.release(a.consumed)
}

func (a *AssistantFunding) RollbackReserve(amount int) error {
	return a.release(amount)
}

// release refunds quota to the same super-administrator wallet that funded
// the request.
func (a *AssistantFunding) release(amount int) error {
	if amount <= 0 {
		return nil
	}
	if amount > a.consumed {
		return fmt.Errorf("assistant funding refund exceeds consumption: refund=%d consumed=%d", amount, a.consumed)
	}
	if model.DB == nil || a.userId <= 0 {
		return ErrAssistantBillingAccountUnavailable
	}
	result := model.UpdateWalletQuotaByDelta(
		model.DB.Unscoped().Model(&model.User{}).Where("id = ?", a.userId),
		amount,
	)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrAssistantBillingAccountUnavailable
	}
	if err := model.InvalidateUserCache(a.userId); err != nil {
		common.SysLog("failed to invalidate assistant billing account cache after refund: " + err.Error())
	}
	a.consumed -= amount
	return nil
}
