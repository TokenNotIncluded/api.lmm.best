package controller

import (
	_ "embed"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Keep the personas in data so the same matrix can be reviewed, extended, or
// replayed by a small simulator without changing the assertions themselves.
//
//go:embed assistant_persona_matrix_testdata.json
var assistantPersonaMatrixTestdata []byte

type assistantPersonaMatrixFixture struct {
	ID       string                      `json:"id"`
	Label    string                      `json:"label"`
	User     assistantPersonaUserFixture `json:"user"`
	Message  string                      `json:"message"`
	Expected assistantPersonaExpectation `json:"expected"`
}

type assistantPersonaUserFixture struct {
	Username               string `json:"username"`
	Email                  string `json:"email"`
	AccessLevel            string `json:"access_level"`
	AdministratorMode      bool   `json:"administrator_mode"`
	DeveloperAccessGranted bool   `json:"developer_access_granted"`
	PaymentMethodsHidden   bool   `json:"payment_methods_hidden"`
	InterlocutorAssessed   bool   `json:"interlocutor_assessed"`
}

type assistantPersonaExpectation struct {
	Profile           string                              `json:"profile"`
	Signals           []string                            `json:"signals"`
	WelcomeContains   []string                            `json:"welcome_contains"`
	Context           assistantPersonaContextExpectation  `json:"context"`
	Security          assistantPersonaSecurityExpectation `json:"security"`
	PaymentOfferState string                              `json:"payment_offer_state"`
	Tools             assistantPersonaToolExpectation     `json:"tools"`
}

type assistantPersonaContextExpectation struct {
	MaskedEmail          string `json:"masked_email"`
	EmailDomain          string `json:"email_domain"`
	EmailCategory        string `json:"email_category"`
	AccessLevel          string `json:"access_level"`
	PaymentMethodsHidden bool   `json:"payment_methods_hidden"`
}

type assistantPersonaSecurityExpectation struct {
	HighConfidenceAbuse    bool `json:"high_confidence_abuse"`
	AuthorizedSecurityTest bool `json:"authorized_security_test"`
}

type assistantPersonaToolExpectation struct {
	Allowed []string `json:"allowed"`
	Denied  []string `json:"denied"`
}

func loadAssistantPersonaMatrix(t *testing.T) []assistantPersonaMatrixFixture {
	t.Helper()
	var fixtures []assistantPersonaMatrixFixture
	require.NoError(t, json.Unmarshal(assistantPersonaMatrixTestdata, &fixtures))
	require.GreaterOrEqual(t, len(fixtures), 9)
	return fixtures
}

func assistantPersonaContextFromFixture(fixture assistantPersonaUserFixture) assistantUserContext {
	context := assistantUserContext{
		UserID:                 1,
		Username:               strings.TrimSpace(fixture.Username),
		AccessLevel:            fixture.AccessLevel,
		AdministratorMode:      fixture.AdministratorMode,
		DeveloperAccessGranted: fixture.DeveloperAccessGranted,
		PaymentMethodsHidden:   fixture.PaymentMethodsHidden,
		InterlocutorAssessed:   fixture.InterlocutorAssessed,
	}
	if context.AccessLevel == "" {
		context.AccessLevel = "L0"
	}

	rawEmail := strings.TrimSpace(fixture.Email)
	if rawEmail != "" {
		context.Email, context.EmailDomain = maskAssistantEmail(rawEmail)
		context.EmailCategory = classifyAssistantEmail(rawEmail)
	}
	return context
}

func TestAssistantPersonaMatrix(t *testing.T) {
	fixtures := loadAssistantPersonaMatrix(t)
	requiredIDs := map[string]bool{
		"A": false,
		"B": false,
		"C": false,
		"D": false,
		"E": false,
		"F": false,
		"G": false,
		"H": false,
	}
	seenIDs := make(map[string]struct{}, len(fixtures))

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.ID+"/"+fixture.Label, func(t *testing.T) {
			require.NotEmpty(t, fixture.ID)
			require.NotEmpty(t, fixture.Message)
			if _, exists := seenIDs[fixture.ID]; exists {
				t.Fatalf("duplicate persona id %q", fixture.ID)
			}
			seenIDs[fixture.ID] = struct{}{}
			if _, required := requiredIDs[fixture.ID]; required {
				requiredIDs[fixture.ID] = true
			}

			context := assistantPersonaContextFromFixture(fixture.User)
			context.PaymentOfferState = assistantPaymentOfferStateForContextAndConversation(context, fixture.Message)
			profile, signals := classifyAssistantCustomerProfile(context, fixture.Message)
			context.CustomerProfile = profile
			assert.Equal(t, fixture.Expected.Profile, string(profile))
			for _, expectedSignal := range fixture.Expected.Signals {
				assert.Contains(t, signals, expectedSignal)
			}

			strategy := assistantWelcomeStrategyForContext(context)
			for _, expectedText := range fixture.Expected.WelcomeContains {
				assert.Contains(t, strategy, expectedText)
			}

			assert.Equal(t, fixture.Expected.Context.MaskedEmail, context.Email)
			assert.Equal(t, fixture.Expected.Context.EmailDomain, context.EmailDomain)
			assert.Equal(t, fixture.Expected.Context.EmailCategory, context.EmailCategory)
			assert.Equal(t, fixture.Expected.Context.AccessLevel, context.AccessLevel)
			assert.Equal(t, fixture.Expected.Context.PaymentMethodsHidden, context.PaymentMethodsHidden)
			assert.Equal(t, fixture.Expected.PaymentOfferState, string(assistantPaymentOfferStateForContext(context)))

			isHighConfidenceAbuse := assistantHasHighConfidenceSecurityAbuse(fixture.Message)
			assert.Equal(t, fixture.Expected.Security.HighConfidenceAbuse, isHighConfidenceAbuse)
			if fixture.Expected.Security.AuthorizedSecurityTest {
				assert.False(t, isHighConfidenceAbuse, "authorized non-destructive security guidance must not be hard-refused")
			}

			toolNames := make(map[string]bool)
			for _, definition := range assistantToolDefinitionsForContext(context) {
				toolNames[definition.Function.Name] = true
			}
			expectedAllowed := append([]string{}, fixture.Expected.Tools.Allowed...)
			expectedAllowed = append(expectedAllowed,
				"navigate_to_page",
				"get_user_overview",
				"get_user_usage_summary",
				"prepare_user_action",
			)
			if assistantWeeklyDiscountToolAllowed(context) {
				expectedAllowed = append(expectedAllowed, "prepare_weekly_discount")
			}
			assert.Len(t, toolNames, len(expectedAllowed), "fixture must describe the complete allowed tool set")
			for _, tool := range expectedAllowed {
				assert.True(t, toolNames[tool], "expected tool %q to be available", tool)
			}
			for _, tool := range fixture.Expected.Tools.Denied {
				assert.False(t, toolNames[tool], "expected tool %q to be denied", tool)
			}

			serialized, err := json.Marshal(context)
			require.NoError(t, err)
			encoded := string(serialized)
			modelContext := make(map[string]any)
			require.NoError(t, json.Unmarshal(serialized, &modelContext))
			assert.Equal(t, assistantSafeAccessLevel(context.AccessLevel), modelContext["access_level"])
			assert.Equal(t, strategy, modelContext["welcome_strategy"])
			assert.NotContains(t, modelContext, "customer_profile")
			assert.NotContains(t, encoded, fixture.User.Email, "raw email must not cross the model boundary")
			assert.NotContains(t, encoded, "matrix-password")
			assert.NotContains(t, encoded, "sk-matrix-secret")
			assert.NotContains(t, encoded, "internal-persona-strategy-secret")
			assert.NotContains(t, encoded, "profile_signals")
			assert.NotContains(t, encoded, "payment_restriction_causes")

			prompt := buildAssistantSystemPrompt(setting.GetAssistantSettings(), context)
			for _, expectedText := range fixture.Expected.WelcomeContains {
				assert.Contains(t, prompt, expectedText)
			}
			assert.NotContains(t, prompt, fixture.User.Email, "raw email must not enter the model prompt")
			assert.NotContains(t, prompt, "matrix-password")
			assert.NotContains(t, prompt, "sk-matrix-secret")
			assert.NotContains(t, prompt, "internal-persona-strategy-secret")
		})
	}

	for id, covered := range requiredIDs {
		assert.True(t, covered, "required persona %s is missing from the fixture", id)
	}
}

