package model

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const UserNameMaxLength = 20

type userSortColumn struct {
	name       string
	expression string
}

var userSortColumns = map[string]userSortColumn{
	"id":            {name: "id"},
	"username":      {name: "username"},
	"quota":         {name: "quota"},
	"group":         {name: "group"},
	"created_at":    {name: "created_at"},
	"last_login_at": {name: "last_login_at"},
	"topup_quota": {
		name:       "topup_quota",
		expression: "COALESCE(user_topup_totals.credited_quota, 0)",
	},
	"topup_money": {
		name:       "topup_money",
		expression: "COALESCE(user_topup_totals.money_micros, 0)",
	},
	"assistant_violations": {
		name:       "assistant_violations",
		expression: "COALESCE(assistant_review_violation_totals.violation_count, 0)",
	},
}

type UserSortOptions struct {
	SortBy    string
	SortOrder string
}

func NewUserSortOptions(sortBy string, sortOrder string) UserSortOptions {
	normalizedSortBy := strings.ToLower(strings.TrimSpace(sortBy))
	normalizedSortOrder := strings.ToLower(strings.TrimSpace(sortOrder))
	if _, ok := userSortColumns[normalizedSortBy]; !ok {
		normalizedSortBy = "id"
		normalizedSortOrder = "desc"
	} else if normalizedSortOrder != "asc" {
		normalizedSortOrder = "desc"
	}

	return UserSortOptions{
		SortBy:    normalizedSortBy,
		SortOrder: normalizedSortOrder,
	}
}

func (options UserSortOptions) Apply(query *gorm.DB) *gorm.DB {
	column, ok := userSortColumns[options.SortBy]
	if !ok {
		column = userSortColumns["id"]
	}
	var q *gorm.DB
	if column.expression != "" {
		direction := "ASC"
		if options.SortOrder != "asc" {
			direction = "DESC"
		}
		q = query.Order(clause.Expr{SQL: column.expression + " " + direction})
	} else {
		q = query.Order(clause.OrderByColumn{
			Column: clause.Column{Name: column.name},
			Desc:   options.SortOrder != "asc",
		})
	}
	if column.name != "id" {
		q = q.Order(clause.OrderByColumn{
			Column: clause.Column{Name: "id"},
			Desc:   true,
		})
	}
	return q
}

// UserTopupMethod is the successful credit a user received through one
// payment method/provider pair. The values are populated only for
// administrator-facing user lists.
type UserTopupMethod struct {
	Method             string `json:"method"`
	Provider           string `json:"provider,omitempty"`
	SettlementCurrency string `json:"settlement_currency"`
	Quota              int64  `json:"quota"`
	MoneyMicros        int64  `json:"money_micros"`
	Orders             int64  `json:"orders"`
}

type UserTopupSummary struct {
	Quota       int64             `json:"quota"`
	MoneyMicros int64             `json:"money_micros"`
	Currency    string            `json:"currency,omitempty"`
	Orders      int64             `json:"orders"`
	Methods     []UserTopupMethod `json:"methods"`
}

func resolveUserSortOptions(sortOptions []UserSortOptions) UserSortOptions {
	if len(sortOptions) == 0 {
		return NewUserSortOptions("", "")
	}
	return sortOptions[0]
}

type userTopupAggregate struct {
	UserID             int    `gorm:"column:user_id"`
	PaymentMethod      string `gorm:"column:payment_method"`
	PaymentProvider    string `gorm:"column:payment_provider"`
	SettlementCurrency string `gorm:"column:settlement_currency"`
	CreditedQuota      int64  `gorm:"column:credited_quota"`
	MoneyMicros        int64  `gorm:"column:money_micros"`
	Orders             int64  `gorm:"column:orders"`
}

// userTopupMoneyMicrosSQL prefers the immutable settlement amount recorded by
// a provider. Historical successful rows predate that column, so retain their
// stored money amount as a per-row fallback instead of allowing one settled
// payment to mask older rows in the same payment-method aggregate.
func userTopupMoneyMicrosSQL(db *gorm.DB) string {
	legacyMoneyMicros := "CAST(ROUND(money * 1000000) AS BIGINT)"
	if db != nil && db.Dialector != nil && db.Dialector.Name() == "mysql" {
		legacyMoneyMicros = "CAST(ROUND(money * 1000000) AS SIGNED)"
	}
	return "CASE WHEN settled_amount_micros > 0 THEN settled_amount_micros WHEN money > 0 THEN " + legacyMoneyMicros + " ELSE 0 END"
}

func userTopupTotals(tx *gorm.DB) *gorm.DB {
	creditedQuotaExpression, creditedQuotaArgs := positiveNormalizedCreditedQuotaSQL()
	moneyMicrosExpression := userTopupMoneyMicrosSQL(tx)
	settlementCurrencyExpression := "COALESCE(NULLIF(UPPER(TRIM(settlement_currency)), ''), 'UNKNOWN')"
	moneyTotalExpression := "CASE WHEN COUNT(DISTINCT " + settlementCurrencyExpression + ") = 1 THEN COALESCE(SUM(" + moneyMicrosExpression + "), 0) ELSE 0 END"
	return successfulExternalPaidTopUpQuery(tx.Model(&TopUp{})).
		Select("user_id, COALESCE(SUM("+creditedQuotaExpression+"), 0) AS credited_quota, "+moneyTotalExpression+" AS money_micros", creditedQuotaArgs...).
		// Subscription completion mirrors have no credited quota or amount. Keep
		// this aggregate independent of the optional subscription table so user
		// list queries remain usable during partial migrations.
		Where("(credited_quota <> 0 OR amount <> 0)").
		Group("user_id")
}

func joinUserTopupTotals(tx, query *gorm.DB) *gorm.DB {
	return query.Joins(
		"LEFT JOIN (?) AS user_topup_totals ON user_topup_totals.user_id = users.id",
		userTopupTotals(tx),
	)
}

func joinAssistantReviewViolationTotals(tx, query *gorm.DB) *gorm.DB {
	return query.Joins(
		"LEFT JOIN (?) AS assistant_review_violation_totals ON assistant_review_violation_totals.user_id = users.id",
		AssistantReviewViolationTotals(tx),
	)
}

