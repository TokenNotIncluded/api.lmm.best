package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPublicCreditBalancesKeepRawLedgerAndFixedUSD(t *testing.T) {
	db, user, _ := setupWalletMCPTest(t)
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(500000), decimal.NewFromInt(500000)))
	require.NoError(t, common.SetPublicCreditsPerUSD(decimal.NewFromInt(500000)))
	persistCreditDenominationFixture(t, db)
	require.NoError(t, db.Model(&user).Update("quota", 500000).Error)
	for _, p := range []string{"500000"} {
		require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.PublicCreditsPerUSDOptionKey).Update("value", p).Error)
		common.ClearPublicCreditsPerUSD()
		oauth, err := oauthBalancePayload(500000)
		require.NoError(t, err)
		require.Equal(t, float64(1), oauth["balance"])
		require.Equal(t, 500000, oauth["quota"])
		require.Equal(t, common.LedgerQuotaUnit, oauth["quota_unit"])
		require.Equal(t, p, oauth["public_credit_balance"])
		assistant := assistantWalletBalanceFields(500000)
		require.Equal(t, float64(1), assistant["wallet_balance_usd"])
		require.Equal(t, p, assistant["wallet_balance_public_credits"])
		gift, err := assistantGiftResponse(&model.AssistantNewUserGift{Quota: 500000})
		require.NoError(t, err)
		require.Equal(t, 500000, gift.CreditAmount)
		require.Equal(t, common.LedgerQuotaUnit, gift.CreditAmountUnit)
		require.Equal(t, p, gift.PublicCreditAmount)
		require.Equal(t, float64(1), *gift.AmountUSD)
		session := walletMCPTestSession(t, user.Id, walletMCPTestExtra("z"))
		balance := walletMCPData(t, walletMCPCall(t, session, "wallet.balance", map[string]any{}, ""))
		require.Equal(t, p, balance["public_available_credits"])
		require.Equal(t, common.LedgerQuotaUnit, balance["available_credits_unit"])
		require.Equal(t, common.LedgerQuotaUnit, balance["currency_unit"])
		require.Equal(t, common.PublicCreditUnit, balance["public_credit_unit"])
		require.EqualValues(t, 1, balance["available_usd"])
	}
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, 500000, stored.Quota)
}

func TestPublicCreditMCPLegacyInputAndBoundQuoteReplay(t *testing.T) {
	db, user, _ := setupWalletMCPTest(t)
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(500000), decimal.NewFromInt(500000)))
	require.NoError(t, common.SetPublicCreditsPerUSD(decimal.NewFromInt(500000)))
	persistCreditDenominationFixture(t, db)
	require.NoError(t, db.Model(&user).Update("quota", 1000000).Error)
	session := walletMCPTestSession(t, user.Id, walletMCPTestExtra("y"))
	args := map[string]any{"quota": 500000}
	pending := walletMCPCall(t, session, "wallet.transfer.create", args, "")
	require.True(t, pending.NeedsInput())
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.PublicCreditsPerUSDOptionKey).Update("value", "500000").Error)
	created := walletMCPData(t, walletMCPCall(t, session, "wallet.transfer.create", args, pending.RequestState))
	require.EqualValues(t, 500000, created["quota"], "the original confirmation keeps its exact wallet quantity")
	require.Equal(t, "500000", created["public_credit_amount"], "response credits preserve the confirmed wallet quantity")
	for i := 0; i < 3; i++ {
		replayed := walletMCPData(t, walletMCPCall(t, session, "wallet.transfer.create", args, pending.RequestState))
		require.Equal(t, created["share_url"], replayed["share_url"])
	}
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, 500000, stored.Quota)
	var count int64
	require.NoError(t, db.Model(&model.WalletTransfer{}).Count(&count).Error)
	require.EqualValues(t, 1, count)

}