func TestAssistantPersonaMatrixKeepsInternalProfileStrategyOutOfModelContext(t *testing.T) {
	context := assistantUserContext{
		UserID:                   1,
		Username:                 "matrix-user password=matrix-password api_key=sk-matrix-secret",
		Email:                    "person@example.com",
		EmailCategory:            "common",
		AccessLevel:              "L1",
		CustomerProfile:          assistantProfileGuided,
		ManualProfileEnabled:     true,
		ManualProfileKey:         "internal-profile-key",
		ManualProfileTags:        []string{"internal-tag"},
		ManualProfileStrategy:    "internal-persona-strategy-secret",
		PaymentRestrictionCauses: []string{"internal-restriction-cause"},
		ProfileSignals:           []string{"internal-profile-signal"},
	}

	serialized, err := json.Marshal(context)
	require.NoError(t, err)
	encoded := string(serialized)
	for _, secret := range []string{
		"person@example.com",
		"matrix-password",
		"sk-matrix-secret",
		"internal-profile-key",
		"internal-tag",
		"internal-persona-strategy-secret",
		"internal-restriction-cause",
		"internal-profile-signal",
		"profile_signals",
		"payment_restriction_causes",
	} {
		assert.NotContains(t, encoded, secret)
	}

	prompt := buildAssistantSystemPrompt(setting.GetAssistantSettings(), context)
	for _, secret := range []string{
		"person@example.com",
		"matrix-password",
		"sk-matrix-secret",
		"internal-profile-key",
		"internal-tag",
		"internal-restriction-cause",
		"internal-profile-signal",
	} {
		assert.NotContains(t, prompt, secret)
	}
	assert.Contains(t, prompt, "internal-persona-strategy-secret", "the normalized administrator strategy is the only manual profile field the model needs")
}

func TestAssistantPersonaContextRedactsIdentitySecrets(t *testing.T) {
	context := assistantPersonaContextFromFixture(assistantPersonaUserFixture{
		Username:             "privacy-user",
		Email:                "person@example.com",
		AccessLevel:          "L1",
		PaymentMethodsHidden: true,
	})

	serialized, err := json.Marshal(context)
	require.NoError(t, err)
	assert.Equal(t, "pe***n@example.com", context.Email)
	assert.NotContains(t, string(serialized), "person@example.com")
	assert.NotContains(t, string(serialized), "password")
	assert.NotContains(t, string(serialized), "sk-")
	assert.Contains(t, string(serialized), "privacy-user")
}
