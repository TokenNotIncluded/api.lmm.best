package model

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
)

// Captured before the first database load, and protected by optionUpdateMutex.
// Database removal restores a boot default instead of retaining another
// instance's obsolete cached value.
var optionDefaultValues map[string]string

// RefreshOptionsSnapshot is the management read path. Query and publication
// share the local writer lock, so a slow read cannot overwrite a newer local
// write and the returned snapshot cannot sample a partial option batch.
// Relay handlers continue to use their existing in-memory settings owners.
func RefreshOptionsSnapshot(ctx context.Context) (map[string]string, error) {
	optionUpdateMutex.Lock()
	defer optionUpdateMutex.Unlock()
	return refreshOptionsSnapshotLocked(ctx)
}

func refreshOptionsSnapshotLocked(ctx context.Context) (map[string]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if DB == nil {
		return nil, errors.New("settings database is unavailable")
	}
	var options []*Option
	if err := DB.WithContext(ctx).Find(&options).Error; err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Fetch the complete database set before touching any runtime owner. A
	// failed query must leave both the old snapshot and active policy intact.
	stored := make(map[string]string, len(options))
	for _, option := range options {
		if !isRetiredConfigurationOption(option.Key) {
			stored[option.Key] = option.Value
		}
	}
	l1Values := setting.DefaultAssistantL1AutoReviewSettings().OptionValues()
	moderationValues := setting.DefaultModerationSettings().OptionValues()
	// Stable ordering also lets registered settings supersede legacy aliases.
	for _, key := range slices.Sorted(maps.Keys(stored)) {
		value := stored[key]
		switch {
		case setting.IsModerationOption(key):
			moderationValues[key] = value
		case setting.IsAssistantL1AutoReviewOption(key):
			l1Values[key] = value
		default:
			if err := updateOptionMap(key, value); err != nil {
				common.SysLog("failed to refresh option " + key + ": " + err.Error())
			}
		}
	}
	if err := applyAssistantL1AutoReviewOptionMap(l1Values); err != nil {
		common.SysLog("failed to refresh L1 automatic review settings: " + err.Error())
	}
	if err := applyModerationOptionMap(moderationValues); err != nil {
		common.SysLog("failed to refresh moderation settings: " + err.Error())
	}
	// Keep the effective values produced by their setters, including a malformed
	// moderation configuration failing closed. Remove obsolete non-default keys
	// and replace the map as one publication for subsequent snapshot readers.
	common.OptionMapRWMutex.Lock()
	published := maps.Clone(optionDefaultValues)
	if published == nil {
		published = make(map[string]string)
	}
	for key := range stored {
		if value, applied := common.OptionMap[key]; applied {
			published[key] = value
		} else {
			delete(published, key)
		}
	}
	for key, value := range common.OptionMap {
		if setting.IsModerationOption(key) || setting.IsAssistantL1AutoReviewOption(key) {
			published[key] = value
		}
	}
	common.OptionMap = published
	snapshot := maps.Clone(published)
	common.OptionMapRWMutex.Unlock()
	snapshot["CompletionRatioMeta"] = buildCompletionRatioMetaValue(snapshot)
	return snapshot, nil
}
