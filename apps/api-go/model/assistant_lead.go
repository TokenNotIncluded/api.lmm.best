package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	AssistantLeadSourceChat    = "chat"
	AssistantLeadSourceHandoff = "handoff"

	AssistantLeadStatusObserved = "observed"
	AssistantLeadStatusPending  = "pending"
	AssistantLeadStatusResolved = "resolved"

	AssistantIntentOnboarding     = "onboarding"
	AssistantIntentPlanPurchase   = "plan_purchase"
	AssistantIntentAPIKey         = "api_key"
	AssistantIntentClientSetup    = "client_setup"
	AssistantIntentCost           = "cost"
	AssistantIntentMath           = "math"
	AssistantIntentRecommendation = "recommendation"
	AssistantIntentBounty         = "bounty"
	AssistantIntentUsage          = "usage"
	AssistantIntentModels         = "models"
	AssistantIntentInvitation     = "invitation"
	AssistantIntentHumanSupport   = "human_support"
	AssistantIntentOther          = "other"

	minAssistantHandoffRunes             = 5
	maxAssistantHandoffRunes             = 2000
	maxAssistantAdminNoteRunes           = 2000
	assistantFirstQuestionMaxRunes       = 4000
	assistantFirstQuestionBucketSeconds  = 60 * 60
	assistantFirstQuestionTopN           = 10
	assistantFirstQuestionThemeScanLimit = 512
	assistantSummaryMaxRows              = 64
)

var (
	ErrAssistantHandoffMessageRequired = errors.New("support message is required")
	ErrAssistantHandoffMessageTooShort = errors.New("support message must contain at least 5 characters")
	ErrAssistantHandoffMessageTooLong  = errors.New("support message must be at most 2000 characters")
	ErrAssistantLeadNotFound           = errors.New("assistant support request not found")
	ErrAssistantLeadAlreadyResolved    = errors.New("assistant support request is already resolved")
	ErrAssistantLeadStatus             = errors.New("assistant support request status is invalid")
	ErrAssistantAdminNoteTooLong       = errors.New("assistant support note must be at most 2000 characters")
	ErrAssistantFirstQuestionRequired  = errors.New("assistant first question is required")
	ErrAssistantFirstQuestionTooLong   = errors.New("assistant first question must be at most 4000 characters")

	assistantFirstQuestionTokenPattern  = regexp.MustCompile(`(?i)\b(sk|rk|pk|ak|tok|token|key|secret)[_-][a-z0-9._~+/-]{6,}\b`)
	assistantFirstQuestionFieldPattern  = regexp.MustCompile(`(?i)\b(token|client[_ -]?secret|secret|credential|private[_ -]?key|access[_ -]?key)\s*[:=：]\s*[^\s,;]+`)
	assistantFirstQuestionUserIDPattern = regexp.MustCompile(`(?i)\b(user[_ -]?id|userid)\s*[:=：]\s*[a-z0-9_-]+`)
)

// AssistantLead stores privacy-minimized intent signals and explicit support
// handoffs. Chat rows contain only the classified intent; raw chat text is not
// persisted. A handoff stores the user's explicitly submitted, redacted note.
type AssistantLead struct {
	Id          int    `json:"id" gorm:"primaryKey"`
	UserId      int    `json:"user_id" gorm:"not null;index"`
	Source      string `json:"source" gorm:"type:varchar(20);not null;index"`
	Intent      string `json:"intent" gorm:"type:varchar(40);not null;index"`
	Message     string `json:"message" gorm:"type:text"`
	Status      string `json:"status" gorm:"type:varchar(20);not null;index"`
	AdminUserId int    `json:"admin_user_id" gorm:"index"`
	AdminNote   string `json:"admin_note" gorm:"type:text"`
	CreatedAt   int64  `json:"created_at" gorm:"not null;index"`
	ResolvedAt  int64  `json:"resolved_at" gorm:"not null;default:0"`
}

func (AssistantLead) TableName() string { return "assistant_leads" }

type AssistantLeadView struct {
	AssistantLead
	Username string `json:"username"`
	Email    string `json:"email"`
}

type AssistantIntentSummary struct {
	Intent string `json:"intent"`
	Count  int64  `json:"count"`
}