func TestPublicCreditPricingSyncKeepsLedgerCalibrationAndRejectsPartialMetadata(t *testing.T) {
	version, k, p, q, scale := 2, float64(500000), float64(500000), float64(500000), float64(1)
	metadata := PricingSyncMetadata{SchemaVersion: &version, Currency: "USD", StorageBasis: model.PricingStorageLegacy, CreditsPerUSD: &k,
		LedgerQuotaPerUSD: &k, LedgerQuotaPerUSDExact: "500000", PublicCreditsPerUSD: &p, PublicCreditsPerUSDExact: "500000", CreditUnitSchemaVersion: &version,
		QuotaUnit: common.LedgerQuotaUnit, PublicCreditUnit: common.PublicCreditUnit, LegacyCreditUnit: common.LedgerQuotaUnit, ModelRatioUnit: "LEDGER_QUOTA_PER_TOKEN", QuotaPerUnit: &q, LegacyPricingUnitsPerUSD: &scale}
	data := map[string]any{"model_ratio": map[string]any{"fixture": 4.0316928}, "completion_ratio": map[string]any{"fixture": 5.0}, "model_price": map[string]any{"fixed": 2.0}, "billing_expr": map[string]any{"tiered": "p * 4"}}
	units, err := syncSourceUnits(metadata)
	require.NoError(t, err)
	require.Equal(t, k, units.creditsPerUSD)
	first, err := normalizeUpstreamSyncData(data, units, k)
	require.NoError(t, err)
	p = 200000
	metadata.PublicCreditsPerUSDExact = "200000"
	_, err = syncSourceUnits(metadata)
	require.Error(t, err, "mutable public denomination cannot change real model prices")
	p = 500000
	metadata.PublicCreditsPerUSDExact = "500000"
	for _, mutate := range []func(*PricingSyncMetadata){
		func(m *PricingSyncMetadata) { m.CreditUnitSchemaVersion = nil },
		func(m *PricingSyncMetadata) { m.QuotaUnit = "CREDIT" },
		func(m *PricingSyncMetadata) { m.PublicCreditUnit = "LEDGER_QUOTA" },
		func(m *PricingSyncMetadata) { m.LedgerQuotaPerUSDExact = "100000" },
		func(m *PricingSyncMetadata) { m.PublicCreditsPerUSDExact = "100000" },
		func(m *PricingSyncMetadata) { m.ModelRatioUnit = "CREDIT_PER_TOKEN" },
		func(m *PricingSyncMetadata) { m.LegacyCreditUnit = "CREDIT" },
		func(m *PricingSyncMetadata) { wrong := float64(100000); m.CreditsPerUSD = &wrong },
	} {
		bad := metadata
		mutate(&bad)
		_, err := syncSourceUnits(bad)
		require.Error(t, err)
	}
	encoded, err := json.Marshal(first)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "public_credits_per_usd")
}

func installPublicCreditBoundaryFixture(t *testing.T, ledger string, databases ...*gorm.DB) {
	t.Helper()
	oldLedger, oldErr := common.LedgerQuotaPerUSD()
	oldQ, _ := common.LegacyPricingQuotaPerUnit()
	oldPublic, publicErr := common.PublicCreditsPerUSD()
	if len(databases) == 0 {
		setupTokenControllerTestDB(t)
	}
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.RequireFromString(ledger), decimal.NewFromFloat(common.QuotaPerUnit)))
	require.NoError(t, common.SetPublicCreditsPerUSD(decimal.NewFromInt(500000)))
	persistCreditDenominationFixture(t, model.DB)
	t.Cleanup(func() {
		common.ClearPublicCreditsPerUSD()
		if oldErr != nil {
			common.ClearCreditsPerUSD()
			return
		}
		require.NoError(t, common.SetCreditCurrencyBasis(oldLedger, oldQ))
		if publicErr == nil && !oldPublic.Equal(oldLedger) {
			require.NoError(t, common.SetPublicCreditsPerUSD(oldPublic))
		}
	})
}