// PopulateUserTopups adds one bounded aggregate query to an administrator
// user list. It deliberately groups in SQL so the handler never loads every
// historical payment row into memory.
func PopulateUserTopups(users []*User) error {
	if len(users) == 0 {
		return nil
	}
	ids := make([]int, 0, len(users))
	for _, user := range users {
		if user == nil || user.Id <= 0 {
			continue
		}
		ids = append(ids, user.Id)
		user.TopupSummary = &UserTopupSummary{Methods: []UserTopupMethod{}}
	}
	if len(ids) == 0 {
		return nil
	}

	var rows []userTopupAggregate
	creditedQuotaExpression, creditedQuotaArgs := positiveNormalizedCreditedQuotaSQL()
	moneyMicrosExpression := userTopupMoneyMicrosSQL(DB)
	settlementCurrencyExpression := "COALESCE(NULLIF(UPPER(TRIM(settlement_currency)), ''), 'UNKNOWN')"
	if err := successfulExternalPaidTopUpQuery(DB.Model(&TopUp{})).
		Select("user_id, payment_method, payment_provider, "+settlementCurrencyExpression+" AS settlement_currency, COALESCE(SUM("+creditedQuotaExpression+"), 0) AS credited_quota, COALESCE(SUM("+moneyMicrosExpression+"), 0) AS money_micros, COUNT(*) AS orders", creditedQuotaArgs...).
		Where("user_id IN ?", ids).
		Where("(credited_quota <> 0 OR amount <> 0)").
		Group("user_id, payment_method, payment_provider, " + settlementCurrencyExpression).
		Order("user_id ASC, payment_method ASC, payment_provider ASC, settlement_currency ASC").
		Scan(&rows).Error; err != nil {
		return err
	}

	byID := make(map[int]*UserTopupSummary, len(ids))
	for _, user := range users {
		if user != nil && user.TopupSummary != nil {
			byID[user.Id] = user.TopupSummary
		}
	}
	currencyTotals := make(map[int]map[string]int64, len(ids))
	for _, row := range rows {
		summary := byID[row.UserID]
		if summary == nil {
			continue
		}
		currency := strings.ToUpper(strings.TrimSpace(row.SettlementCurrency))
		if currency == "" {
			currency = "UNKNOWN"
		}
		method := UserTopupMethod{
			Method:             strings.TrimSpace(row.PaymentMethod),
			Provider:           strings.TrimSpace(row.PaymentProvider),
			SettlementCurrency: currency,
			Quota:              row.CreditedQuota,
			MoneyMicros:        row.MoneyMicros,
			Orders:             row.Orders,
		}
		summary.Quota += method.Quota
		summary.Orders += method.Orders
		summary.Methods = append(summary.Methods, method)
		if currencyTotals[row.UserID] == nil {
			currencyTotals[row.UserID] = make(map[string]int64)
		}
		currencyTotals[row.UserID][currency] += method.MoneyMicros
	}
	for userID, totals := range currencyTotals {
		summary := byID[userID]
		if summary == nil {
			continue
		}
		if len(totals) != 1 {
			summary.Currency = "MULTIPLE"
			summary.MoneyMicros = 0
			continue
		}
		for currency, total := range totals {
			summary.Currency = currency
			summary.MoneyMicros = total
		}
	}
	return nil
}

// User if you add sensitive fields, don't forget to clean them in setupLogin function.
// Otherwise, the sensitive information will be saved on local storage in plain text!
type User struct {
	Id                            int             `json:"id"`
	Username                      string          `json:"username" gorm:"unique;index" validate:"max=20"`
	Password                      string          `json:"password" gorm:"not null;" validate:"min=8,max=20"`
	OriginalPassword              string          `json:"original_password" gorm:"-:all"` // this field is only for Password change verification, don't save it to database!
	DisplayName                   string          `json:"display_name" gorm:"index" validate:"max=20"`
	Role                          int             `json:"role" gorm:"type:int;default:1"`   // admin, common
	Status                        int             `json:"status" gorm:"type:int;default:1"` // enabled, disabled
	Email                         string          `json:"email" gorm:"index" validate:"max=50"`
	GitHubId                      string          `json:"github_id" gorm:"column:github_id;index"`
	DiscordId                     string          `json:"discord_id" gorm:"column:discord_id;index"`
	OidcId                        string          `json:"oidc_id" gorm:"column:oidc_id;index"`
	WeChatId                      string          `json:"wechat_id" gorm:"column:wechat_id;index"`
	TelegramId                    string          `json:"telegram_id" gorm:"column:telegram_id;index"`
	VerificationCode              string          `json:"verification_code" gorm:"-:all"`                         // this field is only for Email verification, don't save it to database!
	AccessToken                   *string         `json:"-" gorm:"type:char(32);column:access_token;uniqueIndex"` // this token is for system management
	Quota                         int             `json:"quota" gorm:"type:bigint;default:0"`
	UsedQuota                     int             `json:"used_quota" gorm:"type:bigint;default:0;column:used_quota"` // used quota
	RequestCount                  int             `json:"request_count" gorm:"type:int;default:0;"`                  // request number
	Group                         string          `json:"group" gorm:"type:varchar(64);default:'default'"`
	AffCode                       string          `json:"aff_code" gorm:"type:varchar(32);column:aff_code;uniqueIndex"`
	AffCount                      int             `json:"aff_count" gorm:"type:int;default:0;column:aff_count"`
	AffQuota                      int             `json:"aff_quota" gorm:"type:bigint;default:0;column:aff_quota"`           // 邀请剩余额度
	AffHistoryQuota               int             `json:"aff_history_quota" gorm:"type:bigint;default:0;column:aff_history"` // 邀请历史额度
	InviterId                     int             `json:"inviter_id" gorm:"type:int;column:inviter_id;index"`
	DeletedAt                     gorm.DeletedAt  `gorm:"index"`
	LinuxDOId                     string          `json:"linux_do_id" gorm:"column:linux_do_id;index"`
	LinuxDOGamificationScore      float64         `json:"-" gorm:"not null;default:0;column:linux_do_gamification_score"`
	LinuxDOScoreUpdatedAt         int64           `json:"-" gorm:"type:bigint;not null;default:0;column:linux_do_score_updated_at"`
	Setting                       string          `json:"setting" gorm:"type:text;column:setting"`
	Remark                        string          `json:"remark,omitempty" gorm:"type:varchar(255)" validate:"max=255"`
	StripeCustomer                string          `json:"stripe_customer" gorm:"type:varchar(64);column:stripe_customer;index"`
	CreatedAt                     int64           `json:"created_at" gorm:"autoCreateTime;column:created_at"`
	LastLoginAt                   int64           `json:"last_login_at" gorm:"default:0;column:last_login_at"`
	LastAPIActivityAt             int64           `json:"last_api_activity_at" gorm:"type:bigint;not null;default:0;column:last_api_activity_at"`
	TrustLevelOverride            *int            `json:"trust_level_override" gorm:"type:int;column:trust_level_override"`
	TrustLevelInfo                *TrustLevelInfo `json:"trust_level_info,omitempty" gorm:"-:all"`
	PaymentRestrictionFlags       int             `json:"-" gorm:"type:int;not null;default:0;column:payment_restriction_flags"`
	AdminPaymentRestrictionFlags  int             `json:"payment_restriction_flags,omitempty" gorm:"-:all"`
	AdminDisposableEmail          bool            `json:"disposable_email,omitempty" gorm:"-:all"`
	AdminLinuxDOGamificationScore *float64        `json:"linux_do_gamification_score,omitempty" gorm:"-:all"`
	AdminLinuxDOScoreUpdatedAt    int64           `json:"linux_do_score_updated_at,omitempty" gorm:"-:all"`
	AssistantConversationCount    *int64          `json:"assistant_conversation_count,omitempty" gorm:"-:all"`
	AssistantViolationCount       *int64          `json:"assistant_violation_count,omitempty" gorm:"-:all"`
	// AssistantProfile is populated only by administrator-facing user
	// management handlers after the strict lower-role visibility check. It is
	// never loaded by the normal user serializer or persisted with User.
	AssistantProfile *AssistantUserProfileSummary `json:"assistant_profile,omitempty" gorm:"-:all"`
	TopupSummary     *UserTopupSummary            `json:"topup_summary,omitempty" gorm:"-:all"`

	ConsoleActivatedAt int64                      `json:"console_activated_at" gorm:"type:bigint;not null;default:0;column:console_activated_at"`
	AuthVersion        int64                      `json:"-" gorm:"type:bigint;not null;default:1;column:auth_version"`
	AdminPermissions   map[string]map[string]bool `json:"admin_permissions,omitempty" gorm:"-:all"`
}

