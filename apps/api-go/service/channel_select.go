package service

import (
	"errors"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

type RetryParam struct {
	Ctx         *gin.Context
	TokenGroup  string
	ModelName   string
	RequestPath string
	Retry       *int
	// ExcludedChannelIDs contains channels that failed this request. It is
	// deliberately request-scoped so a transient 503 or a capability mismatch
	// does not permanently disable a channel for unrelated users.
	ExcludedChannelIDs map[int]struct{}
	// PublicRelayOrder is request-scoped user preference for the administrator
	// published pool. It never changes persisted channel priority.
	PublicRelayOrder    []int
	publicRelayLoaded   map[string]bool
	publicRelayDisabled map[string]map[int]struct{}
	publicRelayOrders   map[string][]int
	resetNextTry        bool
}

func (p *RetryParam) GetRetry() int {
	if p.Retry == nil {
		return 0
	}
	return *p.Retry
}

func (p *RetryParam) SetRetry(retry int) {
	p.Retry = &retry
}

func (p *RetryParam) IncreaseRetry() {
	if p.resetNextTry {
		p.resetNextTry = false
		return
	}
	if p.Retry == nil {
		p.Retry = new(int)
	}
	*p.Retry++
}

func (p *RetryParam) ResetRetryNextTry() {
	p.resetNextTry = true
}

// PreserveRetryState lets local capability filtering discard a selection
// without spending or deferring the next upstream retry. Exclusions remain
// request-scoped and are intentionally not restored.
func (p *RetryParam) PreserveRetryState() func() {
	retry, value, reset := p.Retry, p.GetRetry(), p.resetNextTry
	return func() {
		p.Retry, p.resetNextTry = retry, reset
		if retry != nil {
			*retry = value
		}
	}
}

func (p *RetryParam) ExcludeChannel(channelID int) {
	if p == nil || channelID <= 0 {
		return
	}
	if p.ExcludedChannelIDs == nil {
		p.ExcludedChannelIDs = make(map[int]struct{})
	}
	p.ExcludedChannelIDs[channelID] = struct{}{}
}

func (p *RetryParam) publicRelaySelection(group string) (map[int]struct{}, []int) {
	if p == nil || p.Ctx == nil || group == "" {
		return nil, nil
	}
	if p.publicRelayLoaded == nil {
		p.publicRelayLoaded = make(map[string]bool)
		p.publicRelayDisabled = make(map[string]map[int]struct{})
		p.publicRelayOrders = make(map[string][]int)
	}
	if !p.publicRelayLoaded[group] {
		p.publicRelayLoaded[group] = true
		userID := common.GetContextKeyInt(p.Ctx, constant.ContextKeyUserId)
		if userID == 0 {
			userID = p.Ctx.GetInt("id")
		}
		if userID > 0 {
			disabled, ordered, err := model.PublicRelayDisabledChannels(userID, group)
			if err == nil {
				p.publicRelayDisabled[group] = disabled
				p.publicRelayOrders[group] = ordered
			}
		}
	}
	return p.publicRelayDisabled[group], p.publicRelayOrders[group]
}

func mergeExcluded(base, extra map[int]struct{}) map[int]struct{} {
	if len(extra) == 0 {
		return base
	}
	merged := make(map[int]struct{}, len(base)+len(extra))
	for id := range base {
		merged[id] = struct{}{}
	}
	for id := range extra {
		merged[id] = struct{}{}
	}
	return merged
}

// CacheGetRandomSatisfiedChannel tries to get a random channel that satisfies the requirements.
// 尝试获取一个满足要求的随机渠道。
//
// For "auto" tokenGroup with cross-group Retry enabled:
// 对于启用了跨分组重试的 "auto" tokenGroup：
//
//   - Each group will exhaust all its priorities before moving to the next group.
//     每个分组会用完所有优先级后才会切换到下一个分组。
//
//   - Uses ContextKeyAutoGroupIndex to track current group index.
//     使用 ContextKeyAutoGroupIndex 跟踪当前分组索引。
//
//   - Uses ContextKeyAutoGroupRetryIndex to track the global Retry count when current group started.
//     使用 ContextKeyAutoGroupRetryIndex 跟踪当前分组开始时的全局重试次数。
//
//   - priorityRetry = Retry - startRetryIndex, represents the priority level within current group.
//     priorityRetry = Retry - startRetryIndex，表示当前分组内的优先级级别。
//
//   - When GetRandomSatisfiedChannel returns nil (priorities exhausted), moves to next group.
//     当 GetRandomSatisfiedChannel 返回 nil（优先级用完）时，切换到下一个分组。
//
// Example flow (2 groups, each with 2 priorities, RetryTimes=3):
// 示例流程（2个分组，每个有2个优先级，RetryTimes=3）：
//
//	Retry=0: GroupA, priority0 (startRetryIndex=0, priorityRetry=0)
//	         分组A, 优先级0
//
//	Retry=1: GroupA, priority1 (startRetryIndex=0, priorityRetry=1)
//	         分组A, 优先级1
//
//	Retry=2: GroupA exhausted → GroupB, priority0 (startRetryIndex=2, priorityRetry=0)
//	         分组A用完 → 分组B, 优先级0
//
//	Retry=3: GroupB, priority1 (startRetryIndex=2, priorityRetry=1)
//	         分组B, 优先级1
func CacheGetRandomSatisfiedChannel(param *RetryParam) (*model.Channel, string, error) {
	var channel *model.Channel
	var err error
	selectGroup := param.TokenGroup
	userGroup := common.GetContextKeyString(param.Ctx, constant.ContextKeyUserGroup)

	if param.TokenGroup == "auto" {
		autoGroups := GetRequestAutoGroups(param.Ctx, userGroup)
		if len(autoGroups) == 0 {
			return nil, selectGroup, errors.New("auto groups is not enabled")
		}

		// startGroupIndex: the group index to start searching from
		// startGroupIndex: 开始搜索的分组索引
		startGroupIndex := 0
		crossGroupRetry := common.GetContextKeyBool(param.Ctx, constant.ContextKeyTokenCrossGroupRetry)

		if lastGroupIndex, exists := common.GetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex); exists {
			if idx, ok := lastGroupIndex.(int); ok {
				startGroupIndex = idx
			}
		}

		for i := startGroupIndex; i < len(autoGroups); i++ {
			autoGroup := autoGroups[i]
			// Calculate priorityRetry for current group
			// 计算当前分组的 priorityRetry
			priorityRetry := param.GetRetry()
			// If moved to a new group, reset priorityRetry and update startRetryIndex
			// 如果切换到新分组，重置 priorityRetry 并更新 startRetryIndex
			if i > startGroupIndex {
				priorityRetry = 0
			}
			logger.LogDebug(param.Ctx, "Auto selecting group: %s, priorityRetry: %d", autoGroup, priorityRetry)

			disabled, ordered := param.publicRelaySelection(autoGroup)
			channel, _ = model.GetRandomSatisfiedChannelExcluding(autoGroup, param.ModelName, priorityRetry, param.RequestPath, mergeExcluded(param.ExcludedChannelIDs, disabled), ordered)
			if channel == nil {
				// Current group has no available channel for this model, try next group
				// 当前分组没有该模型的可用渠道，尝试下一个分组
				logger.LogDebug(param.Ctx, "No available channel in group %s for model %s at priorityRetry %d, trying next group", autoGroup, param.ModelName, priorityRetry)
				// 重置状态以尝试下一个分组
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i+1)
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupRetryIndex, 0)
				// Reset retry counter so outer loop can continue for next group
				// 重置重试计数器，以便外层循环可以为下一个分组继续
				param.SetRetry(0)
				continue
			}
			common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroup, autoGroup)
			selectGroup = autoGroup
			logger.LogDebug(param.Ctx, "Auto selected group: %s", autoGroup)

			// Prepare state for next retry
			// 为下一次重试准备状态
			if crossGroupRetry && priorityRetry >= common.RetryTimes {
				// Current group has exhausted all retries, prepare to switch to next group
				// This request still uses current group, but next retry will use next group
				// 当前分组已用完所有重试次数，准备切换到下一个分组
				// 本次请求仍使用当前分组，但下次重试将使用下一个分组
				logger.LogDebug(param.Ctx, "Current group %s retries exhausted (priorityRetry=%d >= RetryTimes=%d), preparing switch to next group for next retry", autoGroup, priorityRetry, common.RetryTimes)
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i+1)
				// Reset retry counter so outer loop can continue for next group
				// 重置重试计数器，以便外层循环可以为下一个分组继续
				param.SetRetry(0)
				param.ResetRetryNextTry()
			} else {
				// Stay in current group, save current state
				// 保持在当前分组，保存当前状态
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i)
			}
			break
		}
	} else {
		disabled, ordered := param.publicRelaySelection(param.TokenGroup)
		channel, err = model.GetRandomSatisfiedChannelExcluding(param.TokenGroup, param.ModelName, param.GetRetry(), param.RequestPath, mergeExcluded(param.ExcludedChannelIDs, disabled), ordered)
		if err != nil {
			return nil, param.TokenGroup, err
		}
	}
	return channel, selectGroup, nil
}