func publicCreditTestContext(t *testing.T, method, path, body string, id int) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	g := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(g)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", id)
	c.Set("role", common.RoleRootUser)
	return c, g
}

func publicCreditResponseData(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body), response.Body.String())
	require.True(t, body.Success, response.Body.String())
	return body.Data
}

func TestPublicCreditSelfAndUserListKeepLedgerValuesAndDollarValue(t *testing.T) {
	db := setupUserOnboardingTestDB(t)
	installPublicCreditBoundaryFixture(t, "500000", db)
	user := model.User{Username: "public-credit-self", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Quota: 3500000, UsedQuota: 7000000, Password: "private-password", Remark: "private-remark"}
	require.NoError(t, db.Create(&user).Error)
	c, w := publicCreditTestContext(t, http.MethodGet, "/api/user/self", "", user.Id)
	GetSelf(c)
	data := publicCreditResponseData(t, w)
	require.EqualValues(t, 3500000, data["quota"])
	require.EqualValues(t, 7000000, data["used_quota"])
	require.Equal(t, common.LedgerQuotaUnit, data["quota_unit"])
	require.Equal(t, "3500000", data["public_credit_balance"])
	require.Equal(t, "7000000", data["public_credit_used"])
	require.Equal(t, "500000", data["ledger_quota_per_usd_exact"])
	require.Equal(t, "500000", data["public_credits_per_usd_exact"])
	require.NotContains(t, w.Body.String(), "private-password")
	require.NotContains(t, w.Body.String(), "private-remark")
	response, err := buildPublicUserCreditResponses([]*model.User{&user})
	require.NoError(t, err)
	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(encoded, &rows))
	require.Equal(t, "3500000", rows[0]["public_credit_balance"])
	require.EqualValues(t, 3500000, rows[0]["quota"])
	require.Equal(t, common.LedgerQuotaUnit, rows[0]["quota_unit"])
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, 3500000, stored.Quota)
	require.Equal(t, 7000000, stored.UsedQuota)

	require.Error(t, common.SetPublicCreditsPerUSD(decimal.NewFromInt(777777)))
	require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", model.PublicCreditsPerUSDOptionKey).Update("value", "200000").Error)
	_, err = publicUserCreditFields(stored.Quota, stored.UsedQuota)
	require.Error(t, err, "invalid durable denomination cannot rescale raw wallet balances")
	require.Equal(t, 3500000, stored.Quota)
}

func TestPublicCreditBoundarySnapshotSurvivesRejectedSettingChange(t *testing.T) {
	installPublicCreditBoundaryFixture(t, "500000")
	basis, err := captureCreditBoundaryBasis()
	require.NoError(t, err)
	require.Error(t, common.SetPublicCreditsPerUSD(decimal.NewFromInt(777777)))
	require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", model.PublicCreditsPerUSDOptionKey).Update("value", "200000").Error)
	quota, err := basis.ledgerAmount(common.PublicCreditUnit, decimal.NewFromInt(1))
	require.NoError(t, err)
	require.Equal(t, 1, quota)
	actual, err := basis.publicAmount(int64(quota))
	require.NoError(t, err)
	require.Equal(t, "1", actual.String())
	fields := gin.H{}
	basis.addMetadata(fields)
	require.Equal(t, "500000", fields["public_credits_per_usd_exact"])
	_, err = basis.ledgerAmount(common.LedgerQuotaUnit, decimal.RequireFromString("33.5"))
	require.ErrorIs(t, err, model.ErrWalletTransferInvalid)
	_, err = captureCreditBoundaryBasis()
	require.Error(t, err, "a fresh boundary cannot use the corrupt durable value")
}

