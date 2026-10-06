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
	PublicCreditUsed    string `json:"public_credit_used"`
}

func publicUserCreditFields(quota, used int) (gin.H, error) {
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
	consumed, err := basis.publicAmount(int64(used))
	if err != nil {
		return nil, err
	}
	fields["public_credit_balance"] = balance.String()
	fields["public_credit_used"] = consumed.String()
	return fields, nil
}

func buildPublicUserCreditResponse(user *model.User) (*publicUserCreditResponse, error) {
	basis, err := captureCreditBoundaryBasis()
	if err != nil {
		return nil, err
	}
	return buildPublicUserCreditResponseWithBasis(user, basis)
}

func buildPublicUserCreditResponseWithBasis(user *model.User, basis creditBoundaryBasis) (*publicUserCreditResponse, error) {
	balance, err := basis.publicAmount(int64(user.Quota))
	if err != nil {
		return nil, err
	}
	used, err := basis.publicAmount(int64(user.UsedQuota))
	if err != nil {
		return nil, err
	}
	return &publicUserCreditResponse{User: user, CreditDenomination: basis.Metadata, PublicCreditBalance: balance.String(), PublicCreditUsed: used.String()}, nil
}

func buildPublicUserCreditResponses(users []*model.User) ([]*publicUserCreditResponse, error) {
	result := make([]*publicUserCreditResponse, 0, len(users))
	basis, err := captureCreditBoundaryBasis()
	if err != nil {
		return nil, err
	}
	for _, user := range users {
		response, err := buildPublicUserCreditResponseWithBasis(user, basis)
		if err != nil {
			return nil, err
		}
		result = append(result, response)
	}
	return result, nil
}
