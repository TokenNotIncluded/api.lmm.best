package model

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	nonSecureRand "math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"gorm.io/gorm"
)

var group2model2channels map[string]map[string][]int // enabled channel
var channelsIDM map[int]*Channel                     // all channels include disabled
// channel2advancedCustomConfig caches parsed Advanced Custom (type 58) configs so
// path-aware selection avoids re-parsing JSON per request. Refreshed on full sync.
var channel2advancedCustomConfig map[int]*dto.AdvancedCustomConfig
var channelSyncLock sync.RWMutex
var channelRefreshLock sync.Mutex
var channelCacheReady bool
var channelCacheLastError error
var channelRefreshHook func()
var channelAfterChannelsQueryHook func()
var channelContextHook func() (context.Context, context.CancelFunc)

const channelCacheSyncTimeout = 10 * time.Second

func InitChannelCache() error {
	if !common.MemoryCacheEnabled {
		InvalidatePricingCache()
		return nil
	}
	if err := refreshChannelCache(); err != nil {
		common.SysLog(fmt.Sprintf("failed to sync channels from database: %v", err))
		return err
	}
	// Publish the complete channel snapshot before invalidating pricing. Pricing
	// invalidation is generation-based and never waits for a pricing rebuild.
	InvalidatePricingCache()
	common.SysLog("channels synced from database")
	return nil
}

// refreshChannelCache serializes the complete fetch/build/publish operation so
// two refreshes cannot overlap or publish out of order. Request readers only
// use channelSyncLock and therefore never wait on the database queries.
func refreshChannelCache() error {
	channelRefreshLock.Lock()
	defer channelRefreshLock.Unlock()

	newChannelId2channel := make(map[int]*Channel)
	newChannel2advancedCustomConfig := make(map[int]*dto.AdvancedCustomConfig)
	ctx, cancel := context.WithTimeout(context.Background(), channelCacheSyncTimeout)
	if channelContextHook != nil {
		cancel()
		ctx, cancel = channelContextHook()
	}
	defer cancel()

	var tx *gorm.DB
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		tx = DB.WithContext(ctx).Begin(&sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	} else {
		tx = DB.WithContext(ctx).Begin()
	}
	if tx.Error != nil {
		err := fmt.Errorf("begin channel cache snapshot transaction: %w", tx.Error)
		recordChannelCacheRefreshError(err)
		return err
	}
	defer tx.Rollback()

	var channels []*Channel
	if err := tx.Find(&channels).Error; err != nil {
		err = fmt.Errorf("load channels: %w", err)
		recordChannelCacheRefreshError(err)
		return err
	}
	if channelAfterChannelsQueryHook != nil {
		channelAfterChannelsQueryHook()
	}
	for _, channel := range channels {
		newChannelId2channel[channel.Id] = channel
		if channel.Type == constant.ChannelTypeAdvancedCustom {
			if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
				newChannel2advancedCustomConfig[channel.Id] = config
			}
		}
	}
	// Only the group names are needed to initialize the routing index. Loading
	// every ability row here made a cache refresh retain a duplicate in-memory
	// copy of the full ability table, which is particularly expensive for a
	// large channel/model catalog.
	var abilityGroups []string
	if err := tx.Model(&Ability{}).Distinct("group").Pluck("group", &abilityGroups).Error; err != nil {
		err = fmt.Errorf("load ability groups: %w", err)
		recordChannelCacheRefreshError(err)
		return err
	}
	if err := tx.Commit().Error; err != nil {
		err = fmt.Errorf("commit channel cache snapshot transaction: %w", err)
		recordChannelCacheRefreshError(err)
		return err
	}
	groups := make(map[string]bool, len(abilityGroups))
	for _, group := range abilityGroups {
		groups[group] = true
	}
	newGroup2model2channels := make(map[string]map[string][]int)
	for group := range groups {
		newGroup2model2channels[group] = make(map[string][]int)
	}
	for _, channel := range channels {
		if channel.Status != common.ChannelStatusEnabled {
			continue // skip disabled channels
		}
		groups := strings.Split(channel.Group, ",")
		for _, group := range groups {
			models := strings.Split(channel.Models, ",")
			for _, model := range models {
				if _, ok := newGroup2model2channels[group][model]; !ok {
					newGroup2model2channels[group][model] = make([]int, 0)
				}
				newGroup2model2channels[group][model] = append(newGroup2model2channels[group][model], channel.Id)
			}
		}
	}

	// sort by priority
	for group, model2channels := range newGroup2model2channels {
		for model, channels := range model2channels {
			sort.Slice(channels, func(i, j int) bool {
				return newChannelId2channel[channels[i]].GetPriority() > newChannelId2channel[channels[j]].GetPriority()
			})
			newGroup2model2channels[group][model] = channels
		}
	}

	if channelRefreshHook != nil {
		channelRefreshHook()
	}

	channelSyncLock.Lock()
	group2model2channels = newGroup2model2channels
	for id, channel := range newChannelId2channel {
		channel = cloneChannel(channel)
		if channel.ChannelInfo.IsMultiKey {
			channel.Keys = channel.GetKeys()
			getChannelRuntimeState(id, channel.ChannelInfo.MultiKeyPollingIndex)
		}
		newChannelId2channel[id] = channel
	}
	channelsIDM = newChannelId2channel
	channel2advancedCustomConfig = newChannel2advancedCustomConfig
	channelCacheReady = true
	channelCacheLastError = nil
	channelSyncLock.Unlock()
	pruneChannelRuntimeStates(newChannelId2channel)
	return nil
}

