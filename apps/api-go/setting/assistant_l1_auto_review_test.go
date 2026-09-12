package setting

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func validL1ReviewSettings() AssistantL1AutoReviewSettings {
	value := DefaultAssistantL1AutoReviewSettings()
	value.Enabled, value.Group, value.Model = true, "review", "review-model"
	value.Prompt = "Evaluate the user's concrete use case and explain the decision."
	return value
}

func preserveL1ReviewSettings(t *testing.T) {
	t.Helper()
	previous := GetAssistantL1AutoReviewSettings()
	t.Cleanup(func() { require.NoError(t, UpdateAssistantL1AutoReviewOptions(previous.OptionValues())) })
}

func TestAssistantL1AutoReviewDefaultsFailClosed(t *testing.T) {
	settings := DefaultAssistantL1AutoReviewSettings()
	require.False(t, settings.Enabled)
	require.False(t, settings.Ready())
	require.False(t, settings.UserAllowed(42))
	require.Empty(t, settings.Prompt, "policy must be explicitly configured")
}

func TestParseAssistantL1AutoReviewSettings(t *testing.T) {
	settings := validL1ReviewSettings()
	require.True(t, settings.Ready())
	require.True(t, settings.UserAllowed(42), "empty allowlist covers ordinary applicants")
	require.False(t, settings.UserAllowed(0))
	values := settings.OptionValues()
	values[AssistantL1AutoApprovalUserIDsOptionKey] = "43，42 43"
	values[AssistantL1AutoReviewMinConfidenceOptionKey] = "0.95"
	parsed, err := ParseAssistantL1AutoReviewSettings(DefaultAssistantL1AutoReviewSettings(), values)
	require.NoError(t, err)
	require.Equal(t, "42,43", parsed.UserIDs)
	require.Equal(t, 0.95, parsed.MinConfidence)
	require.True(t, parsed.UserAllowed(42))
	require.False(t, parsed.UserAllowed(41))
	oversizedIDs := make([]string, 1000)
	for index := range oversizedIDs {
		oversizedIDs[index] = strconv.Itoa(index + 10000)
	}

	for _, tc := range []struct{ name, key, value string }{
		{"invalid switch", AssistantL1AutoReviewEnabledOptionKey, "1"},
		{"mixed case switch", AssistantL1AutoReviewEnabledOptionKey, "TRUE"},
		{"nan", AssistantL1AutoReviewMinConfidenceOptionKey, "NaN"},
		{"inf", AssistantL1AutoReviewMinConfidenceOptionKey, "+Inf"},
		{"too low", AssistantL1AutoReviewMinConfidenceOptionKey, "-0.01"},
		{"too high", AssistantL1AutoReviewMinConfidenceOptionKey, "1.01"},
		{"blank confidence", AssistantL1AutoReviewMinConfidenceOptionKey, ""},
		{"long model", AssistantL1AutoReviewModelOptionKey, strings.Repeat("a", 257)},
		{"long group", AssistantL1AutoReviewGroupOptionKey, strings.Repeat("a", 129)},
		{"long prompt", AssistantL1AutoReviewPromptOptionKey, strings.Repeat("中", 12001)},
		{"bad allowlist", AssistantL1AutoApprovalUserIDsOptionKey, "-1"},
		{"oversized allowlist", AssistantL1AutoApprovalUserIDsOptionKey, strings.Join(oversizedIDs, ",")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseAssistantL1AutoReviewSettings(settings, map[string]string{tc.key: tc.value})
			require.Error(t, err)
		})
	}
}

func TestAssistantL1AutoReviewRuntimeSnapshotAndInvalidReload(t *testing.T) {
	preserveL1ReviewSettings(t)
	config := validL1ReviewSettings()
	require.NoError(t, UpdateAssistantL1AutoReviewOptions(config.OptionValues()))
	called := false
	require.NoError(t, WithAssistantL1AutoReviewSettings(config, func() error { called = true; return nil }))
	require.True(t, called)

	require.NoError(t, UpdateAssistantL1AutoReviewOption(AssistantL1AutoReviewPromptOptionKey, "A changed review policy"))
	called = false
	require.Error(t, WithAssistantL1AutoReviewSettings(config, func() error { called = true; return nil }))
	require.False(t, called, "stale snapshots must never reach the decision transaction")
	require.Error(t, UpdateAssistantL1AutoReviewOption(AssistantL1AutoReviewMinConfidenceOptionKey, "NaN"))
	require.False(t, GetAssistantL1AutoReviewSettings().Enabled)
}

func TestAssistantL1AutoReviewSnapshotsArePublishedAtomically(t *testing.T) {
	preserveL1ReviewSettings(t)
	first, second := validL1ReviewSettings(), validL1ReviewSettings()
	first.Model, first.Group, first.UserIDs = "model-a", "group-a", "42"
	second.Model, second.Group, second.UserIDs = "model-b", "group-b", "43"
	require.NoError(t, UpdateAssistantL1AutoReviewOptions(first.OptionValues()))
	var workers sync.WaitGroup
	errorsCh := make(chan error, 2)
	for _, next := range []AssistantL1AutoReviewSettings{first, second} {
		workers.Go(func() {
			for i := 0; i < 300; i++ {
				if err := UpdateAssistantL1AutoReviewOptions(next.OptionValues()); err != nil {
					errorsCh <- err
					return
				}
				if snapshot := GetAssistantL1AutoReviewSettings(); snapshot != first && snapshot != second {
					errorsCh <- errors.New("observed a partially published configuration")
					return
				}
			}
		})
	}
	workers.Wait()
	close(errorsCh)
	for err := range errorsCh {
		require.NoError(t, err)
	}
}
