#!/usr/bin/env python3
"""Temporary isolated workbench: apply exact, reviewed source replacements."""
from pathlib import Path
import subprocess

def change(path, old, new):
    p = Path('apps/api-go') / path
    text = p.read_text()
    if text.count(old) != 1:
        raise SystemExit(f'Expected one source match in {path}, got {text.count(old)}')
    p.write_text(text.replace(old, new, 1))

change('middleware/auth.go', '\tcase "/api/verify":\n\t\treturn method == http.MethodPost', '''\tcase "/api/verify", "/api/verify/email":
\t\treturn method == http.MethodPost
\tcase "/api/user/topup/info", "/api/user/topup/self":
\t\treturn method == http.MethodGet
\tcase "/api/user/discount-code/validate", "/api/user/amount", "/api/user/pay",
\t\t"/api/user/stripe/amount", "/api/user/stripe/pay", "/api/user/creem/pay",
\t\t"/api/user/waffo/amount", "/api/user/waffo/pay", "/api/user/waffo-pancake/amount", "/api/user/waffo-pancake/pay":
\t\t// Checkout must precede paid activation. UserAuth, body limits and the
\t\t// provider-specific payment policy still run on every request.
\t\treturn method == http.MethodPost
\tcase "/api/livez", "/api/uptime/status", "/api/scripts", "/api/games/signal/daily", "/api/games/signal/leaderboard":
\t\treturn method == http.MethodGet
\tcase "/api/games/signal/attempts", "/api/games/signal/finish":
\t\treturn method == http.MethodPost
\tcase "/api/games/signal/records":
\t\treturn method == http.MethodGet || method == http.MethodPost''')
change('middleware/auth.go', '\n\tif strings.HasPrefix(path, "/api/user/sessions/") ||', '''
\t// Only public raw-script reads; repository settings and editors remain root-only.
\tif strings.HasPrefix(path, "/api/scripts/") && strings.HasSuffix(path, "/raw") {
\t\tname := strings.TrimSuffix(strings.TrimPrefix(path, "/api/scripts/"), "/raw")
\t\treturn method == http.MethodGet && name != "" && !strings.Contains(name, "/")
\t}
\tif strings.HasPrefix(path, "/api/user/sessions/") ||''')
p = Path('apps/api-go/middleware/console_access_test.go')
s = p.read_text()
rows = ['\t\t{http.MethodGet, "/api/user/topup/info"},\n', '\t\t{http.MethodGet, "/api/user/topup/self"},\n', '\t\t{http.MethodPost, "/api/user/stripe/pay"},\n']
for row in rows:
    assert s.count(row) == 1
    s = s.replace(row, '', 1)
needle = '\t\t{http.MethodGet, "/api/open-source-bounties"},\n'
s = s.replace(needle, ''.join(rows) + needle, 1)
p.write_text(s)

change('model/topup.go', '''\t\t\tif settlement.CustomerEmail != "" {
\t\t\t\tvar user User
\t\t\t\tif err := tx.Select("email").First(&user, completed.UserId).Error; err != nil {
\t\t\t\t\treturn err
\t\t\t\t}
\t\t\t\tif user.Email == "" {
\t\t\t\t\tuserUpdates["email"] = settlement.CustomerEmail
\t\t\t\t}
\t\t\t}
''', '''\t\t\t// A payer's contact email is not a verified login-email binding.
\t\t\t// Do not change account identity or fail a valid credit on a shared
\t\t\t// payer address. Email binding remains a separate verified action.
''')

p = Path('apps/api-go/controller/user.go')
s = p.read_text()
start = s.index('\tif err := cleanUser.Insert(inviterId); err != nil {', s.index('func Register('))
end = s.index('\n\tc.JSON(http.StatusOK, gin.H{', start)
old = s[start:end]
assert 'recordAcquisitionRegistration' in old and 'token.Insert()' in old
s = s[:start] + '''\t// Prepare optional credentials before creating any durable account state.
\tvar initialToken *model.Token
\tif constant.GenerateDefaultToken {
\t\tkey, err := common.GenerateKey()
\t\tif err != nil {
\t\t\tcommon.ApiErrorI18n(c, i18n.MsgUserDefaultTokenFailed)
\t\t\treturn
\t\t}
\t\tinitialToken = &model.Token{
\t\t\tName: cleanUser.Username + "的初始令牌", Key: key,
\t\t\tCreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp(),
\t\t\tExpiredTime: -1, RemainQuota: 500000, UnlimitedQuota: true,
\t\t\tModelLimitsEnabled: false, CreationSource: model.TokenCreationSourceSystem,
\t\t}
\t\tif setting.DefaultUseAutoGroup {
\t\t\tinitialToken.Group = "auto"
\t\t}
\t}
\t// Account, invitation counter and initial key either all commit or all roll back.
\tif err := model.DB.Transaction(func(tx *gorm.DB) error {
\t\tif err := cleanUser.InsertWithTx(tx, inviterId); err != nil {
\t\t\treturn err
\t\t}
\t\tif initialToken != nil {
\t\t\tinitialToken.UserId = cleanUser.Id
\t\t\treturn tx.Create(initialToken).Error
\t\t}
\t\treturn nil
\t}); err != nil {
\t\tif errors.Is(err, model.ErrEmailAlreadyTaken) {
\t\t\tcommon.ApiErrorI18n(c, i18n.MsgUserEmailAlreadyTaken)
\t\t\treturn
\t\t}
\t\tcommon.ApiError(c, err)
\t\treturn
\t}
\tcleanUser.FinishInsert(inviterId)
\trecordAcquisitionRegistration(c, cleanUser.Id)
''' + s[end:]
p.write_text(s)