// AssistantProfileBucket is intentionally aggregate-only. It has no user ID,
// email, raw message, or account metadata. Hourly buckets keep storage bounded
// while still allowing administrators to compare onboarding strategies over
// time.
type AssistantProfileBucket struct {
	Id          int    `json:"id" gorm:"primaryKey"`
	Profile     string `json:"profile" gorm:"type:varchar(64);not null;uniqueIndex:idx_assistant_profile_bucket,priority:1"`
	BucketStart int64  `json:"bucket_start" gorm:"not null;uniqueIndex:idx_assistant_profile_bucket,priority:2"`
	Count       int64  `json:"count" gorm:"not null;default:0"`
}

func (AssistantProfileBucket) TableName() string { return "assistant_profile_buckets" }

type AssistantProfileSummary struct {
	Profile string `json:"profile"`
	Count   int64  `json:"count"`
}

// AssistantFirstQuestionStat stores one redacted, normalized question per
// hour. It is aggregate-only: no user identity, email, credential, or raw
// request metadata is kept. Hourly buckets make the existing admin time-window
// filter return accurate counts without retaining individual events.
type AssistantFirstQuestionStat struct {
	Id           int    `json:"-" gorm:"primaryKey"`
	QuestionHash string `json:"-" gorm:"type:char(64);not null;uniqueIndex:idx_assistant_first_question_stat,priority:1"`
	Question     string `json:"-" gorm:"type:text;not null"`
	BucketStart  int64  `json:"-" gorm:"not null;uniqueIndex:idx_assistant_first_question_stat,priority:2"`
	Count        int64  `json:"-" gorm:"not null;default:0"`
	LastAskedAt  int64  `json:"-" gorm:"not null;index"`
}

func (AssistantFirstQuestionStat) TableName() string { return "assistant_first_question_stats" }

type AssistantFirstQuestionSummary struct {
	Question    string `json:"question"`
	Count       int64  `json:"count"`
	LastAskedAt int64  `json:"last_asked_at"`
}

var assistantProfileNames = map[string]struct{}{
	"unknown":                  {},
	"technical_cost_sensitive": {},
	"guided_buyer":             {},
	"promotion_seeker":         {},
	"security_risk":            {},
	"production_operator":      {},
	"privacy_conscious":        {},
	"mobile_accessibility":     {},
	"normal_user":              {},
	"support_seeking":          {},
	"l0_applicant":             {},
}

func assistantMessageContains(message string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(message, term) {
			return true
		}
	}
	return false
}

func isAssistantLowSignalMessage(message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	normalized = strings.Trim(normalized, " \\t\\r\\n,.!?;:，。！？；：~～")
	switch normalized {
	case "hi", "hello", "hey", "ok", "okay", "thanks", "thank you", "got it", "continue",
		"你好", "您好", "哈喽", "嗨", "好的", "好", "行", "可以", "谢谢", "感谢", "收到", "明白", "明白了", "继续", "嗯", "哦":
		return true
	default:
		return false
	}
}

