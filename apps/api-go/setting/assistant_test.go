package setting

import (
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"testing"
)

func TestAssistantReasoningEfforts(t *testing.T) {
	for _, effort := range []string{"auto", "none", "minimal", "low", "medium", "high", "xhigh", "max"} {
		if !IsAssistantReasoningEffort(effort) {
			t.Fatalf("expected %q to be a supported assistant reasoning effort", effort)
		}
	}
	if IsAssistantReasoningEffort("ultra") {
		t.Fatal("unexpected support for unknown assistant reasoning effort")
	}
}

func TestAssistantDefaultsAndValidation(t *testing.T) {
	settings := GetAssistantSettings()
	if !settings.Enabled {
		t.Fatal("assistant should be enabled by default")
	}
	if settings.Model != DefaultAssistantModel {
		t.Fatalf("unexpected default model: %q", settings.Model)
	}
	if settings.Group != DefaultAssistantGroup {
		t.Fatalf("unexpected default group: %q", settings.Group)
	}
	if settings.L1AutoApprovalUserIDs != DefaultAssistantL1AutoApprovalUserIDs {
		t.Fatalf("unexpected automatic approval allowlist: %q", settings.L1AutoApprovalUserIDs)
	}
	if settings.ReasoningEffort != DefaultAssistantReasoningEffort {
		t.Fatalf("unexpected default reasoning effort: %q", settings.ReasoningEffort)
	}
	if !settings.StreamEnabled || settings.Temperature != DefaultAssistantTemperature || settings.MaxTokens != DefaultAssistantMaxTokens || !settings.AgentLoopEnabled || settings.MaxSteps != 6 || settings.TimeoutSeconds != 45 || !settings.CacheEnabled || settings.CacheTTLMinutes != 1440 {
		t.Fatalf("unexpected assistant runtime defaults: %+v", settings)
	}
	if !settings.RetentionEnabled || settings.ActiveRetentionDays != 90 || settings.ArchivedRetentionDays != 30 || settings.SecurityRetentionDays != 180 || settings.RetentionIntervalHours != 24 {
		t.Fatalf("unexpected assistant retention defaults: %+v", settings)
	}
	if !settings.ReviewEnabled || settings.ReviewWindowDays != 30 || settings.ReviewIntervalHours != 24 || settings.ReviewProbability != 0 || settings.ReviewGroup != DefaultAssistantReviewGroup || settings.ReviewModel != DefaultAssistantReviewModel || settings.ReviewReasoningEffort != DefaultAssistantReviewReasoningEffort || len(settings.ReviewGroupPolicies) != 0 {
		t.Fatalf("unexpected assistant review defaults: %+v", settings)
	}

	invalid := map[string]string{
		AssistantModelOptionKey:                  " ",
		AssistantGroupOptionKey:                  " ",
		AssistantL1AutoApprovalUserIDsOptionKey:  "1,not-an-id",
		AssistantReasoningEffortOptionKey:        "extreme",
		AssistantStreamEnabledOptionKey:          "not-a-bool",
		AssistantTemperatureOptionKey:            "2.1",
		AssistantMaxTokensOptionKey:              "63",
		AssistantMaxStepsOptionKey:               "13",
		AssistantTimeoutSecondsOptionKey:         "4",
		AssistantCacheTTLMinutesOptionKey:        "10081",
		AssistantReviewWindowDaysOptionKey:       "0",
		AssistantReviewIntervalHoursOptionKey:    "169",
		AssistantReviewProbabilityOptionKey:      "100.1",
		AssistantReviewGroupOptionKey:            " ",
		AssistantReviewModelOptionKey:            " ",
		AssistantReviewReasoningEffortOptionKey:  "extreme",
		AssistantReviewGroupPoliciesOptionKey:    `{"default":{"probability":1,"intensity":"unknown"}}`,
		AssistantActiveRetentionDaysOptionKey:    "6",
		AssistantArchivedRetentionDaysOptionKey:  "0",
		AssistantSecurityRetentionDaysOptionKey:  "29",
		AssistantRetentionIntervalHoursOptionKey: "169",
	}
	for key, value := range invalid {
		if err := ValidateAssistantOption(key, value); err == nil {
			t.Fatalf("expected validation error for %s=%q", key, value)
		}
	}
	if err := ValidateAssistantOption(AssistantWeeklyCreditUSDOptionKey, "retired-value"); err != nil {
		t.Fatalf("retired weekly credit option must remain write-compatible: %v", err)
	}
}

