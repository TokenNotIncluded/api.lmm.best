package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantJourneyAccessMilestoneUsesGrantedAccessNotHistoricalLetter(t *testing.T) {
	for _, tc := range []struct {
		name      string
		role      int
		activated bool
		letter    bool
		status    string
	}{
		{name: "l0_with_old_draft", role: common.RoleCommonUser, letter: true, status: AssistantJourneyPending},
		{name: "l1_without_letter", role: common.RoleCommonUser, activated: true, status: AssistantJourneyCompleted},
		{name: "administrator_without_letter", role: common.RoleAdminUser, status: AssistantJourneyCompleted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupL1OnboardingTodoTestDB(t)
			require.NoError(t, db.AutoMigrate(&AssistantConversation{}, &AssistantNewUserGift{}, &OpenSourceBountyChallenge{}, &DeveloperAccessRequest{}))
			user := User{Username: "journey-access", Password: "password", Role: tc.role, Status: common.UserStatusEnabled}
			if tc.activated {
				user.ConsoleActivatedAt = 100
			}
			require.NoError(t, db.Create(&user).Error)
			var request DeveloperAccessRequest
			if tc.letter {
				request = DeveloperAccessRequest{UserId: user.Id, Status: DeveloperAccessRequestPending,
					Source: "legacy", Reason: "A historical application.", AIRecommendation: "An old AI draft without access approval.", CreatedAt: 50}
				require.NoError(t, db.Create(&request).Error)
			}

			journey, err := GetAssistantJourney(user.Id)
			require.NoError(t, err)
			require.Len(t, journey.Main, 6)
			assert.Equal(t, AssistantJourneyStep{Id: "get_recommendation", Status: tc.status}, journey.Main[1])
			assert.Equal(t, AssistantJourneyPending, journey.Main[3].Status, "access alone must not establish installation proof")
			assert.Equal(t, AssistantJourneyPending, journey.Main[5].Status, "access alone must not establish an API call")

			if tc.letter {
				var stored DeveloperAccessRequest
				require.NoError(t, db.First(&stored, request.Id).Error)
				assert.Equal(t, request, stored, "reading a journey must preserve the historical letter")
				// Access can become active without editing or approving the old
				// letter. The same compatible step must immediately follow it.
				require.NoError(t, db.Model(&user).Update("console_activated_at", 100).Error)
				journey, err = GetAssistantJourney(user.Id)
				require.NoError(t, err)
				assert.Equal(t, AssistantJourneyCompleted, journey.Main[1].Status)
			}
			// The active checklist no longer depends on a recommendation table.
			require.NoError(t, db.Migrator().DropTable(&DeveloperAccessRequest{}))
			journey, err = GetAssistantJourney(user.Id)
			require.NoError(t, err)
			assert.Equal(t, AssistantJourneyCompleted, journey.Main[1].Status)
		})
	}
}
