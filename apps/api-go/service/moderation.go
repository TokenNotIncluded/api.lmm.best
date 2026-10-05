package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
)

const (
	ModerationSourceRelayInput      = "relay_input"
	ModerationSourceAssistantInput  = "assistant_input"
	ModerationSourceAssistantOutput = "assistant_output"
	moderationChunkMaxBytes         = 16 << 10
	moderationBatchMaxChunks        = 2
	moderationResponseMaxBytes      = 64 << 10
	moderationEnqueueTimeout        = 200 * time.Millisecond
	moderationRequestTimeout        = 25 * time.Second
	moderationLeaseSeconds          = 60
	moderationPollInterval          = time.Second
	moderationWorkerCount           = 2
	moderationEndpoint              = "https://api.openai.com/v1/moderations"
)

// ModerationSubmission contains only one user-authored turn or one assistant
// answer. System prompts, tool definitions and tool results must never be
// included. Group is the signed-in account's group, not the relay route group.
type ModerationSubmission struct {
	UserID    int
	Source    string
	RequestID string
	Group     string
	// RelayGroup is supplied only by authenticated routing context, never JSON.
	RelayGroup string
	Text       string
}

var moderationWakeup = make(chan struct{}, 1)

// QueueModeration only persists a bounded job. It never calls the provider and
// never turns a failed audit write into a failed user-facing request. Callers
// may record its error, without waiting for any moderation result.
func QueueModeration(ctx context.Context, submission ModerationSubmission) error {
	if !moderationSourceValid(submission.Source) || submission.UserID <= 0 || strings.TrimSpace(submission.RequestID) == "" {
		return errors.New("moderation_submission_invalid")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, moderationEnqueueTimeout)
	defer cancel()
	settings, err := model.ReadModerationSettingsContext(ctx)
	if err != nil {
		return errors.New("moderation_settings_unavailable")
	}
	if !moderationSourceEnabled(settings, submission.Source) {
		return nil
	}
	policyScope, policyGroup, policy, ok := setting.ResolveModerationRequestPolicy(settings, submission.Group, submission.RelayGroup, submission.Source != ModerationSourceRelayInput)
	if !ok || policy.Mode == setting.ModerationModeOff {
		return nil
	}
	text := strings.TrimSpace(submission.Text)
	if text == "" {
		return nil
	}
	truncated := len(text) > model.ModerationMaxPayloadBytes
	var digest [32]byte
	if truncated {
		// Oversized content is never persisted or sent. Hashing identifies the
		// unreviewed submission while a short capsule records the coverage gap.
		digest = sha256.Sum256([]byte(text))
		text = "[UNREVIEWED_OVERSIZED_TEXT]"
	} else {
		text = strings.TrimSpace(model.RedactModerationContent(text))
		digest = sha256.Sum256([]byte(text))
		truncated = len(text) > model.ModerationMaxPayloadBytes
		if truncated {
			text = "[UNREVIEWED_OVERSIZED_TEXT]"
		}
	}
	reviewGroup, reviewModel := settings.Group, settings.Model
	if submission.Source != ModerationSourceRelayInput {
		reviewGroup, reviewModel = settings.AssistantGroup, settings.AssistantModel
	}
	fines, err := json.Marshal(policy.CategoryFinesUSD)
	if err != nil {
		return errors.New("moderation_policy_invalid")
	}
	created, err := model.EnqueueModerationJob(ctx, &model.ModerationJob{
		UserID: submission.UserID, Source: submission.Source, RequestID: strings.TrimSpace(submission.RequestID),
		Group: strings.TrimSpace(submission.Group), ReviewGroup: reviewGroup, ReviewModel: reviewModel,
		PolicyScope: policyScope, PolicyGroup: policyGroup, RelayGroup: strings.TrimSpace(submission.RelayGroup),
		InputDigest: hex.EncodeToString(digest[:]), Payload: text, CapturedMode: policy.Mode,
		CapturedCategoryFinesJSON: string(fines),
		CapturedAmountCurrency:    setting.ResolveModerationAmountCurrency(policy.AmountCurrency),
		InputTruncated:            truncated,
	})
	if err != nil {
		return errors.New("moderation_enqueue_failed")
	}
	if created {
		select {
		case moderationWakeup <- struct{}{}:
		default:
		}
	}
	return nil
}

func moderationSourceValid(source string) bool {
	return source == ModerationSourceRelayInput || source == ModerationSourceAssistantInput || source == ModerationSourceAssistantOutput
}

