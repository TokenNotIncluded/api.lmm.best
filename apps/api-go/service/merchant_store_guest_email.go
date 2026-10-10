package service

import (
	"context"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/model"
)

// This path uses a real guest's address-bound challenge, never an account 0.
// The shared sender enforces bounded SMTP and emits only the ownership code.
func SendMerchantStoreGuestVerificationEmail(ctx context.Context, guestToken, expectedEmail, challengeID, code string) error {
	if len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		return errMerchantStoreEmail
	}
	if err := model.ValidateMerchantStoreGuestEmailChallenge(guestToken, expectedEmail, challengeID, code); err != nil {
		return err
	}
	return sendMerchantStorePickupEmail(ctx, merchantStorePickupEmail{destination: expectedEmail, verificationCode: code})
}