func TestWalletTransferExplicitCreditInputRejectsAmbiguousUnits(t *testing.T) {
	installPublicCreditBoundaryFixture(t, "500000")
	basis, err := captureCreditBoundaryBasis()
	require.NoError(t, err)
	cases := []string{
		`null`, `[]`, `{}`, `{"quota":null}`, `{"quota":"33"}`, `{"quota":33.5}`, `{"quota":0}`,
		`{"quota":33,"amount":null}`, `{"quota":null,"schema_version":2,"amount":"1","unit":"CREDIT"}`,
		`{"schema_version":null,"amount":"1","unit":"CREDIT"}`, `{"schema_version":"2","amount":"1","unit":"CREDIT"}`,
		`{"schema_version":1,"amount":"1","unit":"CREDIT"}`, `{"schema_version":2.0,"amount":"1","unit":"CREDIT"}`,
		`{"schema_version":2,"amount":"1"}`, `{"schema_version":2,"unit":"CREDIT"}`, `{"schema_version":2,"amount":1,"unit":"CREDIT"}`,
		`{"schema_version":2,"amount":"1","unit":"USD"}`, `{"schema_version":2,"amount":"1","unit":"CREDIT"}`,
		`{"schema_version":2,"amount":"0","unit":"LEDGER_QUOTA"}`, `{"schema_version":2,"amount":"-1","unit":"LEDGER_QUOTA"}`,
		`{"schema_version":2,"amount":"33.5","unit":"LEDGER_QUOTA"}`, `{"schema_version":2,"amount":"1e100000","unit":"LEDGER_QUOTA"}`,
		`{"schema_version":2,"amount":"9007199254740992","unit":"LEDGER_QUOTA"}`, `{"schema_version":2,"amount":"1","unit":"LEDGER_QUOTA","extra":true}`,
		`{"quota":1,"quota":2}`, `{"schema_version":2,"amount":"1","amount":"2","unit":"LEDGER_QUOTA"}`,
		`{"SCHEMA_VERSION":2,"AMOUNT":"1","UNIT":"LEDGER_QUOTA","extra":true}`,
		`{"SCHEMA_VERSION":2,"AMOUNT":"1","UNIT":"LEDGER_QUOTA"}`,
	}
	for _, amount := range []string{"0", "-0", "-1", "0.000000000000000001", "0.0000000000000000001", "1e100000", " 1", strings.Repeat("1", 129)} {
		encoded, err := json.Marshal(gin.H{"schema_version": 2, "amount": amount, "unit": "CREDIT", "expected_public_credits_per_usd_exact": "100000"})
		require.NoError(t, err)
		cases = append(cases, string(encoded))
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			var input walletTransferCreateInput
			err := json.Unmarshal([]byte(body), &input)
			if err == nil {
				_, err = input.ledgerQuota(basis)
			}
			require.Error(t, err)
		})
	}
	for _, body := range []string{`{"quota":33}`, `{"schema_version":2,"amount":"33","unit":"LEDGER_QUOTA"}`, `{"schema_version":2,"amount":"33","unit":"CREDIT","expected_public_credits_per_usd_exact":"500000"}`} {
		var input walletTransferCreateInput
		require.NoError(t, json.Unmarshal([]byte(body), &input))
		quota, err := input.ledgerQuota(basis)
		require.NoError(t, err)
		require.Equal(t, 33, quota)
	}
}

func TestWalletTransferPublicCreditHTTPPreservesLedgerIdempotencyAndRejectsStaleBasis(t *testing.T) {
	db := setupUserOnboardingTestDB(t)
	assertWalletTransferPublicCreditHTTP(t, db)
}

func TestWalletTransferPublicCreditPostgres(t *testing.T) {
	harness := openAssistantKeyPostgresHarness(t)
	assertWalletTransferPublicCreditHTTP(t, harness.db)
}

