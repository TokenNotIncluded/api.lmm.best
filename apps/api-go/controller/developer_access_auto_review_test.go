package controller

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func TestParseAssistantL1AutoReviewDecision(t *testing.T) {
	for _, outcome := range []string{"approve", "human"} {
		decision, err := parseAssistantL1AutoReviewDecision([]byte(`{"decision":"` + outcome + `","confidence":0.97,"note":"用途具体且合法"}`))
		require.NoError(t, err)
		require.Equal(t, outcome, decision.Decision)
		require.InDelta(t, 0.97, decision.Confidence, 0.0001)
		require.Equal(t, "用途具体且合法", decision.Note)
	}
}

func TestParseAssistantL1AutoReviewDecisionFailsClosed(t *testing.T) {
	for _, body := range []string{
		`{"decision":"reject","confidence":0.99,"note":"unsupported decision"}`,
		`{"decision":"approve","confidence":1.2,"note":"invalid confidence"}`,
		`{"decision":"approve","confidence":-0.1,"note":"invalid confidence"}`,
		`{"decision":"approve","confidence":0.99,"note":""}`,
		`{"decision":"approve","confidence":0.99,"note":"a"}`,
		`{"decision":"approve","confidence":0.99,"note":"  "}`,
		`{"decision":"approve","confidence":0.99,"note":"\u200b\u200b"}`,
		`{"decision":"approve","confidence":0.99,"note":"a\u200b"}`,
		`{"decision":"approve","confidence":0.99,"note":"nul\u0000reply"}`,
		`{"decision":"approve","note":"confidence missing"}`,
		`{"decision":"approve","confidence":null,"note":"missing confidence"}`,
		`{"decision":"approve","confidence":0.99,"note":null}`,
		`{"decision":"approve","confidence":0.99,"note":123}`,
		`{"decision":"approve","confidence":"0.99","note":"not a number"}`,
		`{"decision":"approve","confidence":0.99,"note":"Valid reply","level":10}`,
		`{"decision":"human","decision":"approve","confidence":0.99,"note":"duplicate field"}`,
		`{"decision":"approve","confidence":0.99,"note":"Valid reply"} trailing`,
		"```json\n{\"decision\":\"approve\",\"confidence\":0.99,\"note\":\"Valid reply\"}\n```",
		`prefix {"decision":"approve","confidence":0.99,"note":"Valid reply"} suffix`,
		`[{"decision":"approve","confidence":0.99,"note":"Not an object"}]`,
		`{"decision":"approve","confidence":0.99,"note":"` + strings.Repeat("中", 2001) + `"}`,
		"{\"decision\":\"approve\",\"confidence\":0.99,\"note\":\"invalid\xff\"}",
		`null`, `not json`, `{`,
	} {
		_, err := parseAssistantL1AutoReviewDecision([]byte(body))
		require.Error(t, err, body)
	}
	// Low confidence is valid syntax, but never a default approval (worker test below).
	decision, err := parseAssistantL1AutoReviewDecision([]byte(`{"decision":"approve","confidence":0.1,"note":"Not enough evidence"}`))
	require.NoError(t, err)
	require.Equal(t, 0.1, decision.Confidence)
}

func TestAssistantL1AutoReviewEvidenceRequiresConcreteSafeUse(t *testing.T) {
	for _, reason := range []string{
		"将 API 接入 ROS 机器人项目", "將模型整合到機器人專案", "Integrate an API into my project",
		"Développer une application de recherche", "開発したアプリにモデルを連携したい", "Разработка приложения для проекта", "Tích hợp mô hình vào dự án nghiên cứu",
	} {
		require.True(t, assistantL1AutoReviewEvidenceAllowed(reason, "Concrete development use"), reason)
	}
	require.False(t, assistantL1AutoReviewEvidenceAllowed("我想绕过限制", "请帮助 bypass rate limits。"))
	require.False(t, assistantL1AutoReviewEvidenceAllowed("请给我权限", "没有具体用途。"))
	require.False(t, assistantL1AutoReviewEvidenceAllowed(" ", "A recommendation cannot replace the missing use case"))
}