func moderationSourceEnabled(settings setting.ModerationSettings, source string) bool {
	if source == ModerationSourceRelayInput {
		return settings.Enabled
	}
	return settings.AssistantEnabled && moderationSourceValid(source)
}

func moderationBoundedText(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

// RunModerationWorker is owned by the normal application lifecycle. Each node
// may run workers: claiming is fenced by the durable row lease, rather than
// relying on a node-local queue or master flag.
func RunModerationWorker(ctx context.Context) {
	var workers sync.WaitGroup
	for index := 0; index < moderationWorkerCount; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			runModerationWorker(ctx, common.NodeName+"-moderation-"+common.GetRandomString(12))
		}()
	}
	workers.Wait()
}

func runModerationWorker(ctx context.Context, owner string) {
	ticker := time.NewTicker(moderationPollInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		job, err := model.ClaimModerationJob(ctx, owner, time.Now().Unix(), moderationLeaseSeconds)
		if err != nil {
			common.SysError("moderation_job_claim_failed")
		}
		if err == nil && job != nil {
			processModerationJob(ctx, owner, job)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-moderationWakeup:
		}
	}
}

func processModerationJob(parent context.Context, owner string, job *model.ModerationJob) {
	ctx, cancel := context.WithTimeout(parent, moderationRequestTimeout)
	defer cancel()
	if job.InputTruncated || len(job.Payload) > model.ModerationMaxPayloadBytes {
		cancelModerationJob(parent, owner, job, "moderation_input_too_large")
		return
	}
	settings, err := model.ReadModerationSettingsContext(ctx)
	if err != nil {
		retryModerationJob(parent, owner, job, "moderation_settings_unavailable")
		return
	}
	policy, enabled := job.CurrentPolicy(settings)
	if !moderationSourceEnabled(settings, job.Source) || !enabled || policy.Mode == setting.ModerationModeOff {
		cancelModerationJob(parent, owner, job, "moderation_disabled")
		return
	}
	var user model.User
	if model.DB.WithContext(ctx).Select("id", "group", "status").First(&user, job.UserID).Error != nil {
		retryModerationJob(parent, owner, job, "moderation_account_unavailable")
		return
	}
	if user.Group != job.Group || user.Status != common.UserStatusEnabled {
		cancelModerationJob(parent, owner, job, "moderation_account_changed")
		return
	}
	if !setting.IsModerationModel(job.ReviewModel) || !moderationSourceValid(job.Source) {
		cancelModerationJob(parent, owner, job, "moderation_job_invalid")
		return
	}
	decision, err := callOfficialModeration(ctx, job.ReviewGroup, job.ReviewModel, job.Payload, func(ctx context.Context) error {
		return moderationDispatchAllowed(ctx, job)
	})
	if err != nil {
		if parent.Err() != nil {
			// Preserve a recoverable job on process shutdown; no notification,
			// risk change or penalty is based on an interrupted request.
			return
		}
		if err.Error() == "moderation_disabled" || err.Error() == "moderation_account_changed" {
			cancelModerationJob(parent, owner, job, err.Error())
			return
		}
		retryModerationJob(parent, owner, job, err.Error())
		return
	}
	if parent.Err() != nil {
		return
	}
	err = model.CompleteModerationJob(ctx, job.ID, owner, model.ModerationCompletion{
		Flagged: decision.Flagged, Categories: decision.Categories, Scores: decision.Scores,
		ResponseModel: decision.ResponseModel, CurrentMode: policy.Mode, CategoryFinesUSD: policy.CategoryFinesUSD,
		AmountCurrency: policy.AmountCurrency,
		Now:            time.Now().Unix(),
	})
	if err != nil {
		// A failed commit may have already reached the database. Reclaiming the
		// same durable row reconciles it; never create a new request identity.
		common.SysError("moderation_job_completion_failed")
	}
}

// Recheck before every batch, rather than treating a snapshot captured before
// a long provider call as permission to keep sending subsequent text batches.
func moderationDispatchAllowed(ctx context.Context, job *model.ModerationJob) error {
	settings, err := model.ReadModerationSettingsContext(ctx)
	if err != nil {
		return errors.New("moderation_settings_unavailable")
	}
	policy, configured := job.CurrentPolicy(settings)
	if !moderationSourceEnabled(settings, job.Source) || !configured || policy.Mode == setting.ModerationModeOff {
		return errors.New("moderation_disabled")
	}
	var user model.User
	if model.DB.WithContext(ctx).Select("id", "group", "status").First(&user, job.UserID).Error != nil {
		return errors.New("moderation_account_unavailable")
	}
	if user.Group != job.Group || user.Status != common.UserStatusEnabled {
		return errors.New("moderation_account_changed")
	}
	return nil
}