func recordChannelCacheRefreshError(err error) {
	channelSyncLock.Lock()
	channelCacheLastError = err
	channelSyncLock.Unlock()
}

func channelCacheNotReadyErrorLocked() error {
	if channelCacheLastError != nil {
		return fmt.Errorf("channel cache is not ready: %w", channelCacheLastError)
	}
	return errors.New("channel cache is not ready")
}

func SyncChannelCache(frequency int) {
	SyncChannelCacheContext(context.Background(), frequency)
}

func SyncChannelCacheContext(ctx context.Context, frequency int) {
	if frequency <= 0 {
		return
	}
	ticker := time.NewTicker(time.Duration(frequency) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			common.SysLog("syncing channels from database")
			_ = InitChannelCache()
		}
	}
}

func GetRandomSatisfiedChannel(group string, model string, retry int, requestPath string) (*Channel, error) {
	return GetRandomSatisfiedChannelExcluding(group, model, retry, requestPath, nil)
}

// GetRandomSatisfiedChannelExcluding is the request-scoped variant of channel
// selection. Exclusions are used after an upstream failure so a retry cannot
// immediately select the same unhealthy/capability-mismatched channel. The
// caller owns the map and it is never persisted to the channel cache.
func GetRandomSatisfiedChannelExcluding(group string, model string, retry int, requestPath string, excluded map[int]struct{}, preferred ...[]int) (*Channel, error) {
	// if memory cache is disabled, get channel directly from database
	if !common.MemoryCacheEnabled {
		return GetChannelExcluding(group, model, retry, requestPath, excluded, preferred...)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	if !channelCacheReady {
		return nil, channelCacheNotReadyErrorLocked()
	}

	// First, try to find channels with the exact model name.
	channels := filterChannelsByRequestPathAndModel(group2model2channels[group][model], requestPath, model)

	// If no channels found, try to find channels with the normalized model name.
	if len(channels) == 0 {
		normalizedModel := ratio_setting.FormatMatchingModelName(model)
		channels = filterChannelsByRequestPathAndModel(group2model2channels[group][normalizedModel], requestPath, model)
	}
	if len(excluded) > 0 {
		filtered := make([]int, 0, len(channels))
		for _, channelID := range channels {
			if _, skip := excluded[channelID]; !skip {
				filtered = append(filtered, channelID)
			}
		}
		channels = filtered
	}

	if len(channels) == 0 {
		return nil, nil
	}

	if len(channels) == 1 {
		if channel, ok := channelsIDM[channels[0]]; ok {
			return cloneChannel(channel), nil
		}
		return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channels[0])
	}

	if retry < 0 {
		retry = 0
	}
	var targetPriority int64
	foundPriority := false
	for level := 0; level <= retry; level++ {
		var next int64
		foundNext := false
		for _, channelID := range channels {
			channel, ok := channelsIDM[channelID]
			if !ok {
				return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelID)
			}
			priority := channel.GetPriority()
			if foundPriority && priority >= targetPriority {
				continue
			}
			if !foundNext || priority > next {
				next = priority
				foundNext = true
			}
		}
		if !foundNext {
			break
		}
		targetPriority = next
		foundPriority = true
	}

	sumWeight := 0
	targetCount := 0
	for _, channelID := range channels {
		channel := channelsIDM[channelID]
		if channel.GetPriority() == targetPriority {
			sumWeight += channel.GetWeight()
			targetCount++
		}
	}
	if targetCount == 0 {
		return nil, fmt.Errorf("no channel found, group: %s, model: %s, priority: %d", group, model, targetPriority)
	}
	if len(preferred) > 0 {
		for _, preferredID := range preferred[0] {
			for _, channelID := range channels {
				if channelID == preferredID && channelsIDM[channelID].GetPriority() == targetPriority {
					return cloneChannel(channelsIDM[channelID]), nil
				}
			}
		}
	}

	// smoothing factor and adjustment
	smoothingFactor := 1
	smoothingAdjustment := 0

	if sumWeight == 0 {
		// when all channels have weight 0, set sumWeight to the number of channels and set smoothing adjustment to 100
		// each channel's effective weight = 100
		sumWeight = targetCount * 100
		smoothingAdjustment = 100
	} else if sumWeight/targetCount < 10 {
		// when the average weight is less than 10, set smoothing factor to 100
		smoothingFactor = 100
	}

	// Calculate the total weight of all channels up to endIdx
	totalWeight := sumWeight * smoothingFactor

	// Non-security weighted sampling balances requests across upstream channels.
	randomWeight := nonSecureRand.IntN(totalWeight)

	// Find a channel based on its weight
	for _, channelID := range channels {
		channel := channelsIDM[channelID]
		if channel.GetPriority() != targetPriority {
			continue
		}
		randomWeight -= channel.GetWeight()*smoothingFactor + smoothingAdjustment
		if randomWeight < 0 {
			return cloneChannel(channel), nil
		}
	}
	// return null if no channel is not found
	return nil, errors.New("channel not found")
}

