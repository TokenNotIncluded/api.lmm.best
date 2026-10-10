package model

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAssistantGiftAutonomyExclusiveBounds(t *testing.T) {
	for _, test := range []struct {
		name        string
		amount, cap int
		want        error
	}{
		{"negative amount", -1, 100, ErrAssistantGiftInvalid},
		{"zero", 0, 100, nil}, {"below cap", 99, 100, nil},
		{"equal cap", 100, 100, ErrAssistantGiftLimit}, {"above cap", 101, 100, ErrAssistantGiftLimit},
		{"disabled zero", 0, 0, ErrAssistantGiftDisabled}, {"disabled positive", 1, 0, ErrAssistantGiftDisabled},
		{"invalid cap", 0, -1, ErrAssistantGiftDisabled},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := CheckAssistantGiftCreditLimit(test.amount, test.cap)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v want %v", err, test.want)
			}
		})
	}
}

// These database tests use the repository's real gift fixtures.
func TestAssistantGiftAutonomyZeroDoesNotConsumeOpportunity(t *testing.T) {
	db := setupAssistantGiftTestDB(t)
	user := newAssistantGiftUser(t, db, "gift-autonomy-zero", "autonomy-zero@example.com")
	gift, created, err := DecideAssistantNewUserGiftCredits(user.Id, 7, 0, "", 0, 0, "198.51.100.10")
	require.NoError(t, err)
	require.False(t, created)
	require.Zero(t, gift.Id)
	var count int64
	require.NoError(t, db.Model(&AssistantNewUserGift{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(&AssistantGiftRiskMemory{}).Count(&count).Error)
	require.Zero(t, count)

	gift, created, err = DecideAssistantNewUserGiftCredits(user.Id, 7, 10, "写项目", 0, 0, "198.51.100.10")
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, AssistantGiftOffered, gift.Status)
	_, already, err := ClaimAssistantNewUserGift(user.Id)
	require.NoError(t, err)
	require.False(t, already)
	_, already, err = ClaimAssistantNewUserGift(user.Id)
	require.NoError(t, err)
	require.True(t, already)
	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, 10, stored.Quota)
}
