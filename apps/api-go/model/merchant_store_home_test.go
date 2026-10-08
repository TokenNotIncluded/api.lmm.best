package model

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreHomePersistsSafeAnimatedSVGHeader(t *testing.T) {
	f := newStoreFixture(t, "balance")
	animated := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 40"><rect width="100" height="40" fill="blue"><animate attributeName="opacity" values="0.4;1;0.4" dur="3s" repeatCount="indefinite"/></rect></svg>`
	home, err := SaveMerchantStoreHome(f.seller.Id, MerchantStoreHomeInput{HeaderImage: animated})
	require.NoError(t, err)
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(home.HeaderImage, MerchantStoreSVGDataPrefix))
	require.NoError(t, err)
	require.Contains(t, string(decoded), `<animate attributeName="opacity"`)
	public, err := GetMerchantStoreHome(0, f.seller.Id)
	require.NoError(t, err)
	require.Equal(t, home.HeaderImage, public.HeaderImage)
	unsafe := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 40"><rect width="100" height="40"><animate attributeName="href" to="javascript:alert(1)" dur="3s"/></rect></svg>`
	_, err = SaveMerchantStoreHome(f.seller.Id, MerchantStoreHomeInput{HeaderImage: unsafe, ExpectedVersion: home.Version})
	require.ErrorIs(t, err, ErrMerchantStoreInput)
}

func TestMerchantStoreHomePreservesPrivateSettingsAndRejectsStaleEdits(t *testing.T) {
	f := newStoreFixture(t, "balance")
	original := `{"language":"zh","wallet_display_currency":"CNY","webhook_secret":"PRIVATE_WEBHOOK","future_setting":{"private":"unchanged"}}`
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("setting", original).Error)
	home, err := SaveMerchantStoreHome(f.seller.Id, MerchantStoreHomeInput{Biography: "  **Actual merchant**  ", Announcement: "New delivery instructions", HeaderImage: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10" fill="red"/></svg>`})
	require.NoError(t, err)
	require.Equal(t, 1, home.Version)
	require.Equal(t, "**Actual merchant**", home.Biography)
	require.True(t, strings.HasPrefix(home.HeaderImage, MerchantStoreSVGDataPrefix))
	var user User
	require.NoError(t, DB.First(&user, f.seller.Id).Error)
	var values map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(user.Setting), &values))
	require.JSONEq(t, `"PRIVATE_WEBHOOK"`, string(values["webhook_secret"]))
	require.JSONEq(t, `{"private":"unchanged"}`, string(values["future_setting"]))
	require.ErrorIs(t, func() error {
		_, err := SaveMerchantStoreHome(f.seller.Id, MerchantStoreHomeInput{Biography: "Stale overwrite"})
		return err
	}(), ErrMerchantStoreConflict)
	current, err := GetMerchantStoreHome(0, f.seller.Id)
	require.NoError(t, err)
	encoded, err := json.Marshal(current)
	require.NoError(t, err)
	for _, private := range []string{"PRIVATE_WEBHOOK", "future_setting", "wallet_display_currency", "setting", "access_token", "quota"} {
		require.NotContains(t, string(encoded), private)
	}
	// Other preference forms must not erase the merchant page.
	require.NoError(t, UpdateUserSettingPreservingLocale(f.seller.Id, dto.UserSetting{NotifyType: "email"}))
	current, err = GetMerchantStoreHome(0, f.seller.Id)
	require.NoError(t, err)
	require.Equal(t, home.MerchantStoreHome, current.MerchantStoreHome)
}

func TestMerchantStoreHomeSurvivesStaleAccountProfileAndEmailUpdates(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("setting", `{"language":"zh","wallet_display_currency":"CNY"}`).Error)
	stale, err := GetUserById(f.seller.Id, true)
	require.NoError(t, err)
	home, err := SaveMerchantStoreHome(f.seller.Id, MerchantStoreHomeInput{Biography: "Fresh merchant biography"})
	require.NoError(t, err)
	stale.DisplayName = "Updated account profile"
	require.NoError(t, stale.Update(false))
	current, err := GetMerchantStoreHome(0, f.seller.Id)
	require.NoError(t, err)
	require.Equal(t, home.MerchantStoreHome, current.MerchantStoreHome)
	stale, err = GetUserById(f.seller.Id, true)
	require.NoError(t, err)
	home, err = SaveMerchantStoreHome(f.seller.Id, MerchantStoreHomeInput{Biography: "Newer merchant biography", ExpectedVersion: home.Version})
	require.NoError(t, err)
	require.NoError(t, BindEmailToUser(stale, "new-home-email@example.test"))
	current, err = GetMerchantStoreHome(0, f.seller.Id)
	require.NoError(t, err)
	require.Equal(t, home.MerchantStoreHome, current.MerchantStoreHome)
	require.Equal(t, "new-home-email@example.test", stale.Email)
}