func assertWalletTransferPublicCreditHTTP(t *testing.T, db *gorm.DB) {
	t.Helper()
	installPublicCreditBoundaryFixture(t, "500000", db)
	require.NoError(t, db.AutoMigrate(&model.WalletTransfer{}))
	user := model.User{Username: "credit-transfer-sender", AffCode: "credit-transfer-sender-aff", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 10000}
	require.NoError(t, db.Create(&user).Error)
	request := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		c, w := publicCreditTestContext(t, http.MethodPost, "/api/wallet-transfer", body, user.Id)
		CreateWalletTransfer(c)
		return w
	}
	body := `{"schema_version":2,"amount":"1","unit":"CREDIT","expected_public_credits_per_usd_exact":"500000","request_key":"public-transfer-request-0001"}`
	first := publicCreditResponseData(t, request(body))
	require.EqualValues(t, 1, first["quota"])
	require.Equal(t, common.LedgerQuotaUnit, first["quota_unit"])
	actual := decimal.RequireFromString(first["public_credit_amount"].(string))
	require.True(t, actual.IsPositive())
	require.Equal(t, "1", actual.String())
	second := publicCreditResponseData(t, request(body))
	require.Equal(t, first["id"], second["id"])
	require.Equal(t, first["token"], second["token"])
	legacyReplay := publicCreditResponseData(t, request(`{"quota":1,"request_key":"public-transfer-request-0001"}`))
	require.Equal(t, first["id"], legacyReplay["id"])
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, 9999, stored.Quota)
	conflicting := request(`{"quota":34,"request_key":"public-transfer-request-0001"}`)
	require.Contains(t, conflicting.Body.String(), `"success":false`)
	trailing := request(body + `{}`)
	require.Contains(t, trailing.Body.String(), `"success":false`)
	require.Error(t, common.SetPublicCreditsPerUSD(decimal.NewFromInt(777777)))
	require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", model.PublicCreditsPerUSDOptionKey).Update("value", "200000").Error)
	stale := request(body)
	require.Equal(t, http.StatusServiceUnavailable, stale.Code)
	newBasisConflict := request(strings.Replace(body, `"500000"`, `"200000"`, 1))
	require.Contains(t, newBasisConflict.Body.String(), `"success":false`)
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, 9999, stored.Quota)
	var count int64
	require.NoError(t, db.Model(&model.WalletTransfer{}).Count(&count).Error)
	require.EqualValues(t, 1, count)

	// Restore the fixed durable contract before another transfer and claim.
	require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", model.PublicCreditsPerUSDOptionKey).Update("value", "500000").Error)
	copyBody := `{"schema_version":2,"amount":"33","unit":"LEDGER_QUOTA","request_key":"public-transfer-request-0002"}`
	copyReceipt := publicCreditResponseData(t, request(copyBody))
	require.EqualValues(t, 33, copyReceipt["quota"])
	require.Equal(t, "500000", copyReceipt["public_credits_per_usd_exact"])

	recipient := model.User{Username: "credit-transfer-recipient", AffCode: "credit-transfer-recipient-aff", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&recipient).Error)
	claim := func() map[string]any {
		t.Helper()
		encoded, err := json.Marshal(gin.H{"token": first["token"]})
		require.NoError(t, err)
		c, w := publicCreditTestContext(t, http.MethodPost, "/api/wallet-transfer/claim", string(encoded), recipient.Id)
		ClaimWalletTransfer(c)
		return publicCreditResponseData(t, w)
	}
	claimed := claim()
	require.EqualValues(t, 1, claimed["quota"])
	require.Equal(t, "500000", claimed["public_credits_per_usd_exact"])
	require.Equal(t, common.LedgerQuotaUnit, claimed["quota_unit"])
	for _, private := range []string{"token", "sender_id", "recipient_id", "recipient_email", "recipient_username"} {
		require.NotContains(t, claimed, private)
	}
	require.Equal(t, claimed["public_credit_amount"], claim()["public_credit_amount"])
	require.NoError(t, db.First(&recipient, recipient.Id).Error)
	require.Equal(t, 1, recipient.Quota)
}