p = Path('apps/api-go/model/user.go')
s = p.read_text()
start = s.index('func applyL0UserFilter(')
end = s.index('\nfunc GetAllUsers(', start)
s = s[:start] + '''func applyL0UserFilter(tx *gorm.DB, query *gorm.DB) *gorm.DB {
\treturn applyL0UserFilterWithPolicy(tx, query, CurrentDeveloperAccessPolicy())
}

func applyL0UserFilterWithPolicy(tx *gorm.DB, query *gorm.DB, policy DeveloperAccessPolicy) *gorm.DB {
\tordinaryL0 := "users.trust_level_override IS NULL AND users.console_activated_at = 0"
\targs := []interface{}{TrustLevelMinUser + 1, TrustLevelMaxUser}
\tif policy.paidActivationEnabled {
\t\texpression, expressionArgs := positiveNormalizedCreditedQuotaSQL()
\t\tpaid := successfulExternalPaidTopUpQuery(tx.Model(&TopUp{}).
\t\t\tSelect("1").Where("top_ups.user_id = users.id")).
\t\t\tWhere("("+expression+") > 0", expressionArgs...)
\t\tif policy.paidActivationMinMicros > 0 {
\t\t\t// Sum before rounding, just like the authoritative access snapshot.
\t\t\t// Floating division and single-argument ROUND work on all three DBs.
\t\t\thavingArgs := append(append([]interface{}{}, expressionArgs...), common.QuotaPerUnit, policy.paidActivationMinMicros)
\t\t\tpaid = paid.Group("top_ups.user_id").Having(
\t\t\t\t"ROUND(SUM("+expression+") * 1000000.0 / NULLIF(?, 0)) >= ?", havingArgs...)
\t\t}
\t\tordinaryL0 += " AND NOT EXISTS (?)"
\t\targs = append(args, paid)
\t}
\treturn query.Where("users.role < ?", common.RoleAdminUser).
\t\tWhere("(users.trust_level_override IS NOT NULL AND users.trust_level_override NOT BETWEEN ? AND ?) OR ("+ordinaryL0+")", args...)
}
''' + s[end:]
p.write_text(s)

change('model/checkin.go', '\thasCheckedToday, _ := HasCheckedInToday(userId)', '''\thasCheckedToday, err := HasCheckedInToday(userId)
\tif err != nil {
\t\treturn nil, err
\t}''')
change('model/checkin.go', '\tDB.Model(&Checkin{}).Where("user_id = ?", userId).Count(&totalCheckins)', '''\tif err := DB.Model(&Checkin{}).Where("user_id = ?", userId).Count(&totalCheckins).Error; err != nil {
\t\treturn nil, err
\t}''')
change('model/checkin.go', '\tDB.Model(&Checkin{}).Where("user_id = ?", userId).Select("COALESCE(SUM(quota_awarded), 0)").Scan(&totalQuota)', '''\tif err := DB.Model(&Checkin{}).Where("user_id = ?", userId).Select("COALESCE(SUM(quota_awarded), 0)").Scan(&totalQuota).Error; err != nil {
\t\treturn nil, err
\t}''')
change('controller/token.go', '\t\tcleanToken.Group = token.Group\n', '''\t\tcleanToken.Group = token.Group
\t\tif token.Group != "auto" && strings.TrimSpace(token.Group) != "" {
\t\t\tuserGroup, groupErr := getTokenRequestUserGroup(c)
\t\t\tif groupErr != nil {
\t\t\t\tcommon.ApiError(c, groupErr)
\t\t\t\treturn
\t\t\t}
\t\t\tif !service.IsUserSelectableGroup(userGroup, token.Group) {
\t\t\t\tcommon.ApiError(c, fmt.Errorf("the selected group is not available to this account"))
\t\t\t\treturn
\t\t\t}
\t\t}
''')
p = Path('CHANGELOG.md')
s = p.read_text()
marker = '<!-- Add user-facing or operational changes here before the next release. -->'
assert marker in s
p.write_text(s.replace(marker, marker + '''

- Fixed the Go new-user journey: L0 checkout, security-email verification and
  public script/game reads no longer disappear behind the console gate.
  Authentication, payment restrictions and developer/admin boundaries remain.
- Registration now commits its optional initial key and invitation counter with
  the account; a failed key insert no longer leaves a half-registered user.
  Payment contact emails no longer silently become account login emails.
- L0 administration filters now follow the configured paid-activation threshold;
  failed check-in statistics reads no longer return fabricated zero values.
  Key edits reject groups that are unavailable to the account.
''', 1))
files = ['CHANGELOG.md', 'apps/api-go/controller/token.go', 'apps/api-go/controller/user.go', 'apps/api-go/middleware/auth.go', 'apps/api-go/middleware/console_access_test.go', 'apps/api-go/model/checkin.go', 'apps/api-go/model/topup.go', 'apps/api-go/model/user.go']
subprocess.run(['gofmt', '-w', *[f for f in files if f.endswith('.go')]], check=True)
