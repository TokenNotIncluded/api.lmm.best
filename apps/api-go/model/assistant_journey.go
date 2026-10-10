package model

import "gorm.io/gorm"

const (
	AssistantJourneyPending   = "pending"
	AssistantJourneyCompleted = "completed"
	AssistantJourneyFailed    = "failed"
)

type AssistantJourneyStep struct {
	Id     string `json:"id"`
	Status string `json:"status"`
}

type AssistantJourney struct {
	Main []AssistantJourneyStep `json:"main"`
	Side []AssistantJourneyStep `json:"side"`
}

func journeyStep(id string, completed bool) AssistantJourneyStep {
	status := AssistantJourneyPending
	if completed {
		status = AssistantJourneyCompleted
	}
	return AssistantJourneyStep{Id: id, Status: status}
}

func assistantGiftJourneyStep(gift *AssistantNewUserGift) AssistantJourneyStep {
	status := AssistantJourneyPending
	if gift != nil {
		switch gift.Status {
		case AssistantGiftOffered, AssistantGiftClaimed:
			status = AssistantJourneyCompleted
		case AssistantGiftDeclined:
			status = AssistantJourneyFailed
		}
	}
	return AssistantJourneyStep{Id: "earn_ai_gift", Status: status}
}

// GetAssistantJourney derives progress from authoritative server records. It
// intentionally stores no duplicate progress flags that could drift from the
// developer-access, key, client-proof, relay-usage, or bounty state.
func GetAssistantJourney(userID int) (*AssistantJourney, error) {
	if userID <= 0 {
		return nil, gorm.ErrInvalidData
	}
	var conversationCount int64
	if err := DB.Model(&AssistantConversation{}).Where("user_id = ?", userID).Count(&conversationCount).Error; err != nil {
		return nil, err
	}
	onboarding, err := GetL1OnboardingTodo(userID)
	if err != nil {
		return nil, err
	}
	onboardingState := make(map[string]bool, len(onboarding.Steps))
	for _, step := range onboarding.Steps {
		onboardingState[step.Id] = step.Status == L1OnboardingStatusCompleted
	}
	var bountyCount int64
	if err := DB.Model(&OpenSourceBountyChallenge{}).Where("participant_user_id = ?", userID).Count(&bountyCount).Error; err != nil {
		return nil, err
	}
	gift, err := GetAssistantNewUserGift(userID)
	if err != nil {
		return nil, err
	}

	return &AssistantJourney{
		Main: []AssistantJourneyStep{
			journeyStep("ask_ai", conversationCount > 0),
			// Keep the old step ID for existing clients; it now means "Get L1
			// access". Eligibility carries the fresh DeveloperAccessState.Granted
			// decision. Historical recommendation text never grants access.
			journeyStep("get_recommendation", onboarding.Eligibility.DeveloperAccessGranted),
			journeyStep("create_api_key", onboardingState[L1OnboardingStepCreateAPIKey]),
			journeyStep("install_client", onboardingState[L1OnboardingStepInstallClient]),
			journeyStep("configure_client", onboardingState[L1OnboardingStepConfigureClient]),
			journeyStep("first_api_call", onboardingState[L1OnboardingStepFirstSuccessfulResponse]),
		},
		Side: []AssistantJourneyStep{
			assistantGiftJourneyStep(gift),
			journeyStep("accept_bounty", bountyCount > 0),
		},
	}, nil
}
