package service

import (
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// BillingSession — 统一计费会话
// ---------------------------------------------------------------------------

// BillingSession 封装单次请求的预扣费/结算/退款生命周期。
// 实现 relaycommon.BillingSettler 接口。
type BillingSession struct {
	relayInfo        *relaycommon.RelayInfo
	funding          FundingSource
	preConsumedQuota int  // 实际预扣额度（信任用户可能为 0）
	tokenConsumed    int  // 令牌额度实际扣减量
	extraReserved    int  // 发送前补充预扣的额度（订阅退款时需要单独回滚）
	trusted          bool // 是否命中信任额度旁路
	// hardBudget only applies to NewBudgetBillingSession. Reservation growth
	// must authorize the complete next window without overdrawing a balance.
	hardBudget          bool
	reservedBudget      int  // complete authorized target, including subscription wallet holds
	fundingSettled      bool // funding.Settle 已成功，资金来源已提交
	settled             bool // Settle 全部完成（资金 + 令牌）
	refunded            bool // Refund 已调用
	subscriptionResult  *model.SubscriptionBillingResult
	settlementAttempted bool
	mu                  sync.Mutex
}

// Settle 根据实际消耗额度进行结算。
// 订阅与令牌在同一事务结算；其他资金来源沿用分步提交语义。
func (s *BillingSession) Settle(actualQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hardBudget {
		if actualQuota < 0 || s.refunded {
			return errors.New("invalid billing budget settlement")
		}
		if err := common.ValidateWalletQuota(actualQuota); err != nil {
			return err
		}
		// Once accepted usage is submitted for settlement, no new upstream
		// window or admission refund may race a failed, retryable settlement.
		s.settlementAttempted = true
	}
	if sub, ok := s.funding.(*SubscriptionFunding); ok && sub.managed {
		if actualQuota < 0 || s.refunded {
			return errors.New("invalid subscription settlement")
		}
		s.settlementAttempted = true
		if s.subscriptionResult == nil {
			s.subscriptionResult = &model.SubscriptionBillingResult{
				RequestId: sub.requestId, Status: "unrecorded", ActualQuota: int64(actualQuota),
				ReservedQuota: int64(s.preConsumedQuota), SubscriptionQuota: int64(s.preConsumedQuota), TokenQuota: int64(s.tokenConsumed),
			}
		}
		result, err := model.SettleSubscriptionBilling(sub.requestId, sub.userId, int64(actualQuota))
		if result != nil {
			s.subscriptionResult = result
		}
		if err != nil {
			return err
		}
		s.relayInfo.SubscriptionPostDelta = result.SubscriptionQuota - int64(s.preConsumedQuota)
		s.fundingSettled, s.settled = true, true
		return nil
	}
	if actualQuota < 0 || s.refunded {
		return errors.New("invalid billing settlement")
	}
	if s.settled {
		return nil
	}
	delta := actualQuota - s.preConsumedQuota
	if delta == 0 {
		s.settled = true
		return nil
	}
	// 1) 调整资金来源（仅在尚未提交时执行，防止重复调用）
	if !s.fundingSettled {
		if err := s.funding.Settle(delta); err != nil {
			return err
		}
		s.fundingSettled = true
	}
	// 2) 调整令牌额度
	var tokenErr error
	if !s.relayInfo.IsPlayground && !s.relayInfo.IsAssistant {
		if delta > 0 {
			tokenErr = model.DecreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, delta)
		} else {
			tokenErr = model.IncreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, -delta)
		}
		if tokenErr != nil {
			// 资金来源已提交，令牌调整失败只能记录日志；标记 settled 防止 Refund 误退资金
			common.SysLog(fmt.Sprintf("error adjusting token quota after funding settled (userId=%d, tokenId=%d, delta=%d): %s",
				s.relayInfo.UserId, s.relayInfo.TokenId, delta, tokenErr.Error()))
		}
	}
	// 3) 更新 relayInfo 上的订阅 PostDelta（用于日志）
	if s.funding.Source() == BillingSourceSubscription {
		s.relayInfo.SubscriptionPostDelta += int64(delta)
	}
	s.settled = true
	return tokenErr
}