func cancelModerationJob(ctx context.Context, owner string, job *model.ModerationJob, code string) {
	if err := model.CancelModerationJob(ctx, job.ID, owner, code); err != nil {
		common.SysError("moderation_job_cancel_failed")
	}
}

func retryModerationJob(ctx context.Context, owner string, job *model.ModerationJob, code string) {
	delay := int64(15)
	if job.Attempts > 1 {
		delay = 60
	}
	now := time.Now().Unix()
	if err := model.RetryModerationJob(ctx, job.ID, owner, now, now+delay, code); err != nil {
		common.SysError("moderation_job_retry_failed")
	}
}

type moderationDecision struct {
	Flagged       bool
	Categories    []string
	Scores        map[string]float64
	ResponseModel string
}

var moderationHTTPClient = newModerationHTTPClient()

func newModerationHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Only direct HTTPS to the fixed official origin is supported. Channel
	// proxies and header/parameter overrides never participate in this call.
	transport.Proxy = nil
	return &http.Client{
		Transport: transport, Timeout: moderationRequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func callOfficialModeration(ctx context.Context, group, reviewModel, text string, preflight func(context.Context) error) (moderationDecision, error) {
	channel, key, err := officialModerationCredential(ctx, group, reviewModel)
	if err != nil {
		return moderationDecision{}, err
	}
	organization := ""
	if channel.OpenAIOrganization != nil {
		organization = strings.TrimSpace(*channel.OpenAIOrganization)
	}
	chunks := moderationTextChunks(text)
	if len(chunks) == 0 || len(text) > model.ModerationMaxPayloadBytes {
		return moderationDecision{}, errors.New("moderation_request_invalid")
	}
	combined := moderationDecision{Scores: map[string]float64{}}
	categories := map[string]bool{}
	for start := 0; start < len(chunks); start += moderationBatchMaxChunks {
		if preflight != nil {
			if err := preflight(ctx); err != nil {
				return moderationDecision{}, err
			}
		}
		end := start + moderationBatchMaxChunks
		if end > len(chunks) {
			end = len(chunks)
		}
		decision, err := requestOfficialModeration(ctx, moderationHTTPClient, reviewModel, chunks[start:end], key, organization)
		if err != nil {
			return moderationDecision{}, err
		}
		if combined.ResponseModel != "" && combined.ResponseModel != decision.ResponseModel {
			return moderationDecision{}, errors.New("moderation_response_invalid")
		}
		combined.ResponseModel = decision.ResponseModel
		combined.Flagged = combined.Flagged || decision.Flagged
		for _, category := range decision.Categories {
			categories[category] = true
		}
		for category, score := range decision.Scores {
			if existing, present := combined.Scores[category]; !present || score > existing {
				combined.Scores[category] = score
			}
		}
	}
	for category := range categories {
		combined.Categories = append(combined.Categories, category)
	}
	sort.Strings(combined.Categories)
	return combined, nil
}

// moderationTextChunks covers the whole bounded text. A small overlap avoids
// cutting a short phrase in half at an arbitrary UTF-8 boundary.
func moderationTextChunks(text string) []string {
	var chunks []string
	for len(text) > 0 {
		chunk := moderationBoundedText(text, moderationChunkMaxBytes)
		chunks = append(chunks, chunk)
		if len(chunk) == len(text) {
			break
		}
		next := len(chunk) - 512
		for next > 0 && !utf8.RuneStart(text[next]) {
			next--
		}
		text = text[next:]
	}
	return chunks
}

func requestOfficialModeration(ctx context.Context, client *http.Client, reviewModel string, chunks []string, key, organization string) (moderationDecision, error) {
	if !setting.IsModerationModel(reviewModel) || len(chunks) == 0 || len(chunks) > moderationBatchMaxChunks {
		return moderationDecision{}, errors.New("moderation_request_invalid")
	}
	for _, chunk := range chunks {
		if chunk == "" || len(chunk) > moderationChunkMaxBytes || !utf8.ValidString(chunk) {
			return moderationDecision{}, errors.New("moderation_request_invalid")
		}
	}
	body, err := json.Marshal(struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}{Model: reviewModel, Input: chunks})
	if err != nil {
		return moderationDecision{}, errors.New("moderation_request_invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, moderationEndpoint, bytes.NewReader(body))
	if err != nil {
		return moderationDecision{}, errors.New("moderation_request_invalid")
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	if organization != "" {
		request.Header.Set("OpenAI-Organization", organization)
	}
	response, err := client.Do(request)
	if err != nil {
		// Never persist the raw error, request headers or provider body.
		return moderationDecision{}, errors.New("moderation_provider_unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return moderationDecision{}, errors.New("moderation_provider_rejected")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, moderationResponseMaxBytes+1))
	if err != nil || len(data) > moderationResponseMaxBytes {
		return moderationDecision{}, errors.New("moderation_response_invalid")
	}
	return parseModerationBatchResponse(data, len(chunks))
}

func officialModerationCredential(ctx context.Context, group, reviewModel string) (*model.Channel, string, error) {
	channels, err := model.ListOfficialModerationChannels(ctx, group, reviewModel)
	if err != nil || len(channels) == 0 {
		return nil, "", errors.New("moderation_route_unavailable")
	}
	for _, metadata := range channels {
		channel, err := model.GetChannelByIdContext(ctx, metadata.Id, true)
		if err != nil || !model.IsOfficialModerationChannel(channel) {
			continue
		}
		key := moderationEnabledKey(channel)
		if key != "" {
			return channel, key, nil
		}
	}
	return nil, "", errors.New("moderation_route_unavailable")
}

// Background moderation balances enabled keys without persisting a polling
// cursor. The normal relay helper may write that cursor without a context;
// avoiding that write keeps this worker's deadline and shutdown bounded.
func moderationEnabledKey(channel *model.Channel) string {
	if !channel.ChannelInfo.IsMultiKey {
		return strings.TrimSpace(channel.Key)
	}
	keys := channel.GetKeys()
	if len(keys) == 0 {
		return ""
	}
	start := rand.IntN(len(keys))
	for offset := 0; offset < len(keys); offset++ {
		index := (start + offset) % len(keys)
		if status, configured := channel.ChannelInfo.MultiKeyStatusList[index]; configured && status != common.ChannelStatusEnabled {
			continue
		}
		if key := strings.TrimSpace(keys[index]); key != "" {
			return key
		}
	}
	return ""
}

func parseModerationResponse(body []byte) (moderationDecision, error) {
	return parseModerationBatchResponse(body, 1)
}

func parseModerationBatchResponse(body []byte, expectedResults int) (moderationDecision, error) {
	var response struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Results []struct {
			Flagged    *bool               `json:"flagged"`
			Categories map[string]*bool    `json:"categories"`
			Scores     map[string]*float64 `json:"category_scores"`
		} `json:"results"`
	}
	invalid := func() (moderationDecision, error) {
		return moderationDecision{}, errors.New("moderation_response_invalid")
	}
	if json.Unmarshal(body, &response) != nil || strings.TrimSpace(response.ID) == "" || !setting.IsModerationModel(response.Model) || len(response.Results) != expectedResults || expectedResults < 1 {
		return invalid()
	}
	decision := moderationDecision{ResponseModel: response.Model, Scores: make(map[string]float64)}
	categories := map[string]bool{}
	for _, result := range response.Results {
		if result.Flagged == nil || len(result.Categories) == 0 || len(result.Scores) != len(result.Categories) {
			return invalid()
		}
		flaggedCategories := 0
		for category, flagged := range result.Categories {
			if flagged == nil || !setting.IsModerationCategory(category) {
				return invalid()
			}
			score, ok := result.Scores[category]
			if !ok || score == nil || math.IsNaN(*score) || math.IsInf(*score, 0) || *score < 0 || *score > 1 {
				return invalid()
			}
			if existing, present := decision.Scores[category]; !present || *score > existing {
				decision.Scores[category] = *score
			}
			if *flagged {
				categories[category] = true
				flaggedCategories++
			}
		}
		if *result.Flagged != (flaggedCategories > 0) {
			return invalid()
		}
		decision.Flagged = decision.Flagged || *result.Flagged
	}
	for category := range categories {
		decision.Categories = append(decision.Categories, category)
	}
	sort.Strings(decision.Categories)
	return decision, nil
}
