package model

import (
	"context"
	"errors"

	"github.com/LIghtJUNction/api.lmm.best/setting"
)

// Read the committed policy at a capability boundary. Background option sync
// is deliberately insufficient for revocation across multiple Go instances.
func ReadAssistantToolPolicy(ctx context.Context) (string, setting.AssistantToolPolicy, error) {
	if DB == nil {
		return "", setting.AssistantToolPolicy{}, errors.New("assistant tool policy database is unavailable")
	}
	var option Option
	err := DB.WithContext(ctx).Select("value").Where("key = ?", setting.AssistantToolPolicyOptionKey).Limit(1).Find(&option).Error
	if err != nil {
		return "", setting.AssistantToolPolicy{}, errors.New("assistant tool policy could not be loaded")
	}
	canonical, policy, err := setting.NormalizeAssistantToolPolicy(option.Value)
	if err != nil {
		return "", setting.AssistantToolPolicy{}, errors.New("assistant tool policy is invalid")
	}
	return canonical, policy, nil
}