// ClassifyAssistantIntent intentionally uses a deterministic, auditable
// keyword classifier. It is sufficient for product analytics and cannot turn
// model output into an account action.
func ClassifyAssistantIntent(message string) string {
	normalized := strings.ToLower(strings.TrimSpace(message))
	explicitAccessRequest := assistantMessageContains(normalized,
		"l0", "l1", "开发者权限", "开发者访问", "api 权限", "api 访问", "developer access", "api access") &&
		assistantMessageContains(normalized,
			"申请", "开通", "解锁", "升级", "审核", "需要", "想要", "apply", "request", "unlock", "upgrade", "review", "need", "want")
	switch {
	case assistantMessageContains(normalized,
		"推荐信", "推荐函", "推荐内容", "recommendation letter", "l1 recommendation", "access recommendation"):
		return AssistantIntentRecommendation
	case explicitAccessRequest:
		return AssistantIntentOnboarding
	case assistantMessageContains(normalized,
		"人工", "客服", "管理员", "工单", "报错", "出错了", "human", "support", "administrator", "agent"):
		return AssistantIntentHumanSupport
	case assistantMessageContains(normalized,
		"用了多少 token", "使用了多少 token", "消耗了多少 token", "本月 token", "这个月 token", "token 用量", "token 使用量",
		"tokens used", "tokens have i used", "how many tokens", "token usage", "token consumption", "monthly tokens", "usage logs", "request history"):
		return AssistantIntentUsage
	case assistantMessageContains(normalized,
		"成本", "费用", "计费", "消耗", "价格", "单价", "多少钱", "多少费用", "怎么收费", "收费标准", "报价",
		"cost", "estimate", "billing", "price", "pricing", "token rate"):
		return AssistantIntentCost
	case assistantMessageContains(normalized,
		"计算", "算一下", "数学", "换算", "百分比", "calculate", "calculator", "math", "percentage", "convert units"):
		return AssistantIntentMath
	case assistantMessageContains(normalized,
		"历史调用", "调用数据", "调用统计", "用量统计", "使用统计", "调用记录", "余额", "额度", "配额", "还剩多少",
		"usage", "statistics", "remaining quota", "quota remaining"):
		return AssistantIntentUsage
	case assistantMessageContains(normalized,
		"有哪些模型", "模型列表", "可用模型", "模型清单", "支持哪些模型", "能用哪些模型",
		"available models", "model list", "model ids", "which models"):
		return AssistantIntentModels
	case assistantMessageContains(normalized,
		"邀请奖励", "邀请码", "邀请链接", "邀请用户", "affiliate", "referral", "invite reward",
		"新用户礼包", "新用户福利", "新手礼包", "新手奖励", "新用户奖励", "新人礼包", "新人福利", "新手福利",
		"welcome gift", "welcome bonus", "new-user gift", "new user gift", "new user bonus"):
		return AssistantIntentInvitation
	case assistantMessageContains(normalized,
		"claude code", "cc switch", "cc-switch", "chatgpt", "hermes", "windows", "linux", "macos", "mac os",
		"桌面版", "安装", "配置客户端", "客户端", "接入文档",
		"client setup", "sdk setup", "desktop client", "connect a client", "connect client", "configure a client", "configure client"):
		return AssistantIntentClientSetup
	case assistantMessageContains(normalized,
		"api key", "api-key", "api_key", "apikey", "base url", "base_url", "model id", "模型 id", "模型id", "密钥", "令牌", "access token", "创建 key", "创建key", "create key", "create a key", "create my key"):
		return AssistantIntentAPIKey
	case assistantMessageContains(normalized,
		"开源", "悬赏", "挑战", "小费", "bounty", "tip", "challenge", "任务发布"):
		return AssistantIntentBounty
	case assistantMessageContains(normalized,
		"套餐", "购买", "划算", "优惠", "折扣", "订阅", "充值", "plan", "purchase", "discount", "best value", "top up", "top-up", "topup"):
		return AssistantIntentPlanPurchase
	case assistantMessageContains(normalized,
		"新手", "入门", "怎么用", "如何使用", "怎么开始", "onboarding", "approval", "getting started", "how to use"):
		return AssistantIntentOnboarding
	default:
		return AssistantIntentOther
	}
}

func redactAssistantHandoffMessage(message string) string {
	return RedactAssistantHistoryContent(message)
}

func normalizeAssistantHandoffMessage(message string) (string, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return "", ErrAssistantHandoffMessageRequired
	}
	if utf8.RuneCountInString(message) < minAssistantHandoffRunes {
		return "", ErrAssistantHandoffMessageTooShort
	}
	if utf8.RuneCountInString(message) > maxAssistantHandoffRunes {
		return "", ErrAssistantHandoffMessageTooLong
	}
	return redactAssistantHandoffMessage(message), nil
}

func normalizeAssistantAdminNote(note string) (string, error) {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > maxAssistantAdminNoteRunes {
		return "", ErrAssistantAdminNoteTooLong
	}
	return note, nil
}

// ShouldRecordAssistantIntent drops greetings and acknowledgements, then keeps
// substantive first turns plus later turns with a recognized product intent.
// Unclassified follow-ups previously flooded automatic review with Other.
func ShouldRecordAssistantIntent(message string, firstUserTurn bool) bool {
	if strings.TrimSpace(message) == "" || isAssistantLowSignalMessage(message) {
		return false
	}
	return firstUserTurn || ClassifyAssistantIntent(message) != AssistantIntentOther
}