func (user *User) ToBaseUser() *UserBase {
	cache := &UserBase{
		Id:                 user.Id,
		Group:              user.Group,
		Quota:              user.Quota,
		Status:             user.Status,
		Role:               user.Role,
		Username:           user.Username,
		Setting:            user.Setting,
		Email:              user.Email,
		CreatedAt:          user.CreatedAt,
		LastAPIActivityAt:  user.LastAPIActivityAt,
		TrustLevelOverride: user.TrustLevelOverride,
		ConsoleActivatedAt: user.ConsoleActivatedAt,
		AuthVersion:        user.AuthVersion,
		CacheSchema:        userCacheSchemaVersion,
	}
	return cache
}

func (user *User) GetAccessToken() string {
	if user.AccessToken == nil {
		return ""
	}
	return *user.AccessToken
}

func (user *User) SetAccessToken(token string) {
	user.AccessToken = &token
}

// UpdateUserAccessToken rotates a dashboard personal access token without
// writing a stale user snapshot back over concurrently updated fields.
func UpdateUserAccessToken(id int, token string) error {
	if id == 0 {
		return errors.New("id 为空！")
	}
	result := DB.Model(&User{}).Where("id = ?", id).Update("access_token", token)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

var userBindColumns = map[string]bool{
	"github_id":   true,
	"discord_id":  true,
	"oidc_id":     true,
	"wechat_id":   true,
	"linux_do_id": true,
}

// UpdateUserBindColumn changes only a whitelisted OAuth binding column. OAuth
// callbacks can race with an administrator disabling, demoting, or regrouping
// the same account; writing a previously loaded User snapshot would otherwise
// restore those stale fields.
func UpdateUserBindColumn(userID int, column, value string) error {
	if userID <= 0 {
		return errors.New("id 为空！")
	}
	if !userBindColumns[column] {
		return fmt.Errorf("invalid user bind column: %s", column)
	}
	result := DB.Model(&User{}).Where("id = ?", userID).Update(column, value)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (user *User) GetSetting() dto.UserSetting {
	setting := dto.UserSetting{}
	if user.Setting != "" {
		err := common.Unmarshal([]byte(user.Setting), &setting)
		if err != nil {
			common.SysLog("failed to unmarshal setting: " + err.Error())
		}
	}
	return setting
}

func (user *User) SetSetting(setting dto.UserSetting) {
	settingBytes, err := common.Marshal(setting)
	if err != nil {
		common.SysLog("failed to marshal setting: " + err.Error())
		return
	}
	user.Setting = string(settingBytes)
}

func UpdateUserSetting(userId int, setting dto.UserSetting) error {
	if userId == 0 {
		return errors.New("id 为空！")
	}
	if err := dto.ValidateSidebarModules(setting.SidebarModules); err != nil {
		return err
	}
	settingBytes, err := common.Marshal(setting)
	if err != nil {
		return err
	}
	settingValue := string(settingBytes)
	if err = DB.Model(&User{}).Where("id = ?", userId).Update("setting", settingValue).Error; err != nil {
		return err
	}
	return updateUserSettingCache(userId, settingValue)
}

// 根据用户角色生成默认的边栏配置
func generateDefaultSidebarConfigForRole(userRole int) string {
	defaultConfig := map[string]interface{}{}

	// 聊天区域 - 所有用户都可以访问
	defaultConfig["chat"] = map[string]interface{}{
		"enabled": true,
		"chat":    true,
	}

	// 控制台区域 - 所有用户都可以访问
	defaultConfig["console"] = map[string]interface{}{
		"enabled":    true,
		"detail":     true,
		"token":      true,
		"log":        true,
		"midjourney": true,
		"task":       true,
	}

	// 个人中心区域 - 所有用户都可以访问
	defaultConfig["personal"] = map[string]interface{}{
		"enabled":  true,
		"topup":    true,
		"personal": true,
	}

	// 管理员区域 - 根据角色决定
	if userRole == common.RoleAdminUser {
		// 管理员可以访问管理员区域，但不能访问系统设置
		defaultConfig["admin"] = map[string]interface{}{
			"enabled":    true,
			"channel":    true,
			"models":     true,
			"redemption": true,
			"user":       true,
			"setting":    false, // 管理员不能访问系统设置
		}
	} else if userRole == common.RoleRootUser {
		// 超级管理员可以访问所有功能
		defaultConfig["admin"] = map[string]interface{}{
			"enabled":    true,
			"channel":    true,
			"models":     true,
			"redemption": true,
			"user":       true,
			"setting":    true,
		}
	}
	// 普通用户不包含admin区域

	// 转换为JSON字符串
	configBytes, err := common.Marshal(defaultConfig)
	if err != nil {
		common.SysLog("生成默认边栏配置失败: " + err.Error())
		return ""
	}

	return string(configBytes)
}

// CheckUserExistOrDeleted check if user exist or deleted, if not exist, return false, nil, if deleted or exist, return true, nil
func CheckUserExistOrDeleted(username string, email string) (bool, error) {
	var user User

	// err := DB.Unscoped().First(&user, "username = ? or email = ?", username, email).Error
	// check email if empty
	var err error
	email = NormalizeEmail(email)
	if email == "" {
		err = DB.Unscoped().First(&user, "username = ?", username).Error
	} else {
		err = DB.Unscoped().First(&user, "username = ? or LOWER(email) = ?", username, email).Error
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// not exist, return false, nil
			return false, nil
		}
		// other error, return false, err
		return false, err
	}
	// exist, return true, nil
	return true, nil
}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func emailQuery(tx *gorm.DB, email string) *gorm.DB {
	if tx == nil {
		tx = DB
	}
	return tx.Unscoped().Model(&User{}).Where("LOWER(email) = ?", NormalizeEmail(email))
}

func CountUsersByEmail(email string) (int64, error) {
	email = NormalizeEmail(email)
	if email == "" {
		return 0, nil
	}
	var count int64
	err := emailQuery(DB, email).Count(&count).Error
	return count, err
}

func IsEmailAvailable(email string, excludeUserID int) (bool, error) {
	email = NormalizeEmail(email)
	if email == "" {
		return true, nil
	}
	query := emailQuery(DB, email)
	if excludeUserID > 0 {
		query = query.Where("id <> ?", excludeUserID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}
	return count == 0, nil
}

func EnsureEmailAvailable(email string, excludeUserID int) error {
	available, err := IsEmailAvailable(email, excludeUserID)
	if err != nil {
		return err
	}
	if !available {
		return ErrEmailAlreadyTaken
	}
	return nil
}

// withNormalizedEmailLock serializes concurrent writers that target the same
// normalized email inside tx, so a "check then write" sequence cannot be raced
// by two transactions. It must be called inside an active transaction; the lock
// is scoped to that transaction and released on commit/rollback.
//
//   - PostgreSQL: transaction-level advisory lock keyed by the normalized email.
//   - MySQL (default REPEATABLE READ): a locking read that takes a next-key/gap
//     lock on the email index, blocking concurrent inserts of the same value.
//   - SQLite: no explicit lock; the single-writer model already serializes the
//     write, so a racing second write fails instead of duplicating.
//
// An empty email is allowed to repeat and needs no serialization.
func withNormalizedEmailLock(tx *gorm.DB, email string, fn func(tx *gorm.DB) error) error {
	email = NormalizeEmail(email)
	if email == "" {
		return fn(tx)
	}
	switch {
	case common.UsingMainDatabase(common.DatabaseTypePostgreSQL):
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", email).Error; err != nil {
			return err
		}
	case common.UsingMainDatabase(common.DatabaseTypeMySQL):
		var ids []int
		if err := tx.Raw("SELECT id FROM users WHERE email = ? FOR UPDATE", email).Scan(&ids).Error; err != nil {
			return err
		}
	}
	return fn(tx)
}

func GetMaxUserId() int {
	var user User
	DB.Unscoped().Last(&user)
	return user.Id
}

func applyL0UserFilter(tx *gorm.DB, query *gorm.DB) *gorm.DB {
	creditedQuotaExpression, creditedQuotaArgs := positiveNormalizedCreditedQuotaSQL()
	paidTopUpSubquery := successfulExternalPaidTopUpQuery(tx.Model(&TopUp{}).
		Select("1").
		Where("top_ups.user_id = users.id")).
		Where("("+creditedQuotaExpression+") > 0", creditedQuotaArgs...)

	return query.
		Where("users.role < ?", common.RoleAdminUser).
		Where(
			"(users.trust_level_override IS NOT NULL AND users.trust_level_override NOT BETWEEN ? AND ?) OR "+
				"(users.trust_level_override IS NULL AND users.console_activated_at = 0 AND NOT EXISTS (?))",
			TrustLevelMinUser+1,
			TrustLevelMaxUser,
			paidTopUpSubquery,
		)
}

func GetAllUsers(pageInfo *common.PageInfo, onlyL0 bool, sortOptions ...UserSortOptions) (users []*User, total int64, err error) {
	// Start transaction
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := tx.Unscoped().Model(&User{})
	query = joinUserTopupTotals(tx, query)
	if assistantReviewTablesAvailable(tx) {
		query = joinAssistantReviewViolationTotals(tx, query)
	}
	if onlyL0 {
		query = applyL0UserFilter(tx, query)
	}

	// Get total count within transaction
	err = query.Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Get paginated users within same transaction
	order := resolveUserSortOptions(sortOptions)
	err = order.Apply(query).Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Omit("password", "access_token").Find(&users).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}
	// Commit transaction
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}
	if err = EnrichUsersTrustLevels(users); err != nil {
		return nil, 0, err
	}

	return users, total, nil
}