// filterChannelsByRequestPathAndModel restricts candidates by request path and
// model. Only Advanced Custom (type 58) channels are path-checked: they are kept
// only when one of their configured routes matches requestPath and model. All
// other channel types always pass. When requestPath is empty, filtering is skipped.
// Caller must hold channelSyncLock (read lock). The cached slice is never mutated.
func filterChannelsByRequestPathAndModel(channels []int, requestPath string, model string) []int {
	if requestPath == "" || len(channels) == 0 {
		return channels
	}
	var filtered []int
	for index, channelId := range channels {
		channel, ok := channelsIDM[channelId]
		if !ok {
			if filtered != nil {
				filtered = append(filtered, channelId)
			}
			continue
		}
		keep := channel.Type != constant.ChannelTypeAdvancedCustom
		if !keep {
			config := channel2advancedCustomConfig[channelId]
			keep = config != nil && config.SupportsPathForModel(requestPath, model)
		}
		if keep {
			if filtered != nil {
				filtered = append(filtered, channelId)
			}
			continue
		}
		if filtered == nil {
			filtered = make([]int, 0, len(channels)-1)
			filtered = append(filtered, channels[:index]...)
		}
	}
	if filtered == nil {
		return channels
	}
	return filtered
}

func CacheGetChannel(id int) (*Channel, error) {
	if !common.MemoryCacheEnabled {
		return GetChannelById(id, true)
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	if !channelCacheReady {
		return nil, channelCacheNotReadyErrorLocked()
	}

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("渠道# %d，已不存在", id)
	}
	return cloneChannel(c), nil
}

func CacheGetChannelInfo(id int) (*ChannelInfo, error) {
	if !common.MemoryCacheEnabled {
		channel, err := GetChannelById(id, true)
		if err != nil {
			return nil, err
		}
		return &channel.ChannelInfo, nil
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	if !channelCacheReady {
		return nil, channelCacheNotReadyErrorLocked()
	}

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("渠道# %d，已不存在", id)
	}
	info := cloneChannelInfo(c.ChannelInfo)
	return &info, nil
}