// RecordAssistantIntent persists only a category, never the raw chat message.
func RecordAssistantIntent(userID int, message string) error {
	if userID <= 0 {
		return gorm.ErrInvalidData
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := lockAssistantOwner(tx, userID); err != nil {
			return err
		}
		return tx.Create(&AssistantLead{
			UserId:    userID,
			Source:    AssistantLeadSourceChat,
			Intent:    ClassifyAssistantIntent(message),
			Status:    AssistantLeadStatusObserved,
			CreatedAt: common.GetTimestamp(),
		}).Error
	})
}

func normalizeAssistantFirstQuestion(question string) (string, string, error) {
	question = RedactAssistantHistoryContent(question)
	question = redactAssistantHandoffMessage(question)
	question = assistantFirstQuestionTokenPattern.ReplaceAllString(question, "[REDACTED_SECRET]")
	question = assistantFirstQuestionFieldPattern.ReplaceAllString(question, "$1: [REDACTED]")
	question = assistantFirstQuestionUserIDPattern.ReplaceAllString(question, "[REDACTED_ID]")
	question = strings.ToLower(strings.Join(strings.Fields(question), " "))
	if question == "" {
		return "", "", ErrAssistantFirstQuestionRequired
	}
	if utf8.RuneCountInString(question) > assistantFirstQuestionMaxRunes {
		return "", "", ErrAssistantFirstQuestionTooLong
	}
	hash := sha256.Sum256([]byte(question))
	return question, hex.EncodeToString(hash[:]), nil
}

// RecordAssistantFirstQuestion records one valid first-turn question. The
// caller deliberately provides no user ID: the persisted row is a redacted,
// normalized aggregate that is safe for admin product analytics.
func RecordAssistantFirstQuestion(question string) error {
	normalized, questionHash, err := normalizeAssistantFirstQuestion(question)
	if err != nil {
		return err
	}

	now := common.GetTimestamp()
	bucketStart := now - now%assistantFirstQuestionBucketSeconds
	countExpression := gorm.Expr("count + ?", 1)
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		countExpression = gorm.Expr(`"assistant_first_question_stats"."count" + ?`, 1)
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "question_hash"},
			{Name: "bucket_start"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"count":         countExpression,
			"last_asked_at": now,
		}),
	}).Create(&AssistantFirstQuestionStat{
		QuestionHash: questionHash,
		Question:     normalized,
		BucketStart:  bucketStart,
		Count:        1,
		LastAskedAt:  now,
	}).Error
}

func SubmitAssistantHandoff(userID int, message string) (*AssistantLead, error) {
	if userID <= 0 {
		return nil, gorm.ErrInvalidData
	}
	normalized, err := normalizeAssistantHandoffMessage(message)
	if err != nil {
		return nil, err
	}

	var lead AssistantLead
	err = DB.Transaction(func(tx *gorm.DB) error {
		return submitAssistantHandoffWithTx(tx, userID, normalized, &lead)
	})
	if err != nil {
		return nil, err
	}
	return &lead, nil
}

// SubmitAssistantHandoffWithAuthFlow atomically consumes a confirmation flow
// and creates the handoff from its server-side draft. The flow match binds
// the operation to the current browser session and user; the draft is never
// taken from the confirmation request body, so a client cannot replace the
// assistant-prepared message while confirming it.
func SubmitAssistantHandoffWithAuthFlow(token string, match AuthFlowMatch) (*AssistantLead, error) {
	if strings.TrimSpace(token) == "" || match.Purpose != AuthFlowPurposeAssistantHandoff || match.UserId <= 0 || strings.TrimSpace(match.SessionId) == "" {
		return nil, ErrAuthFlowInvalid
	}

	var lead AssistantLead
	_, err := ConsumeAuthFlowWithAction(token, match, func(tx *gorm.DB, flow *AuthFlow) error {
		var draft struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(flow.Payload), &draft); err != nil {
			return ErrAuthFlowInvalid
		}
		normalized, err := normalizeAssistantHandoffMessage(draft.Message)
		if err != nil {
			return ErrAuthFlowInvalid
		}
		return submitAssistantHandoffWithTx(tx, match.UserId, normalized, &lead)
	})
	if err != nil {
		return nil, err
	}
	return &lead, nil
}