func SearchUsers(keyword string, group string, role *int, status *int, onlyL0 bool, startIdx int, num int, sortOptions ...UserSortOptions) ([]*User, int64, error) {
	var users []*User
	var total int64
	var err error

	// 开始事务
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 构建基础查询
	query := tx.Unscoped().Model(&User{})
	query = joinUserTopupTotals(tx, query)
	if assistantReviewTablesAvailable(tx) {
		query = joinAssistantReviewViolationTotals(tx, query)
	}
	if onlyL0 {
		query = applyL0UserFilter(tx, query)
	}

	// 构建搜索条件
	likeCondition := "username LIKE ? OR email LIKE ? OR display_name LIKE ?"
	likeArgs := []interface{}{"%" + keyword + "%", "%" + keyword + "%", "%" + keyword + "%"}

	// 尝试将关键字转换为整数ID
	keywordInt, err := strconv.Atoi(keyword)
	if err == nil {
		// 如果是数字，同时搜索ID和其他字段
		likeCondition = "id = ? OR " + likeCondition
		likeArgs = append([]interface{}{keywordInt}, likeArgs...)
	}

	query = query.Where("("+likeCondition+")", likeArgs...)
	if group != "" {
		query = query.Where(commonGroupCol+" = ?", group)
	}
	if role != nil {
		query = query.Where("role = ?", *role)
	}
	if status != nil {
		if *status == -1 {
			query = query.Where("deleted_at IS NOT NULL")
		} else {
			query = query.Where("deleted_at IS NULL").Where("status = ?", *status)
		}
	}

	// 获取总数
	err = query.Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 获取分页数据
	order := resolveUserSortOptions(sortOptions)
	err = order.Apply(query.Omit("password", "access_token")).Limit(num).Offset(startIdx).Find(&users).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}
	// 提交事务
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}
	if err = EnrichUsersTrustLevels(users); err != nil {
		return nil, 0, err
	}

	return users, total, nil
}

func GetUserById(id int, selectAll bool) (*User, error) {
	if id == 0 {
		return nil, errors.New("id 为空！")
	}
	user := User{Id: id}
	var err error = nil
	if selectAll {
		err = DB.First(&user, "id = ?", id).Error
	} else {
		err = DB.Omit("password", "access_token").First(&user, "id = ?", id).Error
	}
	return &user, err
}

func GetUserIdByAffCode(affCode string) (int, error) {
	if affCode == "" {
		return 0, errors.New("affCode 为空！")
	}
	var user User
	err := DB.Select("id").First(&user, "aff_code = ?", affCode).Error
	return user.Id, err
}

func registrationQuotaForEmail(email string) int {
	if IsDisposableEmail(email) {
		return 0
	}
	return common.QuotaForNewUser
}

func promotionRewardsAllowedForUser(user *User) bool {
	return user != nil && !IsDisposableEmail(user.Email)
}

func promotionRewardsAllowedForUserID(userID int) bool {
	if userID <= 0 || DB == nil {
		return false
	}
	var user User
	if err := DB.Select("email").First(&user, "id = ?", userID).Error; err != nil {
		return false
	}
	return promotionRewardsAllowedForUser(&user)
}

func DeleteUserById(id int) (err error) {
	if id == 0 {
		return errors.New("id 为空！")
	}
	user := User{Id: id}
	return user.Delete()
}

func HardDeleteUserById(id int) error {
	if id == 0 {
		return errors.New("id 为空！")
	}
	user := User{Id: id}
	return user.HardDelete()
}