func CacheUpdateChannelStatus(id int, status int) {
	cacheUpdateChannelStatuses([]int{id}, status)
}

func cloneChannelMap(source map[int]*Channel) map[int]*Channel {
	cloned := make(map[int]*Channel, len(source))
	for id, channel := range source {
		cloned[id] = channel
	}
	return cloned
}

func cloneRoutingMapWithout(source map[string]map[string][]int, removed map[int]struct{}) map[string]map[string][]int {
	cloned := make(map[string]map[string][]int, len(source))
	for group, models := range source {
		clonedModels := make(map[string][]int, len(models))
		for model, channels := range models {
			filtered := make([]int, 0, len(channels))
			for _, channelID := range channels {
				if _, remove := removed[channelID]; !remove {
					filtered = append(filtered, channelID)
				}
			}
			clonedModels[model] = filtered
		}
		cloned[group] = clonedModels
	}
	return cloned
}

func cacheUpdateChannelStatuses(ids []int, status int) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelRefreshLock.Lock()
	defer channelRefreshLock.Unlock()
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	updatedChannels := cloneChannelMap(channelsIDM)
	removed := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		removed[id] = struct{}{}
		if channel, ok := updatedChannels[id]; ok {
			updated := cloneChannel(channel)
			updated.Status = status
			updatedChannels[id] = updated
		}
	}
	channelsIDM = updatedChannels
	if status != common.ChannelStatusEnabled {
		group2model2channels = cloneRoutingMapWithout(group2model2channels, removed)
	}
}

func cacheDeleteChannels(ids []int) {
	if !common.MemoryCacheEnabled || len(ids) == 0 {
		return
	}
	channelRefreshLock.Lock()
	channelSyncLock.Lock()
	updatedChannels := cloneChannelMap(channelsIDM)
	updatedConfigs := make(map[int]*dto.AdvancedCustomConfig, len(channel2advancedCustomConfig))
	for id, config := range channel2advancedCustomConfig {
		updatedConfigs[id] = config
	}
	removed := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		removed[id] = struct{}{}
		delete(updatedChannels, id)
		delete(updatedConfigs, id)
	}
	channelsIDM = updatedChannels
	channel2advancedCustomConfig = updatedConfigs
	group2model2channels = cloneRoutingMapWithout(group2model2channels, removed)
	channelSyncLock.Unlock()
	channelRefreshLock.Unlock()
	for _, id := range ids {
		channelRuntimeStates.Delete(id)
	}
	InvalidatePricingCache()
}

func CacheUpdateChannel(channel *Channel) {
	if !common.MemoryCacheEnabled {
		return
	}
	if channel == nil {
		return
	}
	channelRefreshLock.Lock()
	channelSyncLock.Lock()
	updated := cloneChannel(channel)

	updatedChannels := cloneChannelMap(channelsIDM)
	if oldChannel, ok := channelsIDM[channel.Id]; ok {
		logger.LogDebug(nil, "CacheUpdateChannel before: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, oldChannel.ChannelInfo.MultiKeyPollingIndex)
	}
	updatedChannels[channel.Id] = updated
	channelsIDM = updatedChannels
	updatedConfigs := make(map[int]*dto.AdvancedCustomConfig, len(channel2advancedCustomConfig)+1)
	for id, config := range channel2advancedCustomConfig {
		updatedConfigs[id] = config
	}
	delete(updatedConfigs, channel.Id)
	if updated.Type == constant.ChannelTypeAdvancedCustom {
		if config := updated.GetOtherSettings().AdvancedCustom; config != nil {
			updatedConfigs[channel.Id] = config
		}
	}
	channel2advancedCustomConfig = updatedConfigs
	if updated.Status != common.ChannelStatusEnabled {
		group2model2channels = cloneRoutingMapWithout(group2model2channels, map[int]struct{}{updated.Id: {}})
	}
	logger.LogDebug(nil, "CacheUpdateChannel after: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, channel.ChannelInfo.MultiKeyPollingIndex)
	// Keep pricing invalidation outside the channel-cache critical section even
	// though invalidation itself is non-blocking.
	channelSyncLock.Unlock()
	channelRefreshLock.Unlock()
	InvalidatePricingCache()
}
