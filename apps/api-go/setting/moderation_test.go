package setting

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func preserveModerationSettings(t *testing.T) {
	t.Helper()
	previous := GetModerationSettings()
	require.NoError(t, UpdateModerationSettings(DefaultModerationSettings().OptionValues()))
	t.Cleanup(func() { require.NoError(t, UpdateModerationSettings(previous.OptionValues())) })
}

func TestModerationDefaultsAndExplicitGroupPolicies(t *testing.T) {
	preserveModerationSettings(t)
	defaults := GetModerationSettings()
	require.False(t, defaults.Enabled)
	require.False(t, defaults.AssistantEnabled)
	require.Empty(t, defaults.GroupPolicies)
	require.Equal(t, "omni-moderation-latest", defaults.Model)
	require.Equal(t, "omni-moderation-latest", defaults.AssistantModel)
	policy, found := ModerationPolicyForGroup("default")
	require.False(t, found)
	require.Equal(t, ModerationModeOff, policy.Mode)
	require.NoError(t, UpdateModerationSettings(map[string]string{
		ModerationGroupPoliciesOptionKey: `{"default":{"mode":"tolerant"},"premium":{"mode":"strict","category_fines_usd":{"sexual/minors":1.25}}}`,
	}))
	policy, found = ModerationPolicyForGroup("other")
	require.False(t, found)
	require.Equal(t, ModerationModeOff, policy.Mode)
	policy, found = ModerationPolicyForGroup("premium")
	require.True(t, found)
	require.Equal(t, 1.25, policy.CategoryFinesUSD["sexual/minors"])
	policy.CategoryFinesUSD["sexual/minors"] = 999
	snapshot := GetModerationSettings()
	snapshot.GroupPolicies["premium"].CategoryFinesUSD["sexual/minors"] = 888
	delete(snapshot.GroupPolicies, "default")
	policy, found = ModerationPolicyForGroup("premium")
	require.True(t, found)
	require.Equal(t, 1.25, policy.CategoryFinesUSD["sexual/minors"])
	_, found = ModerationPolicyForGroup("default")
	require.True(t, found)
}

func TestModerationRejectsNonOfficialModelsAndMalformedPolicies(t *testing.T) {
	for _, model := range []string{"gpt-5", "text-moderation-latest", "omni-moderation-latest-proxy", "", "omni-moderation-latest "} {
		require.False(t, IsModerationModel(model))
	}
	require.True(t, IsModerationModel("omni-moderation-2024-09-26"))
	for _, value := range []string{
		`null`, `[]`, `{} {}`, `{"*":{"mode":"strict"}}`,
		`{" default ":{"mode":"strict"}}`, `{"default":{"mode":"block"}}`,
		`{"default":{"mode":"strict","unexpected":true}}`,
		`{"default":{"mode":"strict","category_fines_usd":{"*":1}}}`,
		`{"default":{"mode":"strict","category_fines_usd":{"hate":-1}}}`,
		`{"default":{"mode":"strict","category_fines_usd":{"hate":1000001}}}`,
		`{"default":{"mode":"strict","category_fines_usd":{"hate":1000.000001}}}`,
		`{"default":{"mode":"strict","category_fines_usd":{"hate":0.1234567}}}`,
		`{"default":{"mode":"strict","category_fines_usd":{"hate":null}}}`,
		`{"default":{"mode":"strict","category_fines_usd":{"hate":1e999}}}`,
	} {
		t.Run(value, func(t *testing.T) {
			_, err := ParseModerationGroupPolicies(value)
			require.Error(t, err)
		})
	}
	for key, value := range map[string]string{
		ModerationEnabledOptionKey: "1", AssistantModerationEnabledOptionKey: "True",
		ModerationGroupOptionKey: "*", AssistantModerationGroupOptionKey: "",
		ModerationModelOptionKey: "gpt-5", AssistantModerationModelOptionKey: "text-moderation-latest",
	} {
		_, err := ParseModerationSettings(DefaultModerationSettings(), map[string]string{key: value})
		require.Error(t, err)
	}
	fines := make(map[string]float64)
	for _, category := range ModerationCategories() {
		require.True(t, IsModerationCategory(category))
		fines[category] = 0.25
	}
	require.Len(t, fines, 13)
	encoded, err := json.Marshal(map[string]ModerationGroupPolicy{"default": {Mode: ModerationModeStrict, CategoryFinesUSD: fines}})
	require.NoError(t, err)
	_, err = ParseModerationGroupPolicies(string(encoded))
	require.NoError(t, err)
}

func TestModerationFinePrecisionAndLimit(t *testing.T) {
	for _, amount := range []float64{0, 0.000001, 0.123456, 99.123456, 1000} {
		require.NoError(t, ValidateModerationFineUSD(amount))
	}
	for _, amount := range []float64{-1, 0.0000001, 99.123456789, 1000.000001, math.NaN(), math.Inf(1), math.Inf(-1)} {
		require.Error(t, ValidateModerationFineUSD(amount))
	}
}

func TestModerationMalformedReloadFailsClosed(t *testing.T) {
	preserveModerationSettings(t)
	require.NoError(t, UpdateModerationSettings(map[string]string{
		ModerationEnabledOptionKey: "true", AssistantModerationEnabledOptionKey: "true",
	}))
	require.Error(t, UpdateModerationSettings(map[string]string{ModerationGroupPoliciesOptionKey: `{"*":{"mode":"strict"}}`}))
	require.False(t, GetModerationSettings().Enabled)
	require.False(t, GetModerationSettings().AssistantEnabled)
}

func TestModerationCurrentSettingsFenceHoldsThroughEffect(t *testing.T) {
	preserveModerationSettings(t)
	require.NoError(t, UpdateModerationSettings(map[string]string{ModerationEnabledOptionKey: "true"}))
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		finished <- WithCurrentModerationSettings(func(settings ModerationSettings) error {
			assert.True(t, settings.Enabled)
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	updated := make(chan error, 1)
	go func() { updated <- UpdateModerationSettings(map[string]string{ModerationEnabledOptionKey: "false"}) }()
	select {
	case err := <-updated:
		close(release)
		<-finished
		t.Fatalf("configuration writer crossed a held effect fence: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-finished)
	require.NoError(t, <-updated)
	require.False(t, GetModerationSettings().Enabled)
}
