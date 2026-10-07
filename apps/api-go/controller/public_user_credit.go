package controller

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

// Public projections accompany, but never replace, the compatibility quota
// fields. Embedding the existing DTO preserves its privacy and wire contract.
type publicUserCreditResponse struct {
	*model.User
	common.CreditDenomination
	PublicCreditBalance string `json:"public_credit_balance"`
	PublicCreditUsed    string `json:"public_credit_used,omitempty"`
	usageProjectionFields
}

func publicUserCreditFields(quota int, used *int) (gin.H, error) {
	basis, err := captureCreditBoundaryBasis()
	if err != nil {
		return nil, err
	}
	fields := gin.H{}
	basis.addMetadata(fields)
	balance, err := basis.publicAmount(int64(quota))
	if err != nil {
		return nil, err
	}
	fields["public_credit_balance"] = balance.String()
	if used != nil {
		consumed, err := basis.publicAmount(int64(*used))
		if err != nil {
			return nil, err
		}
		fields["public_credit_used"] = consumed.String()
	}
	return fields, nil
}

func buildPublicUserCreditResponse(user *model.User) (*publicUserCreditResponse, error) {
	basis, err := captureCreditBoundaryBasis()
	if err != nil {
		return nil, err
	}
	snapshot := loadUsageSnapshot([]*model.User{user}, nil)
	current, projector := snapshot.user(user)
	return buildPublicUserCreditResponseWithBasis(current, basis, projector)
}

func buildPublicUserCreditResponseWithBasis(user *model.User, basis creditBoundaryBasis, projector usageProjector) (*publicUserCreditResponse, error) {
	balance, err := basis.publicAmount(int64(user.Quota))
	if err != nil {
		return nil, err
	}
	usage := projectUserUsage(projector, user)
	response := &publicUserCreditResponse{User: user, CreditDenomination: basis.Metadata, PublicCreditBalance: balance.String(), usageProjectionFields: usage}
	if usage.NormalizedUsedQuota != nil {
		used, err := basis.publicAmount(int64(*usage.NormalizedUsedQuota))
		if err != nil {
			return nil, err
		}
		response.PublicCreditUsed = used.String()
	}
	return response, nil
}

func buildPublicUserCreditResponses(users []*model.User) ([]*publicUserCreditResponse, error) {
	result := make([]*publicUserCreditResponse, 0, len(users))
	basis, err := captureCreditBoundaryBasis()
	if err != nil {
		return nil, err
	}
	snapshot := loadUsageSnapshot(users, nil)
	for _, user := range users {
		current, projector := snapshot.user(user)
		response, err := buildPublicUserCreditResponseWithBasis(current, basis, projector)
		if err != nil {
			return nil, err
		}
		result = append(result, response)
	}
	return result, nil
}
