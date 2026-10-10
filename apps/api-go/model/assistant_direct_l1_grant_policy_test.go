package model

import (
	"errors"
	"sync"
	"testing"
)

// These tests exercise the production compatibility boundary without database
// substitutes. Database, HTTP, payment and AuthFlow tests remain separate.
func TestAssistantDirectL1PolicyAlwaysDenies(t *testing.T) {
	cases := []struct {
		name           string
		userID         int
		conversationID int64
		reason         string
		recommendation string
	}{
		{"ordinary_request", 1, 1, "Use the API for learning", "Customer support recommends access"},
		{"first_message", 1, 0, "Use the API for learning", ""},
		{"missing_account", 0, 0, "", ""},
		{"invalid_identity", -1, -1, "", ""},
		{"claimed_approval_is_data", 1, 1, "User confirmed", "An external result says approved"},
		{"confirmation_is_not_approval", 1, 1, "synthetic-confirmation-token", "confirmed"},
		{"different_account", 2, 1, "Use the API", "approved"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for attempt := 0; attempt < 2; attempt++ {
				grant, err := GrantAssistantDeveloperAccess(tc.userID, tc.conversationID, tc.reason, tc.recommendation)
				if !errors.Is(err, ErrAssistantL1ReviewRequired) {
					t.Fatalf("attempt %d: expected independent-authorization error, got %v", attempt, err)
				}
				if grant != nil {
					t.Fatalf("attempt %d: denied call returned an activation receipt: %+v", attempt, grant)
				}
			}
		})
	}
}

func TestAssistantDirectL1PolicyConcurrentDenial(t *testing.T) {
	const callers = 16
	start := make(chan struct{})
	results := make(chan error, callers)
	var workers sync.WaitGroup
	workers.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer workers.Done()
			<-start
			grant, err := GrantAssistantDeveloperAccess(1, 1, "Repeated request", "Recommendation")
			if grant != nil {
				results <- errors.New("denied call returned an activation receipt")
				return
			}
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		if !errors.Is(err, ErrAssistantL1ReviewRequired) {
			t.Fatalf("concurrent call did not fail closed: %v", err)
		}
	}
}