func TestMerchantStoreHomeCharacterBoundsAllowChineseCopyWithinEditorLimits(t *testing.T) {
	f := newStoreFixture(t, "balance")
	biography := strings.Repeat("汉", 1366)
	announcement := strings.Repeat("公告", 4000)
	home, err := SaveMerchantStoreHome(f.seller.Id, MerchantStoreHomeInput{Biography: biography, Announcement: announcement})
	require.NoError(t, err)
	require.Equal(t, biography, home.Biography)
	require.Equal(t, announcement, home.Announcement)
	require.NoError(t, SaveMerchantStoreAnnouncement(f.root.Id, announcement))
	content, err := GetMerchantStoreAnnouncement()
	require.NoError(t, err)
	require.Equal(t, announcement, content)
	_, err = SaveMerchantStoreHome(f.seller.Id, MerchantStoreHomeInput{Biography: strings.Repeat("汉", 4097)})
	require.ErrorIs(t, err, ErrMerchantStoreInput)
}

func TestMerchantStoreHomePublicContactNeverUsesHiddenProductsOrAccountEmail(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("email", "PRIVATE-ACCOUNT@example.test").Error)
	require.NoError(t, DB.Model(f.product).Updates(map[string]any{"contact": "public-sales@example.test", "created_at": 1}).Error)
	hidden := *f.product
	hidden.ID, hidden.Contact, hidden.TestMode, hidden.CreatedAt = "hidden-home-profile", "SECRET-CONTACT@example.test", true, 100
	require.NoError(t, DB.Create(&hidden).Error)
	home, err := GetMerchantStoreHome(0, f.seller.Id)
	require.NoError(t, err)
	require.Equal(t, "public-sales@example.test", home.Seller.ContactEmail)
	require.Equal(t, storeSellerAvatar("PRIVATE-ACCOUNT@example.test"), home.Seller.AvatarURL)
	require.Contains(t, home.Seller.AvatarURL, "https://gravatar.com/avatar/")
	require.NotContains(t, home.Seller.AvatarURL, "PRIVATE-ACCOUNT")
	// Empty catalogues still have a merchant home, with no hidden sales contact.
	require.NoError(t, DB.Model(f.product).Update("status", "off_shelf").Error)
	home, err = GetMerchantStoreHome(0, f.seller.Id)
	require.NoError(t, err)
	require.Empty(t, home.Seller.ContactEmail)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("status", common.UserStatusDisabled).Error)
	_, err = GetMerchantStoreHome(0, f.seller.Id)
	require.Error(t, err)
}

func TestMerchantStoreHomeBoundsAndAnnouncementRequireCurrentAdministrator(t *testing.T) {
	f := newStoreFixture(t, "balance")
	for _, input := range []MerchantStoreHomeInput{{Biography: strings.Repeat("a", 4097)}, {Announcement: strings.Repeat("b", 16385)}, {HeaderImage: "http://example.test/banner.png"}, {HeaderImage: `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`}, {ExpectedVersion: -1}} {
		_, err := SaveMerchantStoreHome(f.seller.Id, input)
		require.ErrorIs(t, err, ErrMerchantStoreInput)
	}
	require.ErrorIs(t, SaveMerchantStoreAnnouncement(f.seller.Id, "Forbidden"), ErrMerchantStoreDenied)
	admin := marketTestUser(t, DB, "home-admin", 0, common.RoleAdminUser)
	require.NoError(t, SaveMerchantStoreAnnouncement(admin.Id, "  ## Store news  "))
	content, err := GetMerchantStoreAnnouncement()
	require.NoError(t, err)
	require.Equal(t, "## Store news", content)
	require.NoError(t, DB.Model(&admin).Update("role", common.RoleCommonUser).Error)
	require.ErrorIs(t, SaveMerchantStoreAnnouncement(admin.Id, "Demoted overwrite"), ErrMerchantStoreDenied)
	require.ErrorIs(t, SaveMerchantStoreAnnouncement(f.root.Id, strings.Repeat("x", 16385)), ErrMerchantStoreInput)
	require.NoError(t, SaveMerchantStoreAnnouncement(f.root.Id, ""))
	content, err = GetMerchantStoreAnnouncement()
	require.NoError(t, err)
	require.Empty(t, content)
}
