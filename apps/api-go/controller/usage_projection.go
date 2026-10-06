package controller

import "github.com/LIghtJUNction/api.lmm.best/model"

// Historical UsedQuota remains unchanged. A nil current projection is unknown,
// while an available zero is a real zero and must survive JSON serialization.
type usageProjectionFields struct {
	NormalizedUsedQuota      *int `json:"normalized_used_quota,omitempty"`
	UsageProjectionAvailable bool `json:"usage_projection_available"`
}

type usageProjector interface {
	User(userID, currentUsed int) (model.UsageProjection, error)
	Token(tokenID, userID, currentUsed int) (model.UsageProjection, error)
}

func loadUsageProjector() usageProjector {
	projector, err := model.LoadUsageProjector(model.DB)
	if err != nil {
		return nil
	}
	return projector
}

func projectUserUsage(projector usageProjector, user *model.User) usageProjectionFields {
	if projector == nil || user == nil {
		return usageProjectionFields{}
	}
	projection, err := projector.User(user.Id, user.UsedQuota)
	if err != nil {
		return usageProjectionFields{}
	}
	value := projection.NormalizedUsedQuota
	return usageProjectionFields{NormalizedUsedQuota: &value, UsageProjectionAvailable: true}
}

func projectTokenUsage(projector usageProjector, token *model.Token) usageProjectionFields {
	if projector == nil || token == nil {
		return usageProjectionFields{}
	}
	projection, err := projector.Token(token.Id, token.UserId, token.UsedQuota)
	if err != nil {
		return usageProjectionFields{}
	}
	value := projection.NormalizedUsedQuota
	return usageProjectionFields{NormalizedUsedQuota: &value, UsageProjectionAvailable: true}
}

func projectBillingUsage(projector usageProjector, token *model.Token, userID, used int) usageProjectionFields {
	if token != nil {
		return projectTokenUsage(projector, token)
	}
	return projectUserUsage(projector, &model.User{Id: userID, UsedQuota: used})
}
