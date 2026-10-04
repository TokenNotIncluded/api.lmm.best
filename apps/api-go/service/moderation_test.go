package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type moderationRoundTripper func(*http.Request) (*http.Response, error)

func (transport moderationRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

const moderationFixture = `{"id":"modr-offline-test","model":"omni-moderation-2024-09-26","results":[{"flagged":true,"categories":{"violence":true,"hate":false},"category_scores":{"violence":0.99,"hate":0.01}}]}`

func TestParseModerationResponseUsesOfficialCategoryDecisions(t *testing.T) {
	decision, err := parseModerationResponse([]byte(moderationFixture))
	require.NoError(t, err)
	require.True(t, decision.Flagged)
	require.Equal(t, []string{"violence"}, decision.Categories)
	require.Equal(t, "omni-moderation-2024-09-26", decision.ResponseModel)
	require.Equal(t, 0.99, decision.Scores["violence"])
	clear := strings.ReplaceAll(strings.ReplaceAll(moderationFixture, `"flagged":true`, `"flagged":false`), `"violence":true`, `"violence":false`)
	decision, err = parseModerationResponse([]byte(clear))
	require.NoError(t, err)
	require.False(t, decision.Flagged)
	require.Empty(t, decision.Categories)
}

func TestMalformedModerationResponsesNeverBecomeViolations(t *testing.T) {
	for name, body := range map[string]string{
		"missing flag":       strings.Replace(moderationFixture, `"flagged":true,`, "", 1),
		"null flag":          strings.Replace(moderationFixture, `"flagged":true`, `"flagged":null`, 1),
		"unknown category":   strings.ReplaceAll(moderationFixture, "violence", "unrecognized-category"),
		"negative score":     strings.Replace(moderationFixture, "0.99", "-0.2", 1),
		"oversized score":    strings.Replace(moderationFixture, "0.99", "1.01", 1),
		"missing scores":     strings.Replace(moderationFixture, `"violence":0.99,`, "", 1),
		"null category":      strings.Replace(moderationFixture, `"violence":true`, `"violence":null`, 1),
		"inconsistent clear": strings.Replace(moderationFixture, `"flagged":true`, `"flagged":false`, 1),
		"unknown model":      strings.Replace(moderationFixture, "omni-moderation-2024-09-26", "chat-model", 1),
		"missing ID":         strings.Replace(moderationFixture, "modr-offline-test", "", 1),
		"empty results":      `{"id":"modr-offline-test","model":"omni-moderation-latest","results":[]}`,
		"provider error":     `{"error":{"message":"sensitive-provider-body"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			decision, err := parseModerationResponse([]byte(body))
			require.EqualError(t, err, "moderation_response_invalid")
			require.False(t, decision.Flagged)
			require.Empty(t, decision.Categories)
			require.NotContains(t, err.Error(), "sensitive-provider-body")
		})
	}
}

func TestModerationChunksIncludeLongUnicodeTailAndBatchVerdictsMerge(t *testing.T) {
	text := strings.Repeat("界", 32_000) + "final-tail-content"
	chunks := moderationTextChunks(text)
	require.Greater(t, len(chunks), 2)
	require.True(t, strings.HasSuffix(chunks[len(chunks)-1], "final-tail-content"))
	for _, chunk := range chunks {
		require.True(t, utf8.ValidString(chunk))
		require.LessOrEqual(t, len(chunk), moderationChunkMaxBytes)
	}
	batch := `{"id":"modr-batch","model":"omni-moderation-latest","results":[{"flagged":true,"categories":{"hate":true,"violence":false},"category_scores":{"hate":0.9,"violence":0.01}},{"flagged":true,"categories":{"hate":false,"violence":true},"category_scores":{"hate":0.01,"violence":0.98}}]}`
	decision, err := parseModerationBatchResponse([]byte(batch), 2)
	require.NoError(t, err)
	require.Equal(t, []string{"hate", "violence"}, decision.Categories)
	require.Equal(t, 0.9, decision.Scores["hate"])
	require.Equal(t, 0.98, decision.Scores["violence"])
	_, err = parseModerationBatchResponse([]byte(batch), 1)
	require.EqualError(t, err, "moderation_response_invalid")
	invalidSecond := strings.Replace(batch, `"violence":0.98`, `"violence":null`, 1)
	decision, err = parseModerationBatchResponse([]byte(invalidSecond), 2)
	require.Error(t, err)
	require.False(t, decision.Flagged)
	require.Empty(t, decision.Categories)
}

func TestOfficialModerationRequestHasFixedOriginAndNoChatFields(t *testing.T) {
	client := &http.Client{Transport: moderationRoundTripper(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, moderationEndpoint, request.URL.String())
		require.Equal(t, http.MethodPost, request.Method)
		require.Equal(t, "Bearer dummy-offline-test-key", request.Header.Get("Authorization"))
		require.Equal(t, "offline-org", request.Header.Get("OpenAI-Organization"))
		var fields map[string]any
		require.NoError(t, json.NewDecoder(request.Body).Decode(&fields))
		require.Equal(t, map[string]any{"model": "omni-moderation-latest", "input": []any{"one user turn"}}, fields)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(moderationFixture)), Header: http.Header{}}, nil
	})}
	decision, err := requestOfficialModeration(context.Background(), client, "omni-moderation-latest", []string{"one user turn"}, "dummy-offline-test-key", "offline-org")
	require.NoError(t, err)
	require.True(t, decision.Flagged)
	transport := newModerationHTTPClient().Transport.(*http.Transport)
	require.Nil(t, transport.Proxy)
	require.ErrorIs(t, newModerationHTTPClient().CheckRedirect(nil, nil), http.ErrUseLastResponse)
}

func TestOfficialModerationFailureDoesNotExposeCredentialOrBody(t *testing.T) {
	for name, transport := range map[string]moderationRoundTripper{
		"network": func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dummy-offline-test-key in raw network failure")
		},
		"upstream": func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("private-provider-body"))}, nil
		},
		"oversize": func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", moderationResponseMaxBytes+1)))}, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			decision, err := requestOfficialModeration(context.Background(), &http.Client{Transport: transport}, "omni-moderation-latest", []string{"text"}, "dummy-offline-test-key", "")
			require.Error(t, err)
			require.False(t, decision.Flagged)
			require.NotContains(t, err.Error(), "dummy-offline-test-key")
			require.NotContains(t, err.Error(), "private-provider-body")
		})
	}
}

func TestModerationKeySelectionSkipsDisabledKeysWithoutDatabaseWrites(t *testing.T) {
	channel := &model.Channel{Key: "disabled-dummy-key\nenabled-dummy-key\n", ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeyMode: constant.MultiKeyModePolling, MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled}}}
	previous := model.DB
	model.DB = nil
	t.Cleanup(func() { model.DB = previous })
	for index := 0; index < 20; index++ {
		require.Equal(t, "enabled-dummy-key", moderationEnabledKey(channel))
	}
	channel.ChannelInfo.MultiKeyStatusList[1] = common.ChannelStatusAutoDisabled
	require.Empty(t, moderationEnabledKey(channel))
}

func TestQueueModerationPersistsWithoutWaitingForProvider(t *testing.T) {
	db, userID := setupAssistantFundingTestDB(t, 1000)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.ModerationJob{}))
	settings := setting.DefaultModerationSettings()
	settings.Enabled = true
	settings.GroupPolicies = map[string]setting.ModerationGroupPolicy{
		"default": {Mode: setting.ModerationModeStrict, CategoryFinesUSD: map[string]float64{"violence": 0.5}},
	}
	for key, value := range settings.OptionValues() {
		require.NoError(t, db.Create(&model.Option{Key: key, Value: value}).Error)
	}
	previousClient := moderationHTTPClient
	var called atomic.Bool
	moderationHTTPClient = &http.Client{Transport: moderationRoundTripper(func(*http.Request) (*http.Response, error) {
		called.Store(true)
		return nil, errors.New("a request handler must never reach the provider")
	})}
	t.Cleanup(func() { moderationHTTPClient = previousClient })
	submission := ModerationSubmission{UserID: userID, Source: ModerationSourceRelayInput, RequestID: "one-real-request", Group: "default", Text: strings.Repeat("界", 8000)}
	started := time.Now()
	require.NoError(t, QueueModeration(context.Background(), submission))
	require.Less(t, time.Since(started), 200*time.Millisecond)
	require.False(t, called.Load())
	var job model.ModerationJob
	require.NoError(t, db.First(&job).Error)
	require.LessOrEqual(t, len(job.Payload), model.ModerationMaxPayloadBytes)
	require.True(t, utf8.ValidString(job.Payload))
	require.Equal(t, "default", job.Group)
	require.Equal(t, "omni-moderation-latest", job.ReviewModel)
	require.NoError(t, QueueModeration(context.Background(), submission))
	var count int64
	require.NoError(t, db.Model(&model.ModerationJob{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, QueueModeration(context.Background(), ModerationSubmission{UserID: userID, Source: ModerationSourceRelayInput, RequestID: "unconfigured-group", Group: "other", Text: "text"}))
	require.NoError(t, db.Model(&model.ModerationJob{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestModerationMaximumASCIIInputEnqueuesWithinLatencyBudget(t *testing.T) {
	if moderationRaceEnabled {
		t.Skip("production latency is measured without race instrumentation of the SQLite driver")
	}
	db, userID := setupAssistantFundingTestDB(t, 1000)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.ModerationJob{}))
	settings := setting.DefaultModerationSettings()
	settings.Enabled = true
	settings.GroupPolicies = map[string]setting.ModerationGroupPolicy{"default": {Mode: setting.ModerationModeTolerant}}
	for key, value := range settings.OptionValues() {
		require.NoError(t, db.Create(&model.Option{Key: key, Value: value}).Error)
	}
	text := strings.Repeat("x", model.ModerationMaxPayloadBytes)
	started := time.Now()
	require.NoError(t, QueueModeration(context.Background(), ModerationSubmission{UserID: userID, Source: ModerationSourceRelayInput, RequestID: "maximum-ascii", Group: "default", Text: text}))
	elapsed := time.Since(started)
	require.Less(t, elapsed, moderationEnqueueTimeout)
	t.Logf("maximum 256KiB ASCII enqueue including redaction and persistence: %s", elapsed)
	var job model.ModerationJob
	require.NoError(t, db.First(&job).Error)
	require.Equal(t, text, job.Payload)
	require.False(t, job.InputTruncated)
}

func TestModerationDefaultDisabledDoesNotEnqueue(t *testing.T) {
	db, userID := setupAssistantFundingTestDB(t, 1000)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.ModerationJob{}))
	for _, source := range []string{ModerationSourceRelayInput, ModerationSourceAssistantInput, ModerationSourceAssistantOutput} {
		require.NoError(t, QueueModeration(context.Background(), ModerationSubmission{UserID: userID, Source: source, RequestID: "disabled-" + source, Group: "default", Text: "text"}))
	}
	var count int64
	require.NoError(t, db.Model(&model.ModerationJob{}).Count(&count).Error)
	require.Zero(t, count)
}

func setupModerationPipelineJob(t *testing.T, text string) (*gorm.DB, *model.ModerationJob) {
	t.Helper()
	db, userID := setupAssistantFundingTestDB(t, 1000)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Channel{}, &model.Ability{}, &model.ModerationJob{}, &model.ModerationNotice{}, &model.AssistantRequestReview{}, &model.ViolationFeeRecord{}))
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", userID).Update("group", "default").Error)
	settings := setting.DefaultModerationSettings()
	settings.Enabled = true
	settings.GroupPolicies = map[string]setting.ModerationGroupPolicy{"default": {Mode: setting.ModerationModeTolerant}}
	for key, value := range settings.OptionValues() {
		require.NoError(t, db.Create(&model.Option{Key: key, Value: value}).Error)
	}
	channel := model.Channel{Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Key: "dummy-offline-test-key", Models: setting.DefaultModerationModel, Group: "default"}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: setting.DefaultModerationModel, ChannelId: channel.Id, Enabled: true}).Error)
	// Worker integration exercises the durable queue directly. The production
	// ingress deadline is tested separately, without race instrumentation of
	// large pure-Go SQLite inserts distorting the performance measurement.
	created, err := model.EnqueueModerationJob(context.Background(), &model.ModerationJob{
		UserID: userID, Source: ModerationSourceRelayInput, RequestID: "pipeline-test-turn", Group: "default",
		ReviewGroup: "default", ReviewModel: setting.DefaultModerationModel, Payload: text,
		CapturedMode: setting.ModerationModeTolerant, CapturedCategoryFinesJSON: "{}",
	})
	require.NoError(t, err)
	require.True(t, created)
	job, err := model.ClaimModerationJob(context.Background(), "pipeline-test-worker", time.Now().Unix(), moderationLeaseSeconds)
	require.NoError(t, err)
	require.NotNil(t, job)
	return db, job
}

func offlineModerationBatchResponse(t *testing.T, request *http.Request, flagTail bool) *http.Response {
	t.Helper()
	var payload struct {
		Input []string `json:"input"`
	}
	require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
	require.LessOrEqual(t, len(payload.Input), moderationBatchMaxChunks)
	results := make([]map[string]any, 0, len(payload.Input))
	for _, chunk := range payload.Input {
		flagged := flagTail && strings.Contains(chunk, "tail-risk-marker")
		results = append(results, map[string]any{"flagged": flagged, "categories": map[string]bool{"violence": flagged}, "category_scores": map[string]float64{"violence": 0.8}})
	}
	body, err := json.Marshal(map[string]any{"id": "modr-offline-batch", "model": "omni-moderation-2024-09-26", "results": results})
	require.NoError(t, err)
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body)))}
}

func TestModerationWorkerReviewsLongTailBeforeCommittingOneWarning(t *testing.T) {
	text := strings.Repeat("界", 32_000) + "tail-risk-marker"
	db, job := setupModerationPipelineJob(t, text)
	previous := moderationHTTPClient
	var requests int
	moderationHTTPClient = &http.Client{Transport: moderationRoundTripper(func(request *http.Request) (*http.Response, error) {
		requests++
		return offlineModerationBatchResponse(t, request, true), nil
	})}
	t.Cleanup(func() { moderationHTTPClient = previous })
	processModerationJob(context.Background(), "pipeline-test-worker", job)
	require.Greater(t, requests, 1)
	var stored model.ModerationJob
	require.NoError(t, db.First(&stored, job.ID).Error)
	require.Equal(t, model.ModerationJobCompleted, stored.Status)
	require.True(t, stored.Flagged)
	require.Empty(t, stored.Payload)
	require.Zero(t, stored.ChargedQuota)
	var notices int64
	require.NoError(t, db.Model(&model.ModerationNotice{}).Count(&notices).Error)
	require.EqualValues(t, 1, notices)
}

func TestModerationBatchFailureNeverCommitsPartialClassification(t *testing.T) {
	db, job := setupModerationPipelineJob(t, strings.Repeat("界", 32_000)+"tail-risk-marker")
	previous := moderationHTTPClient
	var requests int
	moderationHTTPClient = &http.Client{Transport: moderationRoundTripper(func(request *http.Request) (*http.Response, error) {
		requests++
		if requests == 2 {
			return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("private-provider-error"))}, nil
		}
		return offlineModerationBatchResponse(t, request, true), nil
	})}
	t.Cleanup(func() { moderationHTTPClient = previous })
	processModerationJob(context.Background(), "pipeline-test-worker", job)
	var stored model.ModerationJob
	require.NoError(t, db.First(&stored, job.ID).Error)
	require.Equal(t, model.ModerationJobPending, stored.Status)
	require.False(t, stored.Flagged)
	require.Zero(t, stored.ChargedQuota)
	require.Equal(t, "moderation_provider_rejected", stored.ErrorMessage)
	var notices int64
	require.NoError(t, db.Model(&model.ModerationNotice{}).Count(&notices).Error)
	require.Zero(t, notices)
}

func TestModerationDisabledDuringProviderCallCancelsWithoutEffects(t *testing.T) {
	db, job := setupModerationPipelineJob(t, "tail-risk-marker")
	previous := moderationHTTPClient
	moderationHTTPClient = &http.Client{Transport: moderationRoundTripper(func(request *http.Request) (*http.Response, error) {
		require.NoError(t, db.Model(&model.Option{}).Where("key = ?", setting.ModerationEnabledOptionKey).Update("value", "false").Error)
		return offlineModerationBatchResponse(t, request, true), nil
	})}
	t.Cleanup(func() { moderationHTTPClient = previous })
	processModerationJob(context.Background(), "pipeline-test-worker", job)
	var stored model.ModerationJob
	require.NoError(t, db.First(&stored, job.ID).Error)
	require.Equal(t, model.ModerationJobCancelled, stored.Status)
	require.Zero(t, stored.ChargedQuota)
	var notices int64
	require.NoError(t, db.Model(&model.ModerationNotice{}).Count(&notices).Error)
	require.Zero(t, notices)
}

func TestModerationDisabledAfterFirstBatchStopsSendingRemainingText(t *testing.T) {
	db, job := setupModerationPipelineJob(t, strings.Repeat("界", 32_000)+"tail-risk-marker")
	previous := moderationHTTPClient
	var requests int
	moderationHTTPClient = &http.Client{Transport: moderationRoundTripper(func(request *http.Request) (*http.Response, error) {
		requests++
		require.NoError(t, db.Model(&model.Option{}).Where("key = ?", setting.ModerationEnabledOptionKey).Update("value", "false").Error)
		return offlineModerationBatchResponse(t, request, true), nil
	})}
	t.Cleanup(func() { moderationHTTPClient = previous })
	processModerationJob(context.Background(), "pipeline-test-worker", job)
	require.Equal(t, 1, requests)
	var stored model.ModerationJob
	require.NoError(t, db.First(&stored, job.ID).Error)
	require.Equal(t, model.ModerationJobCancelled, stored.Status)
	require.False(t, stored.Flagged)
	require.Empty(t, stored.Payload)
	require.Zero(t, stored.ChargedQuota)
}

func TestModerationWorkerCancellationKeepsRecoverableLeaseWithoutEffects(t *testing.T) {
	db, job := setupModerationPipelineJob(t, "text")
	previous := moderationHTTPClient
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var called bool
	moderationHTTPClient = &http.Client{Transport: moderationRoundTripper(func(request *http.Request) (*http.Response, error) {
		called = true
		cancel()
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	t.Cleanup(func() { moderationHTTPClient = previous })
	processModerationJob(ctx, "pipeline-test-worker", job)
	require.True(t, called)
	var stored model.ModerationJob
	require.NoError(t, db.First(&stored, job.ID).Error)
	require.Equal(t, model.ModerationJobRunning, stored.Status)
	require.NotEmpty(t, stored.Payload)
	require.False(t, stored.Flagged)
	require.Zero(t, stored.ChargedQuota)
	var notices int64
	require.NoError(t, db.Model(&model.ModerationNotice{}).Count(&notices).Error)
	require.Zero(t, notices)
}

func TestModerationOversizedInputIsCancelledWithoutProviderCall(t *testing.T) {
	db, job := setupModerationPipelineJob(t, strings.Repeat("x", model.ModerationMaxPayloadBytes+1))
	require.True(t, job.InputTruncated)
	previous := moderationHTTPClient
	var called bool
	moderationHTTPClient = &http.Client{Transport: moderationRoundTripper(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("must not send truncated input")
	})}
	t.Cleanup(func() { moderationHTTPClient = previous })
	processModerationJob(context.Background(), "pipeline-test-worker", job)
	require.False(t, called)
	var stored model.ModerationJob
	require.NoError(t, db.First(&stored, job.ID).Error)
	require.Equal(t, model.ModerationJobCancelled, stored.Status)
	require.Equal(t, "moderation_input_too_large", stored.ErrorMessage)
	require.Empty(t, stored.Payload)
	require.Zero(t, stored.ChargedQuota)
}