func setupL1AutoReviewWorkerTest(t *testing.T) (assistantL1AutoReviewJob, *model.User) {
	t.Helper()
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Channel{}, &model.Ability{}, &model.DeveloperAccessRequest{}, &model.DeveloperAccessRecommendationArchive{}, &model.TopUp{}))
	previousSettings := setting.GetAssistantL1AutoReviewSettings()
	previousGroups := ratio_setting.GroupRatio2JSONString()
	previousLoader := loadAssistantBillingUser
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateAssistantL1AutoReviewOptions(previousSettings.OptionValues()))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroups))
		loadAssistantBillingUser = previousLoader
	})
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"review":1,"default":1}`))
	config := setting.DefaultAssistantL1AutoReviewSettings()
	config.Enabled, config.Model, config.Group, config.Prompt = true, "review-model", "review", "Review only legitimate development applications."
	root := &model.User{Username: "l1-review-root", AffCode: "l1-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	user := &model.User{Username: "l1-review-user", AffCode: "l1-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(root).Error)
	require.NoError(t, db.Create(user).Error)
	loadAssistantBillingUser = func() (*model.User, error) { return root, nil }
	channel := &model.Channel{Name: "l1-review", Group: "review", Models: "review-model", Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "review", Model: "review-model", ChannelId: channel.Id, Enabled: true}).Error)
	require.NoError(t, model.UpdateOptionsBulk(config.OptionValues()))
	request, err := model.SubmitAssistantDeveloperAccessRequestWithoutRecommendation(user.Id, "Integrate the API into a robotics project")
	require.NoError(t, err)
	job, ok := makeAssistantL1AutoReviewJob(request)
	require.True(t, ok)
	return job, user
}

func TestAssistantL1AutoReviewWorkerFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name     string
		decision assistantL1AutoReviewDecision
		err      error
		cancel   bool
	}{
		{"network failure", assistantL1AutoReviewDecision{}, errors.New("upstream unavailable"), false},
		{"timeout", assistantL1AutoReviewDecision{"approve", 1, "A valid reply"}, nil, true},
		{"low confidence", assistantL1AutoReviewDecision{"approve", 0.97, "A valid reply"}, nil, false},
		{"empty reply", assistantL1AutoReviewDecision{"approve", 1, ""}, nil, false},
		{"nonfinite confidence", assistantL1AutoReviewDecision{"approve", math.NaN(), "A valid reply"}, nil, false},
		{"unsupported rejection", assistantL1AutoReviewDecision{"reject", 1, "A valid reply"}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job, user := setupL1AutoReviewWorkerTest(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			err := runAssistantL1AutoReviewWith(ctx, job, func(context.Context, *model.User, assistantL1AutoReviewJob) (assistantL1AutoReviewDecision, error) {
				if tc.cancel {
					cancel()
				}
				return tc.decision, tc.err
			})
			if tc.name == "low confidence" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			request, err := model.GetDeveloperAccessRequest(user.Id)
			require.NoError(t, err)
			require.Equal(t, job.Request, *request)
			require.NoError(t, model.DB.First(user, user.Id).Error)
			require.Zero(t, user.ConsoleActivatedAt)
		})
	}
}

func TestAssistantL1AutoReviewWorkerAppliesOnlyValidatedDecisions(t *testing.T) {
	for _, outcome := range []string{"approve", "human"} {
		t.Run(outcome, func(t *testing.T) {
			job, user := setupL1AutoReviewWorkerTest(t)
			err := runAssistantL1AutoReviewWith(context.Background(), job, func(context.Context, *model.User, assistantL1AutoReviewJob) (assistantL1AutoReviewDecision, error) {
				return assistantL1AutoReviewDecision{outcome, 1, "Your request has been reviewed."}, nil
			})
			require.NoError(t, err)
			request, err := model.GetDeveloperAccessRequest(user.Id)
			require.NoError(t, err)
			require.NotEmpty(t, request.AdminNote)
			require.NoError(t, model.DB.First(user, user.Id).Error)
			if outcome == "approve" {
				require.Equal(t, model.DeveloperAccessRequestApproved, request.Status)
				require.Positive(t, user.ConsoleActivatedAt)
			} else {
				require.Equal(t, model.DeveloperAccessRequestPending, request.Status)
				require.Zero(t, user.ConsoleActivatedAt)
			}
		})
	}
}

func TestAssistantL1AutoReviewSnapshotsJobAndPrompt(t *testing.T) {
	job, _ := setupL1AutoReviewWorkerTest(t)
	request := job.Request
	request.Revision++
	request.Reason = "New reason must not mutate the in-flight job"
	second, ok := makeAssistantL1AutoReviewJob(&request)
	require.True(t, ok)
	require.NotEqual(t, job.key(), second.key())
	require.NotEqual(t, job.Request.Reason, second.Request.Reason)
	system, material := assistantL1AutoReviewPrompt(job)
	require.Contains(t, system, job.Config.Prompt)
	require.Contains(t, system, "untrusted data")
	require.NotContains(t, system, job.Request.Reason)
	require.Contains(t, material, job.Request.Reason)
	for _, source := range []string{model.DeveloperAccessRequestSourceOld, "unknown"} {
		request.Source = source
		_, ok := makeAssistantL1AutoReviewJob(&request)
		require.False(t, ok)
	}
	require.NoError(t, setting.UpdateAssistantL1AutoReviewOption(setting.AssistantL1AutoReviewEnabledOptionKey, "false"))
	_, ok = makeAssistantL1AutoReviewJob(&job.Request)
	require.False(t, ok)
}