func inviteUser(inviterId int) error {
	result := DB.Model(&User{}).Where("id = ?", inviterId).Updates(map[string]interface{}{
		"aff_count":   gorm.Expr("aff_count + ?", 1),
		"aff_quota":   gorm.Expr("aff_quota + ?", common.QuotaForInviter),
		"aff_history": gorm.Expr("aff_history + ?", common.QuotaForInviter),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (user *User) TransferAffQuotaToQuota(quota int) error {
	// 检查quota是否小于最小额度
	if float64(quota) < common.QuotaPerUnit {
		return fmt.Errorf("转移额度最小为%s！", logger.LogQuota(common.QuotaFromFloat(common.QuotaPerUnit)))
	}
	if err := common.ValidateWalletQuota(quota); err != nil || quota <= 0 {
		return errors.New("邀请额度超出钱包安全范围")
	}

	// 开始数据库事务
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer tx.Rollback() // 确保在函数退出时事务能回滚

	// 加锁查询用户以确保数据一致性
	if err := lockForUpdate(tx).First(user, user.Id).Error; err != nil {
		return err
	}
	if user.AffQuota < quota {
		return errors.New("邀请额度不足！")
	}

	query, err := GuardWalletQuotaDelta(
		tx.Model(&User{}).Where("id = ? AND aff_quota >= ?", user.Id, quota),
		quota,
	)
	if err != nil {
		return err
	}
	result := query.Updates(map[string]interface{}{
		"quota":     gorm.Expr("quota + ?", quota),
		"aff_quota": gorm.Expr("aff_quota - ?", quota),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrWalletQuotaOutOfRange
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}
	user.AffQuota -= quota
	user.Quota += quota
	syncUserQuotaDeltaCacheAsync(user.Id, quota, "transfer affiliate quota")
	return nil
}

func (user *User) prepareForInsert(tx *gorm.DB) error {
	user.Email = NormalizeEmail(user.Email)
	if err := ensureEmailAvailableWithTx(tx, user.Email, 0); err != nil {
		return err
	}
	if user.Password == "" {
		return nil
	}
	var err error
	user.Password, err = common.Password2Hash(user.Password)
	return err
}

// BindEmailToUser atomically checks email availability and assigns it to the
// user, serializing concurrent binds of the same email so two accounts cannot
// end up sharing one address. The email is normalized before check and store.
func BindEmailToUser(user *User, email string) error {
	email = NormalizeEmail(email)
	if err := DB.Transaction(func(tx *gorm.DB) error {
		return withNormalizedEmailLock(tx, email, func(tx *gorm.DB) error {
			if err := ensureEmailAvailableWithTx(tx, email, user.Id); err != nil {
				return err
			}
			user.Email = email
			return user.UpdateWithTx(tx, false)
		})
	}); err != nil {
		return err
	}
	return updateUserCache(*user)
}

func ensureEmailAvailableWithTx(tx *gorm.DB, email string, excludeUserID int) error {
	email = NormalizeEmail(email)
	if email == "" {
		return nil
	}
	query := emailQuery(tx, email)
	if excludeUserID > 0 {
		query = query.Where("id <> ?", excludeUserID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrEmailAlreadyTaken
	}
	return nil
}

func (user *User) Insert(inviterId int) error {
	if err := DB.Transaction(func(tx *gorm.DB) error {
		return withNormalizedEmailLock(tx, user.Email, func(tx *gorm.DB) error {
			if err := user.prepareForInsert(tx); err != nil {
				return err
			}
			user.Quota = registrationQuotaForEmail(user.Email)
			user.AffCode = common.GetRandomString(4)

			// 初始化用户设置，包括默认的边栏配置
			if user.Setting == "" {
				defaultSetting := dto.UserSetting{}
				// 这里暂时不设置SidebarModules，因为需要在用户创建后根据角色设置
				user.SetSetting(defaultSetting)
			}

			return tx.Create(user).Error
		})
	}); err != nil {
		return err
	}

	user.finishInsert(inviterId)
	return nil
}

func (user *User) finishInsert(inviterId int) {
	// 用户创建成功后，根据角色初始化边栏配置
	// 需要重新获取用户以确保有正确的ID和Role
	var createdUser User
	if err := DB.Where("username = ?", user.Username).First(&createdUser).Error; err == nil {
		// 生成基于角色的默认边栏配置
		defaultSidebarConfig := generateDefaultSidebarConfigForRole(createdUser.Role)
		if defaultSidebarConfig != "" {
			currentSetting := createdUser.GetSetting()
			currentSetting.SidebarModules = defaultSidebarConfig
			createdUser.SetSetting(currentSetting)
			createdUser.Update(false)
			common.SysLog(fmt.Sprintf("为新用户 %s (角色: %d) 初始化边栏配置", createdUser.Username, createdUser.Role))
		}
	}

	newUserEligible := promotionRewardsAllowedForUser(user)
	if newUserEligible && common.QuotaForNewUser > 0 {
		RecordLog(user.Id, LogTypeSystem, fmt.Sprintf("新用户注册赠送 %s", logger.LogQuota(common.QuotaForNewUser)))
	}
	if inviterId != 0 && operation_setting.IsPaymentComplianceConfirmed() &&
		newUserEligible && promotionRewardsAllowedForUserID(inviterId) {
		if common.QuotaForInvitee > 0 {
			_ = IncreaseUserQuota(user.Id, common.QuotaForInvitee, true)
			RecordLog(user.Id, LogTypeSystem, fmt.Sprintf("使用邀请码赠送 %s", logger.LogQuota(common.QuotaForInvitee)))
		}
		if common.QuotaForInviter > 0 {
			//_ = IncreaseUserQuota(inviterId, common.QuotaForInviter)
			RecordLog(inviterId, LogTypeSystem, fmt.Sprintf("邀请用户赠送 %s", logger.LogQuota(common.QuotaForInviter)))
			_ = inviteUser(inviterId)
		}
	}
}

func (user *User) FinishInsert(inviterId int) {
	user.finishInsert(inviterId)
}

// InsertWithTx inserts a new user within an existing transaction.
// This is used for OAuth registration where user creation and binding need to be atomic.
// Post-creation tasks (sidebar config, logs, inviter rewards) are handled after the transaction commits.
func (user *User) InsertWithTx(tx *gorm.DB, inviterId int) error {
	return withNormalizedEmailLock(tx, user.Email, func(tx *gorm.DB) error {
		if err := user.prepareForInsert(tx); err != nil {
			return err
		}
		user.Quota = registrationQuotaForEmail(user.Email)
		user.AffCode = common.GetRandomString(4)

		// 初始化用户设置
		if user.Setting == "" {
			defaultSetting := dto.UserSetting{}
			user.SetSetting(defaultSetting)
		}

		return tx.Create(user).Error
	})
}

// FinalizeOAuthUserCreation performs post-transaction tasks for OAuth user creation.
// This should be called after the transaction commits successfully.
func (user *User) FinalizeOAuthUserCreation(inviterId int) {
	// 用户创建成功后，根据角色初始化边栏配置
	var createdUser User
	if err := DB.Where("id = ?", user.Id).First(&createdUser).Error; err == nil {
		defaultSidebarConfig := generateDefaultSidebarConfigForRole(createdUser.Role)
		if defaultSidebarConfig != "" {
			currentSetting := createdUser.GetSetting()
			currentSetting.SidebarModules = defaultSidebarConfig
			createdUser.SetSetting(currentSetting)
			createdUser.Update(false)
			common.SysLog(fmt.Sprintf("为新用户 %s (角色: %d) 初始化边栏配置", createdUser.Username, createdUser.Role))
		}
	}

	newUserEligible := promotionRewardsAllowedForUser(user)
	if newUserEligible && common.QuotaForNewUser > 0 {
		RecordLog(user.Id, LogTypeSystem, fmt.Sprintf("新用户注册赠送 %s", logger.LogQuota(common.QuotaForNewUser)))
	}
	if inviterId != 0 && operation_setting.IsPaymentComplianceConfirmed() &&
		newUserEligible && promotionRewardsAllowedForUserID(inviterId) {
		if common.QuotaForInvitee > 0 {
			_ = IncreaseUserQuota(user.Id, common.QuotaForInvitee, true)
			RecordLog(user.Id, LogTypeSystem, fmt.Sprintf("使用邀请码赠送 %s", logger.LogQuota(common.QuotaForInvitee)))
		}
		if common.QuotaForInviter > 0 {
			RecordLog(inviterId, LogTypeSystem, fmt.Sprintf("邀请用户赠送 %s", logger.LogQuota(common.QuotaForInviter)))
			_ = inviteUser(inviterId)
		}
	}
}

func (user *User) Update(updatePassword bool) error {
	var previousAuthVersion int64
	if err := DB.Model(&User{}).Where("id = ?", user.Id).Select("auth_version").Find(&previousAuthVersion).Error; err != nil {
		return err
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		return user.UpdateWithTx(tx, updatePassword)
	}); err != nil {
		return err
	}
	if err := updateUserCache(*user); err != nil {
		return err
	}
	if user.AuthVersion > previousAuthVersion {
		_, err := RevokeAllUserSessions(user.Id, "user_security_changed")
		return err
	}
	return nil
}

func (user *User) UpdateWithTx(tx *gorm.DB, updatePassword bool) error {
	var err error
	if updatePassword {
		user.Password, err = common.Password2Hash(user.Password)
		if err != nil {
			return err
		}
	}
	newUser := *user
	current := User{}
	if err = tx.First(&current, user.Id).Error; err != nil {
		return err
	}
	// Updates(struct) ignores zero values. Match that behavior when deciding
	// whether this request actually changes authentication-sensitive state;
	// partial self-profile updates intentionally leave role/status/group empty.
	authChanged := (updatePassword && current.Password != newUser.Password) ||
		(newUser.Role != 0 && current.Role != newUser.Role) ||
		(newUser.Status != 0 && current.Status != newUser.Status) ||
		(newUser.Group != "" && current.Group != newUser.Group)
	if authChanged {
		newUser.AuthVersion, err = IncrementUserAuthVersionWithTx(tx, user.Id)
		if err != nil {
			return err
		}
	}
	if err = tx.Model(&current).Omit(
		"access_token",
		"quota",
		"used_quota",
		"request_count",
		"aff_count",
		"aff_quota",
		"aff_history",
		"auth_version",
	).Updates(newUser).Error; err != nil {
		return err
	}
	return tx.First(user, user.Id).Error
}

func (user *User) Edit(updatePassword bool) error {
	var previousAuthVersion int64
	if err := DB.Model(&User{}).Where("id = ?", user.Id).Select("auth_version").Find(&previousAuthVersion).Error; err != nil {
		return err
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		return user.EditWithTx(tx, updatePassword)
	}); err != nil {
		return err
	}
	if err := updateUserCache(*user); err != nil {
		return err
	}
	if user.AuthVersion > previousAuthVersion {
		_, err := RevokeAllUserSessions(user.Id, "user_security_changed")
		return err
	}
	return nil
}

func (user *User) EditWithTx(tx *gorm.DB, updatePassword bool) error {
	var err error
	if updatePassword {
		user.Password, err = common.Password2Hash(user.Password)
		if err != nil {
			return err
		}
	}

	newUser := *user
	updates := map[string]interface{}{
		"username":     newUser.Username,
		"display_name": newUser.DisplayName,
		"group":        newUser.Group,
		"remark":       newUser.Remark,
	}
	if updatePassword {
		updates["password"] = newUser.Password
	}

	current := User{}
	if err = tx.First(&current, user.Id).Error; err != nil {
		return err
	}
	authChanged := (updatePassword && current.Password != newUser.Password) || current.Group != newUser.Group
	if authChanged {
		newUser.AuthVersion, err = IncrementUserAuthVersionWithTx(tx, user.Id)
		if err != nil {
			return err
		}
	}
	if err = tx.Model(&current).Updates(updates).Error; err != nil {
		return err
	}
	return tx.First(user, user.Id).Error
}

func (user *User) ClearBinding(bindingType string) error {
	if user.Id == 0 {
		return errors.New("user id is empty")
	}

	bindingColumnMap := map[string]string{
		"email":    "email",
		"github":   "github_id",
		"discord":  "discord_id",
		"oidc":     "oidc_id",
		"wechat":   "wechat_id",
		"telegram": "telegram_id",
		"linuxdo":  "linux_do_id",
	}

	column, ok := bindingColumnMap[bindingType]
	if !ok {
		return errors.New("invalid binding type")
	}

	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&User{}).Where("id = ?", user.Id).Update(column, "").Error; err != nil {
			return err
		}
		if bindingType == ExternalIdentityProviderTelegram {
			return ReleaseExternalIdentityWithTx(tx, ExternalIdentityProviderTelegram, user.Id)
		}
		return nil
	}); err != nil {
		return err
	}

	if err := DB.Where("id = ?", user.Id).First(user).Error; err != nil {
		return err
	}

	return updateUserCache(*user)
}