func submitAssistantHandoffWithTx(tx *gorm.DB, userID int, normalized string, lead *AssistantLead) error {
	if tx == nil || userID <= 0 || lead == nil {
		return gorm.ErrInvalidData
	}
	if err := lockAssistantOwner(tx, userID); err != nil {
		return err
	}
	findErr := tx.Where("user_id = ? AND source = ? AND status = ?", userID, AssistantLeadSourceHandoff, AssistantLeadStatusPending).
		Order("id DESC").First(lead).Error
	if findErr == nil {
		return nil
	}
	if !errors.Is(findErr, gorm.ErrRecordNotFound) {
		return findErr
	}
	*lead = AssistantLead{
		UserId:    userID,
		Source:    AssistantLeadSourceHandoff,
		Intent:    AssistantIntentHumanSupport,
		Message:   normalized,
		Status:    AssistantLeadStatusPending,
		CreatedAt: common.GetTimestamp(),
	}
	return tx.Create(lead).Error
}

func GetLatestAssistantHandoff(userID int) (*AssistantLead, error) {
	if userID <= 0 {
		return nil, gorm.ErrInvalidData
	}
	var lead AssistantLead
	err := DB.Where("user_id = ? AND source = ?", userID, AssistantLeadSourceHandoff).
		Order("id DESC").First(&lead).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &lead, nil
}

func ListAssistantHandoffs(status string, limit int) ([]AssistantLeadView, error) {
	status = strings.TrimSpace(status)
	if status == "" {
		status = AssistantLeadStatusPending
	}
	if status != AssistantLeadStatusPending && status != AssistantLeadStatusResolved {
		return nil, ErrAssistantLeadStatus
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	var leads []AssistantLeadView
	err := DB.Table("assistant_leads AS lead").
		Select("lead.*, users.username, users.email").
		Joins("JOIN users ON users.id = lead.user_id").
		Where("lead.source = ? AND lead.status = ?", AssistantLeadSourceHandoff, status).
		Order("lead.id DESC").Limit(limit).Find(&leads).Error
	return leads, err
}

func ListAssistantIntentSummary(since int64) ([]AssistantIntentSummary, error) {
	return listAssistantIntents(context.Background(), since, 0)
}

func listAssistantIntents(ctx context.Context, since, until int64) ([]AssistantIntentSummary, error) {
	query := DB.WithContext(ctx).Model(&AssistantLead{}).
		Select("intent, COUNT(*) AS count").
		Group("intent").Order("count DESC, intent ASC").
		Limit(assistantSummaryMaxRows)
	if since > 0 {
		query = query.Where("created_at >= ?", since)
	}
	if until > 0 {
		query = query.Where("created_at <= ?", until)
	}
	var summary []AssistantIntentSummary
	if err := query.Scan(&summary).Error; err != nil {
		return nil, err
	}
	return summary, nil
}

func ListAssistantFirstQuestionSummary(since int64) ([]AssistantFirstQuestionSummary, error) {
	return listAssistantFirstQuestions(context.Background(), since, 0)
}

func listAssistantFirstQuestions(ctx context.Context, since, until int64) ([]AssistantFirstQuestionSummary, error) {
	return listAssistantFirstQuestionsWithLimit(ctx, since, until, assistantFirstQuestionTopN)
}

func listAssistantFirstQuestionsWithLimit(ctx context.Context, since, until int64, limit int) ([]AssistantFirstQuestionSummary, error) {
	query := DB.WithContext(ctx).Model(&AssistantFirstQuestionStat{}).
		Select("question, SUM(count) AS count, MAX(last_asked_at) AS last_asked_at").
		Group("question_hash, question").
		Order("count DESC, last_asked_at DESC, question ASC").
		Limit(limit)
	if since > 0 {
		query = query.Where("bucket_start >= ?", since)
	}
	if until > 0 {
		query = query.Where("bucket_start <= ?", until)
	}
	var summary []AssistantFirstQuestionSummary
	if err := query.Scan(&summary).Error; err != nil {
		return nil, err
	}
	return summary, nil
}

// listAssistantFirstQuestionThemes reclassifies redacted first-turn aggregates
// with the current classifier. This makes automatic review useful immediately
// after classifier improvements without rewriting historical event rows.
func listAssistantFirstQuestionThemes(ctx context.Context, since, until int64) ([]AssistantIntentSummary, error) {
	questions, err := listAssistantFirstQuestionsWithLimit(ctx, since, until, assistantFirstQuestionThemeScanLimit)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int64)
	for _, question := range questions {
		if question.Count <= 0 || isAssistantLowSignalMessage(question.Question) {
			continue
		}
		counts[ClassifyAssistantIntent(question.Question)] += question.Count
	}
	themes := make([]AssistantIntentSummary, 0, len(counts))
	for intent, count := range counts {
		themes = append(themes, AssistantIntentSummary{Intent: intent, Count: count})
	}
	sort.Slice(themes, func(i, j int) bool {
		if themes[i].Count != themes[j].Count {
			return themes[i].Count > themes[j].Count
		}
		return themes[i].Intent < themes[j].Intent
	})
	return themes, nil
}

