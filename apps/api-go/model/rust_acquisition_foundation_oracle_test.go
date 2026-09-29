package model

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRustAcquisitionFoundationCurrentGoOracle(t *testing.T) {
	output := os.Getenv("LMM_ACQUISITION_GO_ORACLE_OUTPUT")
	if output == "" {
		t.Skip("explicit current Go acquisition export not selected")
	}
	db := acquisitionDB(t)
	ctx := context.Background()
	epoch := time.Now().Unix()
	rawBase := `{"consent":true,"consent_version":2,"nonce":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","landing":"/guide?api_key=private#fragment","referrer":"https://GitHub.com/project?token=private","source":"community","campaign":"readme"}`
	raws := []string{rawBase, `null`, `{}`, `{"consent":true,"consent":null}`, rawBase + ` {"trailing":true}`}
	for _, pair := range [][2]string{{"landing", "//example.com/guide"}, {"landing", "/%67uide"}, {"landing", "/guide?code=private"}, {"landing", "/guide?code=%ZZ"}, {"landing", "/guide?x=ok;code=private"}, {"landing", "/oauth/callback?code=private"}, {"landing", "/guide#%ZZ"}, {"landing", "/guide?state"}, {"landing", "/pricing?token=private"}, {"landing", "/sign-in"}, {"landing", "/return"}, {"landing", "https://example.com/guide"}, {"referrer", "https://api.lmm.best/guide?key=private"}, {"referrer", "https://sub.lmm.best/path"}, {"referrer", "http://127.0.0.1/path"}, {"referrer", "https://host.local/path"}, {"referrer", "https://example.com/path with spaces"}, {"referrer", "https://user:password@example.com/path"}, {"referrer", "HTTPS://Example.COM:8443/a"}, {"referrer", "https://example.com/a%ZZ"}, {"referrer", "https://example.com/?code=%ZZ"}, {"source", "sk-secret"}, {"source", "a.b.c"}, {"source", strings.Repeat("a", 41)}, {"source", "社区教程"}, {"nonce", "bad"}, {"nonce", strings.Repeat("A", 32)}} {
		var v map[string]any
		require.NoError(t, json.Unmarshal([]byte(rawBase), &v))
		v[pair[0]] = pair[1]
		b, _ := json.Marshal(v)
		raws = append(raws, string(b))
	}
	raws = append(raws, strings.Replace(rawBase, `"consent_version":2`, `"consent_version":3`, 1), strings.Replace(rawBase, `"consent":true`, `"CONSENT":true,"consent":null`, 1), strings.Replace(rawBase, `"source":"community"`, `"source":"community","source":null`, 1), strings.Replace(rawBase, `"consent_version":2`, `"consent_version":2.0`, 1), strings.Replace(rawBase, `"source":"community"`, `"source":7`, 1))
	vectors := []any{}
	for _, raw := range raws {
		var input AcquisitionInput
		err := json.NewDecoder(strings.NewReader(raw)).Decode(&input)
		var visit AcquisitionVisit
		if err == nil {
			visit, err = NormalizeAcquisition(input, []string{"api.lmm.best", "lmm.best"}, 123)
		}
		item := map[string]any{"raw": raw, "valid": err == nil}
		if err == nil {
			item["visit"] = visit
		}
		vectors = append(vectors, item)
	}
	labels := []any{}
	for _, value := range []string{" community ", "hello_world", "中文来源", "x.y", "x.y.z", "bearer demo", "sk-demo", strings.Repeat("a", 41), strings.Repeat("界", 80), strings.Repeat("界", 81), "a\nb", "1_2-3", "ſource"} {
		labels = append(labels, map[string]string{"input": value, "output": AcquisitionLabel(value)})
	}
	require.NoError(t, db.Create(&User{Id: 7, Username: "acquisition-seven", AffCode: "acquisition-seven", CreatedAt: epoch + 86400}).Error)
	require.NoError(t, db.Create(&User{Id: 8, Username: "acquisition-eight", AffCode: "acquisition-eight", CreatedAt: epoch - 86400}).Error)
	require.NoError(t, db.Create(&AcquisitionConfig{ID: 1, StartedAt: epoch, LookbackDays: 30}).Error)
	link, err := SaveAcquisitionLink(ctx, AcquisitionLink{Name: " Original ", Source: "community", Medium: "post", Campaign: "readme", Target: "/guide"})
	require.NoError(t, err)
	clean := func(value any) any {
		encoded, _ := json.Marshal(value)
		var result any
		require.NoError(t, json.Unmarshal(encoded, &result))
		var walk func(any)
		walk = func(v any) {
			switch v := v.(type) {
			case map[string]any:
				for key, x := range v {
					if key == "created_at" || key == "updated_at" || key == "first_observed_at" {
						v[key] = float64(0)
					} else if (key == "id" || key == "link_id" || key == "registration_link_id") && x == link.ID {
						v[key] = "<link>"
					} else {
						walk(x)
					}
				}
			case []any:
				for _, x := range v {
					walk(x)
				}
			}
		}
		walk(result)
		return result
	}
	workflow := map[string]any{"created_link": clean(link)}
	renamed, err := SaveAcquisitionLink(ctx, AcquisitionLink{ID: link.ID, Name: " Renamed ", Source: "ignored", Target: "/pricing", Archived: true})
	require.NoError(t, err)
	workflow["renamed_link"] = clean(renamed)
	preview, err := PreviewAcquisitionLink(ctx, link.ID)
	require.NoError(t, err)
	workflow["preview"] = clean(preview)
	hash := AcquisitionVisitorHash(strings.Repeat("b", 32))
	input := AcquisitionInput{Consent: true, ConsentVersion: 2, Nonce: strings.Repeat("a", 32), Landing: "/guide?api_key=private", LinkID: link.ID}
	visit, err := ObserveAcquisition(ctx, hash, 0, input, []string{"lmm.best"})
	require.NoError(t, err)
	workflow["visit"] = clean(visit)
	replay, err := ObserveAcquisition(ctx, hash, 0, input, nil)
	require.NoError(t, err)
	workflow["replay"] = clean(replay)
	require.NoError(t, AttributeAcquisitionRegistration(ctx, 7, hash))
	require.NoError(t, AttributeAcquisitionRegistration(ctx, 7, hash))
	var account AcquisitionAccount
	require.NoError(t, db.First(&account, "user_id = ?", 7).Error)
	workflow["attribution"] = clean(account)
	require.NoError(t, SetAcquisitionLookback(ctx, 7))
	require.NoError(t, SetAcquisitionLookback(ctx, 7))
	require.NoError(t, db.First(&account, "user_id = ?", 7).Error)
	workflow["after_policy"] = clean(account)
	var policies int64
	require.NoError(t, db.Model(&AcquisitionAttributionPolicy{}).Count(&policies).Error)
	workflow["policy_count"] = policies
	require.NoError(t, GrantAcquisitionConsent(ctx, 8))
	var historical AcquisitionAccount
	require.NoError(t, db.First(&historical, "user_id = ?", 8).Error)
	workflow["historical_source"] = historical.RegistrationSource
	_, err = ObserveAcquisition(ctx, hash, 8, input, nil)
	workflow["cross_owner_rejected"] = err != nil
	require.NoError(t, SaveAcquisitionSelfReport(ctx, 7, "community", " A useful tutorial "))
	report, err := ReadAcquisitionSelfReport(ctx, 7)
	require.NoError(t, err)
	workflow["self_report"] = clean(report)
	require.NoError(t, db.Create(&AcquisitionActivity{UserID: 7, Day: 123}).Error)
	require.NoError(t, db.Create(&AcquisitionCorrection{UserID: 7, Source: "manual"}).Error)
	require.NoError(t, db.Create(&AcquisitionFirstPayment{UserID: 7, Source: "community"}).Error)
	require.NoError(t, RevokeAcquisitionVisitor(ctx, hash))
	require.NoError(t, RevokeAcquisitionAccount(ctx, 7))
	require.NoError(t, RevokeAcquisitionVisitor(ctx, hash))
	counts := map[string]int64{}
	for _, table := range []string{"acquisition_accounts", "acquisition_activities", "acquisition_corrections", "acquisition_correction_heads", "acquisition_first_payments", "acquisition_self_reports"} {
		var count int64
		require.NoError(t, db.Table(table).Where("user_id = ?", 7).Count(&count).Error)
		counts[table] = count
	}
	workflow["after_withdrawal"] = counts
	_, err = ObserveAcquisition(ctx, hash, 7, input, nil)
	workflow["stale_consent_rejected"] = err != nil
	require.NoError(t, AttributeAcquisitionRegistration(ctx, 7, hash))
	var remaining int64
	require.NoError(t, db.Model(&AcquisitionAccount{}).Where("user_id = ?", 7).Count(&remaining).Error)
	workflow["registration_cannot_undo_withdrawal"] = remaining == 0
	_, err = DeleteAcquisitionLink(ctx, link.ID)
	require.NoError(t, err)
	_, err = PreviewAcquisitionLink(ctx, link.ID)
	workflow["deleted_preview_rejected"] = err != nil
	var tombstones int64
	require.NoError(t, db.Model(&AcquisitionLink{}).Where("id = ? AND deleted_at > 0", link.ID).Count(&tombstones).Error)
	workflow["retained_link_tombstone"] = tombstones
	encoded, err := json.MarshalIndent(map[string]any{"epoch": epoch, "vectors": vectors, "labels": labels, "workflow": workflow}, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(output, append(encoded, '\n'), 0600))
}