func (user *User) Delete() error {
	if user.Id == 0 {
		return errors.New("id 为空！")
	}
	var nextAuthVersion int64
	if err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		nextAuthVersion, err = IncrementUserAuthVersionWithTx(tx, user.Id)
		if err != nil {
			return err
		}
		if err := deleteUserAssistantData(tx, user.Id); err != nil {
			return err
		}
		return tx.Delete(user).Error
	}); err != nil {
		return err
	}
	if err := publishCommittedUserAuthVersion(user.Id, nextAuthVersion); err != nil {
		return err
	}
	if _, err := RevokeAllUserSessions(user.Id, "user_deleted"); err != nil {
		return err
	}
	return invalidateUserCache(user.Id)
}

func (user *User) HardDelete() error {
	if user.Id == 0 {
		return errors.New("id 为空！")
	}
	var tokens []Token
	var deletedAuthVersion int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		deletedAuthVersion, err = IncrementUserAuthVersionWithTx(tx, user.Id)
		if err != nil {
			return err
		}
		if common.RedisEnabled {
			if err := tx.Unscoped().Select("id", commonKeyCol).Where("user_id = ?", user.Id).Find(&tokens).Error; err != nil {
				return err
			}
		}
		if err := deleteUserAuthenticationData(tx, user.Id); err != nil {
			return err
		}
		if err := deleteUserAssistantData(tx, user.Id); err != nil {
			return err
		}
		return tx.Unscoped().Delete(user).Error
	})
	if err != nil {
		return err
	}
	if err := publishCommittedUserAuthVersion(user.Id, deletedAuthVersion); err != nil {
		common.SysError(fmt.Sprintf("failed to publish auth tombstone after hard deleting user %d: %v", user.Id, err))
	}
	if err := invalidateTokensCache(tokens); err != nil {
		common.SysError(fmt.Sprintf("failed to invalidate token cache after hard deleting user %d: %v", user.Id, err))
	}
	if err := invalidateUserCache(user.Id); err != nil {
		common.SysError(fmt.Sprintf("failed to invalidate user cache after hard deleting user %d: %v", user.Id, err))
	}
	return nil
}

func deleteUserAuthenticationData(tx *gorm.DB, userId int) error {
	if err := releaseAllExternalIdentitiesWithTx(tx, userId); err != nil {
		return err
	}
	for _, authenticationData := range []any{
		&TwoFABackupCode{},
		&TwoFA{},
		&UserSession{},
		&AuthFlow{},
		&PasskeyCredential{},
		&Token{},
	} {
		if err := tx.Unscoped().Where("user_id = ?", userId).Delete(authenticationData).Error; err != nil {
			return err
		}
	}
	return deleteUserOAuthBindingsByUserId(tx, userId)
}