func TestAssistantSettingsUpdates(t *testing.T) {
	original := GetAssistantSettings()
	t.Cleanup(func() {
		SetAssistantEnabled(original.Enabled)
		_ = UpdateAssistantModel(original.Model)
		_ = UpdateAssistantGroup(original.Group)
		_ = UpdateAssistantL1AutoApprovalUserIDs(original.L1AutoApprovalUserIDs)
		_ = UpdateAssistantReasoningEffort(original.ReasoningEffort)
		SetAssistantStreamEnabled(original.StreamEnabled)
		_ = UpdateAssistantTemperature(strconv.FormatFloat(original.Temperature, 'f', -1, 64))
		_ = UpdateAssistantMaxTokens(strconv.Itoa(original.MaxTokens))
		SetAssistantAgentLoopEnabled(original.AgentLoopEnabled)
		_ = UpdateAssistantMaxSteps(strconv.Itoa(original.MaxSteps))
		_ = UpdateAssistantTimeoutSeconds(strconv.Itoa(original.TimeoutSeconds))
		SetAssistantCacheEnabled(original.CacheEnabled)
		_ = UpdateAssistantCacheTTLMinutes(strconv.Itoa(original.CacheTTLMinutes))
		SetAssistantReviewEnabled(original.ReviewEnabled)
		_ = UpdateAssistantReviewWindowDays(strconv.Itoa(original.ReviewWindowDays))
		_ = UpdateAssistantReviewIntervalHours(strconv.Itoa(original.ReviewIntervalHours))
		_ = UpdateAssistantReviewProbability(strconv.FormatFloat(original.ReviewProbability, 'f', -1, 64))
		_ = UpdateAssistantReviewGroup(original.ReviewGroup)
		_ = UpdateAssistantReviewModel(original.ReviewModel)
		_ = UpdateAssistantReviewReasoningEffort(original.ReviewReasoningEffort)
		_ = UpdateAssistantReviewGroupPolicies(AssistantReviewGroupPoliciesJSON(original.ReviewGroupPolicies))
		SetAssistantRetentionEnabled(original.RetentionEnabled)
		_ = UpdateAssistantActiveRetentionDays(strconv.Itoa(original.ActiveRetentionDays))
		_ = UpdateAssistantArchivedRetentionDays(strconv.Itoa(original.ArchivedRetentionDays))
		_ = UpdateAssistantSecurityRetentionDays(strconv.Itoa(original.SecurityRetentionDays))
		_ = UpdateAssistantRetentionIntervalHours(strconv.Itoa(original.RetentionIntervalHours))
	})

	SetAssistantEnabled(false)
	if err := UpdateAssistantModel(" custom-model "); err != nil {
		t.Fatal(err)
	}
	if err := UpdateAssistantGroup(" premium "); err != nil {
		t.Fatal(err)
	}
	if err := UpdateAssistantL1AutoApprovalUserIDs("42,7,42"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateAssistantReasoningEffort("MAX"); err != nil {
		t.Fatal(err)
	}
	SetAssistantStreamEnabled(false)
	if err := UpdateAssistantTemperature("1.1"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateAssistantMaxTokens("2048"); err != nil {
		t.Fatal(err)
	}
	SetAssistantAgentLoopEnabled(false)
	if err := UpdateAssistantMaxSteps("9"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateAssistantTimeoutSeconds("60"); err != nil {
		t.Fatal(err)
	}
	SetAssistantCacheEnabled(false)
	if err := UpdateAssistantCacheTTLMinutes("30"); err != nil {
		t.Fatal(err)
	}
	SetAssistantReviewEnabled(false)
	if err := UpdateAssistantReviewWindowDays("14"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateAssistantReviewIntervalHours("6"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateAssistantReviewProbability("1.0"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateAssistantReviewGroup(" review-premium "); err != nil {
		t.Fatal(err)
	}
	if err := UpdateAssistantReviewModel("review-model"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateAssistantReviewReasoningEffort("XHIGH"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateAssistantReviewGroupPolicies(`{"free":{"probability":0,"intensity":"off"},"premium":{"probability":25,"intensity":"high"}}`); err != nil {
		t.Fatal(err)
	}
	SetAssistantRetentionEnabled(false)
	if err := UpdateAssistantActiveRetentionDays("120"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateAssistantArchivedRetentionDays("45"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateAssistantSecurityRetentionDays("365"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateAssistantRetentionIntervalHours("12"); err != nil {
		t.Fatal(err)
	}

	settings := GetAssistantSettings()
	if settings.Enabled || settings.Model != "custom-model" || settings.Group != "premium" || settings.L1AutoApprovalUserIDs != "7,42" || settings.ReasoningEffort != "max" || settings.StreamEnabled || settings.Temperature != 1.1 || settings.MaxTokens != 2048 || settings.AgentLoopEnabled || settings.MaxSteps != 9 || settings.TimeoutSeconds != 60 || settings.CacheEnabled || settings.CacheTTLMinutes != 30 || settings.ReviewEnabled || settings.ReviewWindowDays != 14 || settings.ReviewIntervalHours != 6 || settings.ReviewProbability != 1 || settings.ReviewGroup != "review-premium" || settings.ReviewModel != "review-model" || settings.ReviewReasoningEffort != "xhigh" || settings.ReviewGroupPolicies["premium"].Intensity != "high" || settings.RetentionEnabled || settings.ActiveRetentionDays != 120 || settings.ArchivedRetentionDays != 45 || settings.SecurityRetentionDays != 365 || settings.RetentionIntervalHours != 12 {
		t.Fatalf("unexpected updated settings: %+v", settings)
	}
	if !AssistantL1AutoApprovalUserAllowed(7) || AssistantL1AutoApprovalUserAllowed(8) {
		t.Fatalf("automatic approval allowlist was not enforced")
	}
}

func TestAssistantSearchURLValidationBlocksPrivateTargetsAndCredentials(t *testing.T) {
	valid := []string{
		"https://search.example.com/api/search?q=initial",
		"http://8.8.8.8/search",
	}
	for _, value := range valid {
		if err := ValidateAssistantSearchURL(value); err != nil {
			t.Fatalf("expected search URL %q to be valid: %v", value, err)
		}
	}
	invalid := []string{
		"ftp://search.example.com/api",
		"http://user:password@search.example.com/api",
		"http://127.0.0.1/api",
		"http://10.0.0.7/api",
		"http://169.254.169.254/latest/meta-data",
		"http://[::1]/api",
	}
	for _, value := range invalid {
		if err := ValidateAssistantSearchURL(value); err == nil {
			t.Fatalf("expected search URL %q to be rejected", value)
		}
	}
}

func TestAssistantSearchPublicIPPolicy(t *testing.T) {
	cases := []struct {
		address string
		public  bool
	}{
		{address: "8.8.8.8", public: true},
		{address: "2001:4860:4860::8888", public: true},
		{address: "10.0.0.1", public: false},
		{address: "100.64.0.1", public: false},
		{address: "192.0.2.1", public: false},
		{address: "fd00::1", public: false},
	}
	for _, testCase := range cases {
		ip := net.ParseIP(testCase.address)
		if got := IsAssistantSearchPublicIP(ip); got != testCase.public {
			t.Fatalf("IsAssistantSearchPublicIP(%q) = %t, want %t", testCase.address, got, testCase.public)
		}
	}
}

func TestAssistantSkillFilesAreBoundedDeterministicAndSecretSafe(t *testing.T) {
	raw, err := json.Marshal([]AssistantSkillFile{
		{Path: "skills/z-last/SKILL.md", Content: "---\nname: z-last\ndescription: Last skill\n---\nlast", Enabled: false},
		{Path: "skills/a-first/SKILL.md", Content: "---\nname: a-first\ndescription: First skill\n---\nfirst", Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	files, err := NormalizeAssistantSkillFiles(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Path != "skills/a-first/SKILL.md" || files[1].Path != "skills/z-last/SKILL.md" {
		t.Fatalf("skill files were not sorted deterministically: %+v", files)
	}
	prompt := AssistantSkillPromptForFiles(files)
	if prompt == "" || !strings.Contains(prompt, "first") || strings.Contains(prompt, "last") {
		t.Fatalf("unexpected enabled-skill prompt: %q", prompt)
	}

	invalid := []string{
		`[{"path":"../secret/SKILL.md","content":"---\nname: secret\ndescription: x\n---\nx","enabled":true}]`,
		`[{"path":"skills/key/SKILL.md","content":"---\nname: key\ndescription: x\n---\nAPI key: abc123","enabled":true}]`,
		`[{"path":"skills/a/SKILL.md","content":"---\nname: a\ndescription: x\n---\nx","enabled":true},{"path":"skills/a/SKILL.md","content":"---\nname: a\ndescription: y\n---\ny","enabled":true}]`,
	}
	for _, value := range invalid {
		if _, err := NormalizeAssistantSkillFiles(value); err == nil {
			t.Fatalf("expected invalid skill file payload to be rejected: %s", value)
		}
	}
}