func RecordAssistantProfile(profile string) error {
	profile = strings.TrimSpace(profile)
	if _, ok := assistantProfileNames[profile]; !ok {
		return errors.New("assistant profile is invalid")
	}
	now := common.GetTimestamp()
	const bucketSeconds int64 = 60 * 60
	bucketStart := now - now%bucketSeconds
	countExpression := gorm.Expr("count + ?", 1)
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		// PostgreSQL resolves an unqualified column in an ON CONFLICT update
		// against both the target row and the proposed row. Qualify the target
		// table so profile counters are updated instead of being dropped with
		// SQLSTATE 42702 (ambiguous column reference).
		countExpression = gorm.Expr(`"assistant_profile_buckets"."count" + ?`, 1)
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "profile"},
			{Name: "bucket_start"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"count": countExpression,
		}),
	}).Create(&AssistantProfileBucket{
		Profile:     profile,
		BucketStart: bucketStart,
		Count:       1,
	}).Error
}

func ListAssistantProfileSummary(since int64) ([]AssistantProfileSummary, error) {
	return listAssistantProfiles(context.Background(), since, 0)
}

func listAssistantProfiles(ctx context.Context, since, until int64) ([]AssistantProfileSummary, error) {
	query := DB.WithContext(ctx).Model(&AssistantProfileBucket{}).
		Select("profile, SUM(count) AS count").
		Group("profile").Order("count DESC, profile ASC").
		Limit(assistantSummaryMaxRows)
	if since > 0 {
		query = query.Where("bucket_start >= ?", since)
	}
	if until > 0 {
		query = query.Where("bucket_start <= ?", until)
	}
	var summary []AssistantProfileSummary
	if err := query.Scan(&summary).Error; err != nil {
		return nil, err
	}
	return summary, nil
}

func ResolveAssistantHandoff(adminUserID int, leadID int, note string) (*AssistantLead, error) {
	if adminUserID <= 0 || leadID <= 0 {
		return nil, gorm.ErrInvalidData
	}
	normalizedNote, err := normalizeAssistantAdminNote(note)
	if err != nil {
		return nil, err
	}
	var lead AssistantLead
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("id = ? AND source = ?", leadID, AssistantLeadSourceHandoff).First(&lead).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAssistantLeadNotFound
			}
			return err
		}
		if lead.Status != AssistantLeadStatusPending {
			return ErrAssistantLeadAlreadyResolved
		}
		now := common.GetTimestamp()
		if err := tx.Model(&lead).Updates(map[string]any{
			"status":        AssistantLeadStatusResolved,
			"admin_user_id": adminUserID,
			"admin_note":    normalizedNote,
			"resolved_at":   now,
		}).Error; err != nil {
			return err
		}
		lead.Status = AssistantLeadStatusResolved
		lead.AdminUserId = adminUserID
		lead.AdminNote = normalizedNote
		lead.ResolvedAt = now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &lead, nil
}