// ValidateAndFill check password & user status
func (user *User) ValidateAndFill() (err error) {
	// When querying with struct, GORM will only query with non-zero fields,
	// that means if your field's value is 0, '', false or other zero values,
	// it won't be used to build query conditions
	password := user.Password
	username := strings.TrimSpace(user.Username)
	if username == "" || password == "" {
		return ErrUserEmptyCredentials
	}
	// find by username or email
	err = DB.Where("username = ? OR email = ?", username, username).First(user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrInvalidCredentials
		}
		return fmt.Errorf("%w: %v", ErrDatabase, err)
	}
	if user.Password == "" {
		return ErrInvalidCredentials
	}
	okay := common.ValidatePasswordAndHash(password, user.Password)
	if !okay || user.Status != common.UserStatusEnabled {
		return ErrInvalidCredentials
	}
	return nil
}

func (user *User) FillUserById() error {
	if user.Id == 0 {
		return errors.New("id 为空！")
	}
	DB.Where(User{Id: user.Id}).First(user)
	return nil
}

func (user *User) FillUserByEmail() error {
	if user.Email == "" {
		return errors.New("email 为空！")
	}
	DB.Where(User{Email: user.Email}).First(user)
	return nil
}

func (user *User) FillUserByGitHubId() error {
	if user.GitHubId == "" {
		return errors.New("GitHub id 为空！")
	}
	DB.Where(User{GitHubId: user.GitHubId}).First(user)
	return nil
}

// UpdateGitHubId updates the user's GitHub ID (used for migration from login to numeric ID)
func (user *User) UpdateGitHubId(newGitHubId string) error {
	if user.Id == 0 {
		return errors.New("user id is empty")
	}
	return DB.Model(user).Update("github_id", newGitHubId).Error
}

func (user *User) FillUserByDiscordId() error {
	if user.DiscordId == "" {
		return errors.New("discord id 为空！")
	}
	DB.Where(User{DiscordId: user.DiscordId}).First(user)
	return nil
}

func (user *User) FillUserByOidcId() error {
	if user.OidcId == "" {
		return errors.New("oidc id 为空！")
	}
	DB.Where(User{OidcId: user.OidcId}).First(user)
	return nil
}

func (user *User) FillUserByWeChatId() error {
	if user.WeChatId == "" {
		return errors.New("WeChat id 为空！")
	}
	DB.Where(User{WeChatId: user.WeChatId}).First(user)
	return nil
}

func (user *User) FillUserByTelegramId() error {
	if user.TelegramId == "" {
		return errors.New("Telegram id 为空！")
	}
	err := DB.Where(User{TelegramId: user.TelegramId}).First(user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New("该 Telegram 账户未绑定")
	}
	return nil
}

func IsEmailAlreadyTaken(email string) bool {
	count, err := CountUsersByEmail(email)
	return err == nil && count > 0
}

func GetUniqueUserByEmail(email string) (*User, error) {
	email = NormalizeEmail(email)
	if email == "" {
		return nil, ErrEmailNotFound
	}
	var users []User
	if err := DB.Where("LOWER(email) = ?", email).Limit(2).Find(&users).Error; err != nil {
		return nil, err
	}
	switch len(users) {
	case 0:
		return nil, ErrEmailNotFound
	case 1:
		return &users[0], nil
	default:
		return nil, ErrEmailAmbiguous
	}
}

func IsWeChatIdAlreadyTaken(wechatId string) bool {
	return DB.Unscoped().Where("wechat_id = ?", wechatId).Find(&User{}).RowsAffected == 1
}

func IsGitHubIdAlreadyTaken(githubId string) bool {
	return DB.Unscoped().Where("github_id = ?", githubId).Find(&User{}).RowsAffected == 1
}

func IsDiscordIdAlreadyTaken(discordId string) bool {
	return DB.Unscoped().Where("discord_id = ?", discordId).Find(&User{}).RowsAffected == 1
}

func IsOidcIdAlreadyTaken(oidcId string) bool {
	return DB.Where("oidc_id = ?", oidcId).Find(&User{}).RowsAffected == 1
}

func IsTelegramIdAlreadyTaken(telegramId string) bool {
	return DB.Unscoped().Where("telegram_id = ?", telegramId).Find(&User{}).RowsAffected == 1
}

func ResetUserPasswordByEmail(email string, password string) error {
	if email == "" || password == "" {
		return errors.New("邮箱地址或密码为空！")
	}
	user, err := GetUniqueUserByEmail(email)
	if err != nil {
		return err
	}
	hashedPassword, err := common.Password2Hash(password)
	if err != nil {
		return err
	}
	if err = DB.Transaction(func(tx *gorm.DB) error {
		if _, err := IncrementUserAuthVersionWithTx(tx, user.Id); err != nil {
			return err
		}
		return tx.Model(&User{}).Where("id = ?", user.Id).Update("password", hashedPassword).Error
	}); err != nil {
		return err
	}
	if err := PublishUserAuthCache(user.Id); err != nil {
		return err
	}
	_, err = RevokeAllUserSessions(user.Id, "password_reset")
	return err
}

func IsAdmin(userId int) bool {
	if userId == 0 {
		return false
	}
	var user User
	err := DB.Where("id = ?", userId).Select("role").Find(&user).Error
	if err != nil {
		common.SysLog("no such user " + err.Error())
		return false
	}
	return user.Role >= common.RoleAdminUser
}

func ValidateAccessToken(token string) (*User, error) {
	if token == "" {
		return nil, nil
	}
	token = strings.Replace(token, "Bearer ", "", 1)
	user := &User{}
	err := DB.Where("access_token = ?", token).First(user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: %v", ErrDatabase, err)
	}
	return user, nil
}

// GetUserQuota gets quota from Redis first, falls back to DB if needed
func GetUserQuota(id int, fromDB bool) (quota int, err error) {
	if !fromDB && common.RedisEnabled {
		return getUserQuotaCache(id)
	}
	err = DB.Model(&User{}).Where("id = ?", id).Select("quota").Find(&quota).Error
	if err != nil {
		return 0, err
	}

	return quota, nil
}

func GetUserUsedQuota(id int) (quota int, err error) {
	err = DB.Model(&User{}).Where("id = ?", id).Select("used_quota").Find(&quota).Error
	return quota, err
}

func GetUserEmail(id int) (email string, err error) {
	err = DB.Model(&User{}).Where("id = ?", id).Select("email").Find(&email).Error
	return email, err
}