// SubscriptionSettlement exposes committed funding amounts and the actual cost
// separately to consumption logging. A failed settlement must not be logged as
// a fully paid request. The durable record is also queryable by request ID.
func (s *BillingSession) SubscriptionSettlement() *model.SubscriptionBillingResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subscriptionResult == nil {
		return nil
	}
	result := *s.subscriptionResult
	return &result
}

// Refund 退还所有预扣费。订阅同步原子退款，其他来源异步执行。
func (s *BillingSession) Refund(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if wallet, ok := s.funding.(*WalletFunding); ok && s.hardBudget {
		if s.settled || s.refunded || s.fundingSettled || s.settlementAttempted {
			return
		}
		if err := wallet.RefundBudget(s.budgetTokenID()); err != nil {
			common.SysLog("error refunding wallet billing budget: " + err.Error())
			return
		}
		s.refunded = true
		return
	}
	if sub, ok := s.funding.(*SubscriptionFunding); ok && sub.managed {
		if s.settled || s.refunded || s.settlementAttempted {
			return
		}
		// Synchronous and atomic: a failed refund remains retryable. The durable
		// record, not the in-memory flag, protects against duplicate credits.
		if err := billingRefundTasks.run(sub.Refund); err != nil {
			common.SysLog("error refunding subscription billing: " + err.Error())
			return
		}
		s.refunded = true
		return
	}
	if s.settled || s.refunded || !s.needsRefundLocked() {
		return
	}

	logger.LogInfo(c, fmt.Sprintf("用户 %d 请求失败, 返还预扣费（token_quota=%s, funding=%s）",
		s.relayInfo.UserId,
		logger.FormatQuota(s.tokenConsumed),
		s.funding.Source(),
	))

	// 复制需要的值到闭包中
	tokenId := s.relayInfo.TokenId
	tokenKey := s.relayInfo.TokenKey
	isPlayground := s.relayInfo.IsPlayground
	isAssistant := s.relayInfo.IsAssistant
	tokenConsumed := s.tokenConsumed
	extraReserved := s.extraReserved
	subscriptionId := s.relayInfo.SubscriptionId
	funding := s.funding

	err := billingRefundTasks.goRun(func() error {
		var result error
		// 1) 退还资金来源
		if err := funding.Refund(); err != nil {
			result = errors.Join(result, err)
			common.SysLog("error refunding billing source: " + err.Error())
		}
		if extraReserved > 0 && funding.Source() == BillingSourceSubscription && subscriptionId > 0 {
			if err := model.PostConsumeUserSubscriptionDelta(subscriptionId, -int64(extraReserved)); err != nil {
				result = errors.Join(result, err)
				common.SysLog("error refunding subscription extra reserved quota: " + err.Error())
			}
		}
		// 2) 退还令牌额度
		if tokenConsumed > 0 && !isPlayground && !isAssistant {
			if err := model.IncreaseTokenQuota(tokenId, tokenKey, tokenConsumed); err != nil {
				result = errors.Join(result, err)
				common.SysLog("error refunding token quota: " + err.Error())
			}
		}
		return result
	}, func(error) { common.SysError("billing refund task failed; financial reconciliation required") })
	if err != nil {
		common.SysError("billing refund not scheduled: " + err.Error())
		return
	}
	s.refunded = true
}

// NeedsRefund 返回是否存在需要退还的预扣状态。
func (s *BillingSession) NeedsRefund() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.needsRefundLocked()
}

func (s *BillingSession) needsRefundLocked() bool {
	if s.settled || s.refunded || s.fundingSettled || s.settlementAttempted {
		// fundingSettled 时资金来源已提交结算，不能再退预扣费
		return false
	}
	if s.tokenConsumed > 0 {
		return true
	}
	if wallet, ok := s.funding.(*WalletFunding); ok && s.hardBudget && wallet.consumed > 0 {
		// Internal requests have no token reservation, but their guarded
		// wallet admission still belongs to the refundable session.
		return true
	}
	// 订阅可能在 tokenConsumed=0 时仍预扣了额度
	if sub, ok := s.funding.(*SubscriptionFunding); ok && sub.preConsumed > 0 {
		return true
	}
	return false
}