func TestPublicCreditBoundaryMaximumAndDisplayRoundtripAreExplicit(t *testing.T) {
	installPublicCreditBoundaryFixture(t, "500000")
	basis, err := captureCreditBoundaryBasis()
	require.NoError(t, err)
	max := decimal.NewFromInt(common.MaxWalletQuota)
	quota, err := basis.ledgerAmount(common.PublicCreditUnit, max.Add(decimal.RequireFromString("0.9")))
	require.NoError(t, err)
	require.Equal(t, common.MaxWalletQuota, quota)
	_, err = basis.ledgerAmount(common.PublicCreditUnit, max.Add(decimal.NewFromInt(1)))
	require.Error(t, err)
	_, err = basis.ledgerAmount(common.LedgerQuotaUnit, max.Add(decimal.RequireFromString("0.9")))
	require.Error(t, err)
	display, err := basis.publicAmount(2)
	require.NoError(t, err)
	quota, err = basis.ledgerAmount(common.PublicCreditUnit, display)
	require.NoError(t, err)
	require.Equal(t, 2, quota, "display is the exact wallet integer")
}

func TestPublicCreditTokenStatusSeparatesCompatibilityQuotaAndPublicSummary(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	installPublicCreditBoundaryFixture(t, "500000", db)
	user := model.User{Username: "public-token-owner", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "public-credit-status-test-key", Status: common.TokenStatusEnabled, RemainQuota: 3500000, UsedQuota: 1750000, ExpiredTime: -1}
	require.NoError(t, db.Create(&token).Error)
	c, w := publicCreditTestContext(t, http.MethodGet, "/dashboard/billing/credit_grants", "", user.Id)
	c.Set("token_id", token.Id)
	GetTokenStatus(c)
	var data map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &data))
	require.EqualValues(t, 3500000, data["total_available"])
	require.EqualValues(t, 0, data["total_used"])
	require.Equal(t, common.LedgerQuotaUnit, data["unit"])
	summary := data["credit_summary"].(map[string]any)
	require.Equal(t, common.PublicCreditUnit, summary["unit"])
	require.Equal(t, "3500000", summary["total_available"])
	require.Equal(t, "1750000", summary["total_used"])
	require.Equal(t, "5250000", summary["total_granted"])
	require.EqualValues(t, 0, data["expires_at"])
	var stored model.Token
	require.NoError(t, db.First(&stored, token.Id).Error)
	require.Equal(t, token.RemainQuota, stored.RemainQuota)
	require.Equal(t, token.UsedQuota, stored.UsedQuota)
}

func TestPublicCreditToolConfigDoesNotRelabelLegacyPricingCalibration(t *testing.T) {
	db := setupUserOnboardingTestDB(t)
	installPublicCreditBoundaryFixture(t, "500000", db)
	require.NoError(t, db.AutoMigrate(&model.ToolMarketConfig{}))
	c, w := publicCreditTestContext(t, http.MethodGet, "/api/tool-market/config", "", 1)
	GetToolMarketConfig(c)
	data := publicCreditResponseData(t, w)
	require.Equal(t, "500000", data["credits_per_usd"])
	require.Equal(t, "500000", data["ledger_quota_per_usd_exact"])
	require.Equal(t, "500000", data["public_credits_per_usd_exact"])
	require.Equal(t, common.LedgerQuotaUnit, data["quota_unit"])
}

func TestPublicCreditBoundaryReadsDurableDenominationOrFailsClosed(t *testing.T) {
	installPublicCreditBoundaryFixture(t, "500000")
	common.ClearPublicCreditsPerUSD()
	basis, err := captureCreditBoundaryBasis()
	require.NoError(t, err)
	require.Equal(t, "500000", basis.Metadata.PublicCreditsPerUSDExact)
	public, err := basis.publicAmount(500000)
	require.NoError(t, err)
	require.Equal(t, "500000", public.String())
	for _, invalid := range []string{"200000", "0"} {
		require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", model.PublicCreditsPerUSDOptionKey).Update("value", invalid).Error)
		unavailable, err := captureCreditBoundaryBasis()
		require.Error(t, err, "invalid durable configuration cannot fall back to an old node cache")
		require.Equal(t, creditBoundaryBasis{}, unavailable)
	}
}