// GetUserGroup gets group from Redis first, falls back to DB if needed
func GetUserGroup(id int, fromDB bool) (group string, err error) {
	defer func() {
		// Update Redis cache asynchronously on successful DB read
		if shouldUpdateRedis(fromDB, err) {
			gopool.Go(func() {
				if err := RefreshUserGroupCache(id); err != nil {
					common.SysLog("failed to update user group cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		group, err := getUserGroupCache(id)
		if err == nil {
			return group, nil
		}
		// Don't return error - fall through to DB
	}
	fromDB = true
	err = DB.Model(&User{}).Where("id = ?", id).Select(commonGroupCol).Find(&group).Error
	if err != nil {
		return "", err
	}

	return group, nil
}

// GetUserSetting gets setting from Redis first, falls back to DB if needed
func GetUserSetting(id int, fromDB bool) (settingMap dto.UserSetting, err error) {
	var setting string
	defer func() {
		// Update Redis cache asynchronously on successful DB read
		if shouldUpdateRedis(fromDB, err) {
			gopool.Go(func() {
				if err := updateUserSettingCache(id, setting); err != nil {
					common.SysLog("failed to update user setting cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		setting, err := getUserSettingCache(id)
		if err == nil {
			return setting, nil
		}
		// Don't return error - fall through to DB
	}
	fromDB = true
	// can be nil setting
	var safeSetting sql.NullString
	err = DB.Model(&User{}).Where("id = ?", id).Select("setting").Find(&safeSetting).Error
	if err != nil {
		return settingMap, err
	}
	if safeSetting.Valid {
		setting = safeSetting.String
	} else {
		setting = ""
	}
	userBase := &UserBase{
		Setting: setting,
	}
	return userBase.GetSetting(), nil
}

func IncreaseUserQuota(id int, quota int, db bool) error {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	if err := common.ValidateWalletQuota(quota); err != nil {
		return err
	}
	if !db && common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeUserQuota, id, quota)
		return nil
	}
	if err := increaseUserQuota(id, quota); err != nil {
		return err
	}
	syncUserQuotaDeltaCacheAsync(id, quota, "increase user quota")
	return nil
}

func increaseUserQuota(id int, quota int) error {
	return ApplyWalletQuotaDelta(DB, id, quota)
}

func DecreaseUserQuota(id int, quota int, db bool) error {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	if err := common.ValidateWalletQuota(quota); err != nil {
		return err
	}
	if !db && common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeUserQuota, id, -quota)
		return nil
	}
	if err := decreaseUserQuota(id, quota); err != nil {
		return err
	}
	syncUserQuotaDeltaCacheAsync(id, -quota, "decrease user quota")
	return nil
}

func decreaseUserQuota(id int, quota int) error {
	return ApplyWalletQuotaDelta(DB, id, -quota)
}

func syncUserQuotaDeltaCacheAsync(id int, delta int, operation string) {
	// Keep the local RedisEnabled race fix: do not enqueue a worker that only
	// observes a concurrently restored test flag. Database success is always
	// established before this helper is called.
	if !common.RedisEnabled || delta == 0 {
		return
	}
	gopool.Go(func() {
		if err := cacheIncrUserQuota(id, int64(delta)); err != nil {
			common.SysLog("failed to " + operation + ": " + err.Error())
		}
	})
}

func DeltaUpdateUserQuota(id int, delta int) (err error) {
	if delta == 0 {
		return nil
	}
	if delta > 0 {
		return IncreaseUserQuota(id, delta, false)
	} else {
		return DecreaseUserQuota(id, -delta, false)
	}
}

//func GetRootUserEmail() (email string) {
//	DB.Model(&User{}).Where("role = ?", common.RoleRootUser).Select("email").Find(&email)
//	return email
//}

func GetRootUser() *User {
	var user User
	if err := DB.Where("role = ?", common.RoleRootUser).First(&user).Error; err != nil {
		return nil
	}
	return &user
}

func UpdateUserLastLoginAt(id int) {
	if err := DB.Model(&User{}).Where("id = ?", id).Update("last_login_at", common.GetTimestamp()).Error; err != nil {
		common.SysLog("failed to update user last_login_at: " + err.Error())
	}
}

func UpdateUserUsedQuotaAndRequestCount(id int, quota int) {
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeUsedQuota, id, quota)
		addNewRecord(BatchUpdateTypeRequestCount, id, 1)
		return
	}
	updateUserUsedQuotaAndRequestCount(id, quota, 1)
}

// UpdateUserUsedQuota adjusts accumulated usage without incrementing the
// request counter. Refunds and asynchronous final settlements use this path
// so usage totals remain reversible while request volume stays factual.
func UpdateUserUsedQuota(id int, quota int) {
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeUsedQuota, id, quota)
		return
	}
	if err := DB.Model(&User{}).Where("id = ?", id).
		Update("used_quota", boundedQuotaCounterExpr("used_quota", quota)).Error; err != nil {
		common.SysLog("failed to update user used quota: " + err.Error())
	}
}

func updateUserUsedQuotaAndRequestCount(id int, quota int, count int) {
	err := DB.Model(&User{}).Where("id = ?", id).Updates(
		map[string]interface{}{
			"used_quota":           boundedQuotaCounterExpr("used_quota", quota),
			"request_count":        boundedInt32CounterExpr(count),
			"last_api_activity_at": common.GetTimestamp(),
		},
	).Error
	if err != nil {
		common.SysLog("failed to update user used quota and request count: " + err.Error())
		return
	}

	//// 更新缓存
	//if err := invalidateUserCache(id); err != nil {
	//	common.SysError("failed to invalidate user cache: " + err.Error())
	//}
}

func updateUserQuotaUsedQuotaAndRequestCount(id int, quota int, usedQuota int, requestCount int) error {
	if quota == 0 && usedQuota == 0 && requestCount == 0 {
		return nil
	}

	query := DB.Model(&User{}).Where("id = ?", id)
	var err error
	if quota != 0 {
		query, err = GuardWalletQuotaDelta(query, quota)
		if err != nil {
			common.SysLog("failed to batch update user quota, used quota and request count: " + err.Error())
			return err
		}
	}
	updates := map[string]interface{}{
		"used_quota":           boundedQuotaCounterExpr("used_quota", usedQuota),
		"request_count":        boundedInt32CounterExpr(requestCount),
		"last_api_activity_at": common.GetTimestamp(),
	}
	if quota != 0 {
		updates["quota"] = gorm.Expr("quota + ?", quota)
	}
	result := query.Updates(updates)
	if result.Error != nil {
		common.SysLog("failed to batch update user quota, used quota and request count: " + result.Error.Error())
		return result.Error
	}
	if quota != 0 && result.RowsAffected != 1 {
		common.SysLog("failed to batch update user quota, used quota and request count: wallet quota boundary exceeded")
		return ErrWalletQuotaOutOfRange
	}
	return nil
}

// GetUsernameById gets username from Redis first, falls back to DB if needed
func GetUsernameById(id int, fromDB bool) (username string, err error) {
	defer func() {
		// Update Redis cache asynchronously on successful DB read
		if shouldUpdateRedis(fromDB, err) {
			gopool.Go(func() {
				if err := updateUserNameCache(id, username); err != nil {
					common.SysLog("failed to update user name cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		username, err := getUserNameCache(id)
		if err == nil {
			return username, nil
		}
		// Don't return error - fall through to DB
	}
	fromDB = true
	err = DB.Model(&User{}).Where("id = ?", id).Select("username").Find(&username).Error
	if err != nil {
		return "", err
	}

	return username, nil
}

func IsLinuxDOIdAlreadyTaken(linuxDOId string) bool {
	var user User
	err := DB.Unscoped().Where("linux_do_id = ?", linuxDOId).First(&user).Error
	return !errors.Is(err, gorm.ErrRecordNotFound)
}

func (user *User) FillUserByLinuxDOId() error {
	if user.LinuxDOId == "" {
		return errors.New("linux do id is empty")
	}
	err := DB.Where("linux_do_id = ?", user.LinuxDOId).First(user).Error
	return err
}

func RootUserExists() bool {
	var user User
	err := DB.Where("role = ?", common.RoleRootUser).First(&user).Error
	if err != nil {
		return false
	}
	return true
}