// GetPreConsumedQuota 返回实际预扣的额度。
func (s *BillingSession) GetPreConsumedQuota() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.preConsumedQuota
}

// GetReservedBudget returns the complete budget authorized before sending the
// next window. A subscription's GetPreConsumedQuota may include only its grant,
// while its ledger also holds capacity for later wallet overflow.
func (s *BillingSession) GetReservedBudget() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hardBudget {
		return s.reservedBudget
	}
	return s.preConsumedQuota
}

func (s *BillingSession) budgetTokenID() int {
	if s.relayInfo == nil || s.relayInfo.IsPlayground || s.relayInfo.IsAssistant {
		return 0
	}
	return s.relayInfo.TokenId
}

func budgetReservationAPIError(err error) *types.NewAPIError {
	if errors.Is(err, model.ErrSubscriptionBillingTokenQuota) {
		return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	if errors.Is(err, model.ErrWalletBillingBudgetQuota) || errors.Is(err, model.ErrSubscriptionBillingWalletQuota) || errors.Is(err, model.ErrSubscriptionQuotaInsufficient) {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
}

func (s *BillingSession) Reserve(targetQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hardBudget {
		if targetQuota < 0 {
			return errors.New("budget target must not be negative")
		}
		if err := common.ValidateWalletQuota(targetQuota); err != nil {
			return err
		}
		if s.settled || s.refunded || s.fundingSettled || s.settlementAttempted {
			return errors.New("billing budget is no longer reservable")
		}
		if targetQuota <= s.reservedBudget {
			return nil
		}
	}
	if sub, ok := s.funding.(*SubscriptionFunding); ok && sub.managed {
		if s.settlementAttempted || s.refunded {
			return errors.New("subscription billing is no longer reservable")
		}
		result, err := model.ReserveSubscriptionBilling(sub.requestId, sub.userId, int64(targetQuota))
		if err != nil {
			if s.hardBudget {
				return budgetReservationAPIError(err)
			}
			return err
		}
		s.preConsumedQuota = int(result.ReservedQuota)
		s.tokenConsumed = int(result.TokenQuota)
		s.extraReserved = int(result.ReservedQuota - sub.preConsumed)
		if s.hardBudget {
			s.reservedBudget = targetQuota
		}
		s.syncRelayInfo()
		return nil
	}
	if s.hardBudget {
		wallet, ok := s.funding.(*WalletFunding)
		if !ok {
			return errors.New("funding source does not support billing budgets")
		}
		delta := targetQuota - s.reservedBudget
		if err := wallet.ReserveBudget(delta, s.budgetTokenID()); err != nil {
			return budgetReservationAPIError(err)
		}
		s.preConsumedQuota += delta
		if s.budgetTokenID() != 0 {
			s.tokenConsumed += delta
		}
		s.extraReserved += delta
		s.reservedBudget = targetQuota
		s.syncRelayInfo()
		return nil
	}

	if s.settled || s.refunded || s.trusted || targetQuota <= s.preConsumedQuota {
		return nil
	}

	delta := targetQuota - s.preConsumedQuota
	if delta <= 0 {
		return nil
	}

	if err := s.reserveFunding(delta); err != nil {
		return err
	}
	if err := s.reserveToken(delta); err != nil {
		s.rollbackFundingReserve(delta)
		return err
	}

	s.preConsumedQuota += delta
	s.tokenConsumed += delta
	s.extraReserved += delta
	s.syncRelayInfo()
	return nil
}

// ---------------------------------------------------------------------------
// PreConsume — 统一预扣费入口（含信任额度旁路）
// ---------------------------------------------------------------------------

// preConsume 执行预扣费：信任检查 -> 令牌预扣 -> 资金来源预扣。
// 任一步骤失败时原子回滚已完成的步骤。
func (s *BillingSession) preConsume(c *gin.Context, quota int) *types.NewAPIError {
	effectiveQuota := quota

	// ---- 信任额度旁路 ----
	if s.shouldTrust(c) {
		s.trusted = true
		effectiveQuota = 0
		logger.LogInfo(c, fmt.Sprintf("用户 %d 额度充足, 信任且不需要预扣费 (funding=%s)", s.relayInfo.UserId, s.funding.Source()))
	} else if effectiveQuota > 0 {
		logger.LogInfo(c, fmt.Sprintf("用户 %d 需要预扣费 %s (funding=%s)", s.relayInfo.UserId, logger.FormatQuota(effectiveQuota), s.funding.Source()))
	}
	if wallet, ok := s.funding.(*WalletFunding); ok && s.hardBudget {
		if !s.relayInfo.IsPlayground && !s.relayInfo.IsAssistant && s.relayInfo.TokenId <= 0 {
			return types.NewErrorWithStatusCode(errors.New("billing budget token is missing"), types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry())
		}
		if err := wallet.ReserveBudget(effectiveQuota, s.budgetTokenID()); err != nil {
			return budgetReservationAPIError(err)
		}
		s.preConsumedQuota = effectiveQuota
		if s.budgetTokenID() != 0 {
			s.tokenConsumed = effectiveQuota
		}
		s.reservedBudget = effectiveQuota
		s.syncRelayInfo()
		return nil
	}

	// ---- 1) 预扣令牌额度 ----
	sub, subscriptionManaged := s.funding.(*SubscriptionFunding)
	subscriptionManaged = subscriptionManaged && sub.managed
	if effectiveQuota > 0 && !subscriptionManaged {
		if err := PreConsumeTokenQuota(s.relayInfo, effectiveQuota); err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		s.tokenConsumed = effectiveQuota
	}

	// ---- 2) 预扣资金来源 ----
	if err := s.funding.PreConsume(effectiveQuota); err != nil {
		// 预扣费失败，回滚令牌额度
		if s.tokenConsumed > 0 && !s.relayInfo.IsPlayground && !s.relayInfo.IsAssistant {
			if rollbackErr := model.IncreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, s.tokenConsumed); rollbackErr != nil {
				common.SysLog(fmt.Sprintf("error rolling back token quota (userId=%d, tokenId=%d, amount=%d, fundingErr=%s): %s",
					s.relayInfo.UserId, s.relayInfo.TokenId, s.tokenConsumed, err.Error(), rollbackErr.Error()))
			}
			s.tokenConsumed = 0
		}
		if errors.Is(err, model.ErrSubscriptionBillingTokenQuota) {
			return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		if errors.Is(err, model.ErrSubscriptionBillingWalletQuota) {
			return types.NewErrorWithStatusCode(
				fmt.Errorf("订阅与钱包余额不足以预留本次预计费用: %w", err),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		if errors.Is(err, ErrWebDrawingMinimumBalance) {
			return newWebDrawingMinimumBalanceError()
		}
		if errors.Is(err, ErrInsufficientWalletQuota) {
			userQuota, quotaErr := model.GetUserQuota(s.relayInfo.UserId, true)
			if quotaErr != nil {
				userQuota = 0
			}
			return types.NewErrorWithStatusCode(
				fmt.Errorf("用户额度不足, 剩余额度: %s", logger.FormatQuota(userQuota)),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		if errors.Is(err, model.ErrNoActiveSubscription) || errors.Is(err, model.ErrSubscriptionQuotaInsufficient) {
			return types.NewErrorWithStatusCode(fmt.Errorf("订阅额度不足或未配置订阅: %w", err), types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		if errors.Is(err, ErrAssistantBalanceInsufficient) {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}

	s.preConsumedQuota = effectiveQuota
	if subscriptionManaged {
		s.preConsumedQuota = int(sub.preConsumed)
		if sub.tokenId != 0 {
			s.tokenConsumed = int(sub.tokenConsumed)
		}
	}
	if s.hardBudget {
		s.reservedBudget = effectiveQuota
		if subscriptionManaged {
			// A repeated request ID returns its existing ledger admission, not
			// the newly requested amount. Never advertise unreserved capacity.
			s.reservedBudget = int(sub.preConsumed)
			if sub.tokenId != 0 {
				s.reservedBudget = int(sub.tokenConsumed)
			}
			if s.reservedBudget != effectiveQuota {
				return types.NewErrorWithStatusCode(errors.New("subscription billing budget replay mismatch"), types.ErrorCodeInvalidRequest, http.StatusConflict, types.ErrOptionWithSkipRetry())
			}
		}
	}

	// ---- 同步 RelayInfo 兼容字段 ----
	s.syncRelayInfo()

	return nil
}

func (s *BillingSession) reserveFunding(delta int) error {
	switch funding := s.funding.(type) {
	case *WalletFunding:
		// 与结算补扣（SettleBilling 正差额 → WalletFunding.Settle）语义一致：
		// 全额无条件扣减，余额不足的部分记为欠费（余额可为负），不中断请求，
		// 保证日志记录的预扣额度与用户余额的实际变动始终对账一致。
		// DecreaseUserQuota 仅在数据库错误时失败。
		if err := model.DecreaseUserQuota(funding.userId, delta, false); err != nil {
			return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
		}
		funding.consumed += delta
		return nil
	case *SubscriptionFunding:
		if err := model.PostConsumeUserSubscriptionDelta(funding.subscriptionId, int64(delta)); err != nil {
			return types.NewErrorWithStatusCode(
				fmt.Errorf("订阅额度不足或未配置订阅: %s", err.Error()),
				types.ErrorCodeInsufficientUserQuota,
				http.StatusForbidden,
				types.ErrOptionWithSkipRetry(),
				types.ErrOptionWithNoRecordErrorLog(),
			)
		}
		return nil
	case *AssistantFunding:
		return funding.Reserve(delta)
	default:
		return types.NewError(fmt.Errorf("unsupported funding source: %s", s.funding.Source()), types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
}

func (s *BillingSession) rollbackFundingReserve(delta int) {
	switch funding := s.funding.(type) {
	case *WalletFunding:
		if err := model.IncreaseUserQuota(funding.userId, delta, false); err != nil {
			common.SysLog("error rolling back wallet funding reserve: " + err.Error())
		} else {
			funding.consumed -= delta
		}
	case *SubscriptionFunding:
		if err := model.PostConsumeUserSubscriptionDelta(funding.subscriptionId, -int64(delta)); err != nil {
			common.SysLog("error rolling back subscription funding reserve: " + err.Error())
		}
	case *AssistantFunding:
		if err := funding.RollbackReserve(delta); err != nil {
			common.SysLog("error rolling back assistant funding reserve: " + err.Error())
		}
	}
}

func (s *BillingSession) reserveToken(delta int) error {
	if delta <= 0 || s.relayInfo.IsPlayground || s.relayInfo.IsAssistant {
		return nil
	}
	if err := PreConsumeTokenQuota(s.relayInfo, delta); err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	return nil
}

func NewAssistantBillingSession(c *gin.Context, relayInfo *relaycommon.RelayInfo, preConsumedQuota int) (*BillingSession, *types.NewAPIError) {
	if relayInfo == nil {
		return nil, types.NewError(errors.New("relayInfo is nil"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	session := &BillingSession{
		relayInfo: relayInfo,
		funding:   NewAssistantFunding(relayInfo.UserId),
	}
	if apiErr := session.preConsume(c, preConsumedQuota); apiErr != nil {
		return nil, apiErr
	}
	return session, nil
}

// shouldTrust 统一信任额度检查，适用于钱包和订阅。
func (s *BillingSession) shouldTrust(c *gin.Context) bool {
	// 异步任务（ForcePreConsume=true）必须预扣全额，不允许信任旁路
	if s.hardBudget || s.relayInfo.ForcePreConsume {
		return false
	}

	trustQuota := common.GetTrustQuota()
	if trustQuota <= 0 {
		return false
	}

	// 检查令牌是否充足
	tokenTrusted := s.relayInfo.TokenUnlimited
	if !tokenTrusted {
		tokenQuota := c.GetInt("token_quota")
		tokenTrusted = tokenQuota > trustQuota
	}
	if !tokenTrusted {
		return false
	}

	switch s.funding.Source() {
	case BillingSourceWallet:
		// Wallet authorization is finalized by TryReserveUserQuota's guarded DB
		// update. Skipping that reservation would let a stale-high Redis snapshot
		// start upstream work that the durable balance cannot cover.
		return false
	case BillingSourceSubscription:
		// 订阅不能启用信任旁路。原因：
		// 1. PreConsumeUserSubscription 要求 amount>0 来创建预扣记录并锁定订阅
		// 2. SubscriptionFunding.PreConsume 忽略参数，始终用 s.amount 预扣
		// 3. 若信任旁路将 effectiveQuota 设为 0，会导致 preConsumedQuota 与实际订阅预扣不一致
		return false
	default:
		return false
	}
}

// syncRelayInfo 将 BillingSession 的状态同步到 RelayInfo 的兼容字段上。
func (s *BillingSession) syncRelayInfo() {
	info := s.relayInfo
	info.FinalPreConsumedQuota = s.preConsumedQuota
	info.BillingSource = s.funding.Source()

	if sub, ok := s.funding.(*SubscriptionFunding); ok {
		info.SubscriptionId = sub.subscriptionId
		info.SubscriptionPreConsumed = sub.preConsumed + int64(s.extraReserved)
		info.SubscriptionPostDelta = 0
		info.SubscriptionAmountTotal = sub.AmountTotal
		info.SubscriptionAmountUsedAfterPreConsume = sub.AmountUsedAfter + int64(s.extraReserved)
		info.SubscriptionPlanId = sub.PlanId
		info.SubscriptionPlanTitle = sub.PlanTitle
	} else {
		info.SubscriptionId = 0
		info.SubscriptionPreConsumed = 0
	}
}

// ---------------------------------------------------------------------------
// NewBillingSession 工厂 — 根据计费偏好创建会话并处理回退
// ---------------------------------------------------------------------------

func newWebDrawingMinimumBalanceError() *types.NewAPIError {
	return types.NewErrorWithStatusCode(
		ErrWebDrawingMinimumBalance, "WEB_DRAWING_MINIMUM_BALANCE", http.StatusForbidden,
		types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
}

// NewBillingSession 根据用户计费偏好创建 BillingSession，处理 subscription_first / wallet_first 的回退。
func NewBillingSession(c *gin.Context, relayInfo *relaycommon.RelayInfo, preConsumedQuota int) (*BillingSession, *types.NewAPIError) {
	return newBillingSession(c, relayInfo, preConsumedQuota, false)
}

// NewBudgetBillingSession opts an ongoing session into atomic, guarded budget
// reservation. Final settlement still accounts for usage already served, even
// if a provider's final report includes an unexpected trailing overage.
func NewBudgetBillingSession(c *gin.Context, relayInfo *relaycommon.RelayInfo, initialQuota int) (*BillingSession, *types.NewAPIError) {
	if initialQuota < 0 {
		return nil, types.NewErrorWithStatusCode(errors.New("initial billing budget must not be negative"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if err := common.ValidateWalletQuota(initialQuota); err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if initialQuota == 0 && relayInfo != nil && !relayInfo.PriceData.FreeModel {
		// A rounded-down paid estimate still needs funding authorization before
		// upstream work. Only the frozen server-side free-model policy skips it.
		initialQuota = 1
		relayInfo.PriceData.QuotaToPreConsume = 1
	}
	return newBillingSession(c, relayInfo, initialQuota, true)
}

func newBillingSession(c *gin.Context, relayInfo *relaycommon.RelayInfo, preConsumedQuota int, hardBudget bool) (*BillingSession, *types.NewAPIError) {
	if relayInfo == nil {
		return nil, types.NewError(fmt.Errorf("relayInfo is nil"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	minimumQuota := c.GetInt(string(constant.ContextKeyWebDrawingMinimumQuota))
	if minimumQuota > 0 {
		if err := common.ValidateWalletQuota(minimumQuota); err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		balance, err := model.GetUserQuota(relayInfo.UserId, true)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if balance < minimumQuota {
			return nil, newWebDrawingMinimumBalanceError()
		}
	}
	if hardBudget && preConsumedQuota == 0 && relayInfo.PriceData.FreeModel {
		// Free duration sessions need an owner even with a zero wallet. Avoid
		// creating the managed subscription path's one-unit admission record.
		session := &BillingSession{
			relayInfo: relayInfo, hardBudget: true,
			funding: &WalletFunding{userId: relayInfo.UserId, minimumQuota: minimumQuota},
		}
		if apiErr := session.preConsume(c, 0); apiErr != nil {
			return nil, apiErr
		}
		return session, nil
	}

	pref := common.NormalizeBillingPreference(relayInfo.UserSetting.BillingPreference)

	// 钱包路径需要先检查用户额度
	tryWallet := func() (*BillingSession, *types.NewAPIError) {
		userQuota, err := model.GetUserQuota(relayInfo.UserId, true)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if minimumQuota > 0 && userQuota < minimumQuota {
			return nil, newWebDrawingMinimumBalanceError()
		}
		if userQuota <= 0 {
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("用户额度不足, 剩余额度: %s", logger.FormatQuota(userQuota)),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		if userQuota-preConsumedQuota < 0 {
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("预扣费额度失败, 用户剩余额度: %s, 需要预扣费额度: %s", logger.FormatQuota(userQuota), logger.FormatQuota(preConsumedQuota)),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		relayInfo.UserQuota = userQuota

		session := &BillingSession{
			relayInfo:  relayInfo,
			funding:    &WalletFunding{userId: relayInfo.UserId, minimumQuota: minimumQuota},
			hardBudget: hardBudget,
		}
		if apiErr := session.preConsume(c, preConsumedQuota); apiErr != nil {
			return nil, apiErr
		}
		return session, nil
	}

	trySubscription := func() (*BillingSession, *types.NewAPIError) {
		subConsume := int64(preConsumedQuota)
		if subConsume <= 0 {
			subConsume = 1
		}
		session := &BillingSession{
			relayInfo:  relayInfo,
			hardBudget: hardBudget,
			funding: &SubscriptionFunding{
				requestId: relayInfo.RequestId,
				userId:    relayInfo.UserId,
				modelName: relayInfo.OriginModelName,
				amount:    subConsume,
				managed:   true,
				// Async task refunds currently persist only one funding source.
				// Keep their existing no-split policy until task bookkeeping can
				// carry and refund both committed funding amounts.
				walletOverflow: pref == "subscription_first" && relayInfo.TaskRelayInfo == nil && !relayInfo.IsPlayground && (!hardBudget || !relayInfo.IsAssistant),
			},
		}
		if !relayInfo.IsPlayground && !relayInfo.IsAssistant {
			session.funding.(*SubscriptionFunding).tokenId = relayInfo.TokenId
			if relayInfo.TokenId <= 0 {
				return nil, types.NewError(errors.New("subscription billing token is missing"), types.ErrorCodePreConsumeTokenQuotaFailed, types.ErrOptionWithSkipRetry())
			}
		}
		// 必须传 subConsume 而非 preConsumedQuota，保证 SubscriptionFunding.amount、
		// preConsume 参数和 FinalPreConsumedQuota 三者一致，避免订阅多扣费。
		if apiErr := session.preConsume(c, int(subConsume)); apiErr != nil {
			return nil, apiErr
		}
		return session, nil
	}

	switch pref {
	case "subscription_only":
		return trySubscription()
	case "wallet_only":
		return tryWallet()
	case "wallet_first":
		session, err := tryWallet()
		if err != nil {
			if err.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
				return trySubscription()
			}
			return nil, err
		}
		return session, nil
	case "subscription_first":
		fallthrough
	default:
		hasSub, subCheckErr := model.HasActiveUserSubscription(relayInfo.UserId)
		if subCheckErr != nil {
			return nil, types.NewError(subCheckErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if !hasSub {
			return tryWallet()
		}
		session, apiErr := trySubscription()
		if apiErr != nil {
			if errors.Is(apiErr.Err, model.ErrSubscriptionBillingWalletQuota) {
				return nil, apiErr
			}
			if apiErr.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
				// 仅当用户的活跃订阅允许钱包回退时才回退到钱包，否则返回订阅额度不足错误
				allowOverflow, overflowErr := model.UserActiveSubscriptionsAllowWalletOverflow(relayInfo.UserId)
				if overflowErr != nil {
					return nil, types.NewError(overflowErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
				}
				if allowOverflow {
					return tryWallet()
				}
				return nil, apiErr
			}
			return nil, apiErr
		}
		return session, nil
	}
}
