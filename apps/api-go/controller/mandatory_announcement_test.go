package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/console_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupMandatoryAnnouncementTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupUserOnboardingTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AnnouncementRead{}, &model.Option{}))
	return db
}

func saveMandatoryAnnouncementOption(t *testing.T, db *gorm.DB, key, raw string) {
	t.Helper()
	require.NoError(t, db.Save(&model.Option{Key: key, Value: raw}).Error)
}

func TestMandatoryAnnouncementOrderRevisionAndAccountIsolation(t *testing.T) {
	db := setupMandatoryAnnouncementTestDB(t)
	items := []model.MandatoryAnnouncement{
		{ID: 2, Content: "Second", PublishDate: "2025-02-02T00:00:00Z", Mandatory: true},
		{ID: 1, Content: "First", PublishDate: "2025-02-01T00:00:00Z", Mandatory: true},
		{ID: 3, Content: "Future", PublishDate: "2099-01-01T00:00:00Z", Mandatory: true},
	}
	save := func() {
		raw, err := json.Marshal(items)
		require.NoError(t, err)
		saveMandatoryAnnouncementOption(t, db, "console_setting.announcements", string(raw))
	}
	save()
	u1, u2 := model.User{Username: "announcement-one", AffCode: "ann1"}, model.User{Username: "announcement-two", AffCode: "ann2"}
	require.NoError(t, db.Create(&u1).Error)
	require.NoError(t, db.Create(&u2).Error)
	status, err := model.GetAnnouncementStatus(u1.Id)
	require.NoError(t, err)
	require.Len(t, status, 2)
	require.Equal(t, int64(1), status[0].ID)
	require.ErrorIs(t, model.AcknowledgeAnnouncement(u1.Id, 2, status[1].Revision), model.ErrAnnouncementOrder)
	require.NoError(t, model.AcknowledgeAnnouncement(u1.Id, 1, status[0].Revision))
	require.NoError(t, model.AcknowledgeAnnouncement(u1.Id, 1, status[0].Revision))
	require.NoError(t, model.AcknowledgeAnnouncement(u1.Id, 2, status[1].Revision))
	var count int64
	require.NoError(t, db.Model(&model.AnnouncementRead{}).Count(&count).Error)
	require.Equal(t, int64(2), count)
	other, err := model.GetAnnouncementStatus(u2.Id)
	require.NoError(t, err)
	require.Zero(t, other[0].ReadAt)
	items[1].Content = "Corrected first announcement"
	save()
	updated, err := model.GetAnnouncementStatus(u1.Id)
	require.NoError(t, err)
	require.Zero(t, updated[0].ReadAt)
	require.Positive(t, updated[1].ReadAt)
	require.ErrorIs(t, model.AcknowledgeAnnouncement(u1.Id, 1, status[0].Revision), model.ErrAnnouncementOrder)
	require.NoError(t, model.AcknowledgeAnnouncement(u1.Id, 1, updated[0].Revision))
	require.NoError(t, db.Model(&model.AnnouncementRead{}).Count(&count).Error)
	require.Equal(t, int64(3), count)
}

// A pinned acknowledgement generation is the whole point of ackRevision: an
// administrator must be able to fix a typo without sending every reader who
// already confirmed back through the notice, and must still be able to ask
// for a fresh confirmation on purpose.
func TestMandatoryAnnouncementAckRevisionDecouplesContentEdits(t *testing.T) {
	db := setupMandatoryAnnouncementTestDB(t)
	items := []model.MandatoryAnnouncement{
		{ID: 1, Content: "Teh terms changed", PublishDate: "2025-03-01T00:00:00Z", Mandatory: true, AckRevision: "1"},
	}
	save := func() {
		raw, err := json.Marshal(items)
		require.NoError(t, err)
		saveMandatoryAnnouncementOption(t, db, "console_setting.announcements", string(raw))
	}
	save()
	user := model.User{Username: "announcement-pinned", AffCode: "annpin"}
	require.NoError(t, db.Create(&user).Error)

	status, err := model.GetAnnouncementStatus(user.Id)
	require.NoError(t, err)
	require.Len(t, status, 1)
	pinned := status[0].Revision
	require.NoError(t, model.AcknowledgeAnnouncement(user.Id, 1, pinned))

	items[0].Content = "The terms changed"
	items[0].Extra = "Typo fixed"
	save()
	edited, err := model.GetAnnouncementStatus(user.Id)
	require.NoError(t, err)
	require.Equal(t, pinned, edited[0].Revision)
	require.Positive(t, edited[0].ReadAt)

	items[0].AckRevision = "2"
	save()
	bumped, err := model.GetAnnouncementStatus(user.Id)
	require.NoError(t, err)
	require.NotEqual(t, pinned, bumped[0].Revision)
	require.Zero(t, bumped[0].ReadAt)
	require.NoError(t, model.AcknowledgeAnnouncement(user.Id, 1, bumped[0].Revision))

	var count int64
	require.NoError(t, db.Model(&model.AnnouncementRead{}).Count(&count).Error)
	require.Equal(t, int64(2), count)
}

// Notices written before the field existed must keep their content-derived
// revision, so upgrading the server does not re-prompt everyone.
func TestMandatoryAnnouncementWithoutAckRevisionKeepsContentRevision(t *testing.T) {
	db := setupMandatoryAnnouncementTestDB(t)
	saveMandatoryAnnouncementOption(t, db, "console_setting.announcements", `[{"id":1,"content":"Legacy notice","publishDate":"2025-03-01T00:00:00Z","mandatory":true}]`)
	user := model.User{Username: "announcement-legacy", AffCode: "annleg"}
	require.NoError(t, db.Create(&user).Error)

	status, err := model.GetAnnouncementStatus(user.Id)
	require.NoError(t, err)
	require.Len(t, status, 1)
	legacy := status[0].Revision

	saveMandatoryAnnouncementOption(t, db, "console_setting.announcements", `[{"id":1,"content":"Legacy notice","publishDate":"2025-03-01T00:00:00Z","mandatory":true,"ackRevision":""}]`)
	unchanged, err := model.GetAnnouncementStatus(user.Id)
	require.NoError(t, err)
	require.Equal(t, legacy, unchanged[0].Revision)
}

func TestMandatoryAnnouncementUsesDatabaseContentAcrossStaleNodeCaches(t *testing.T) {
	db := setupMandatoryAnnouncementTestDB(t)
	settings := console_setting.GetConsoleSetting()
	previous := settings.Announcements
	t.Cleanup(func() { settings.Announcements = previous })
	saveMandatoryAnnouncementOption(t, db, "console_setting.announcements", `[{"id":1,"content":"Persisted notice","publishDate":"2025-03-01T00:00:00Z","mandatory":true,"ackRevision":"current"}]`)
	user := model.User{Username: "announcement-current", AffCode: "anncur"}
	require.NoError(t, db.Create(&user).Error)

	settings.Announcements = `not valid JSON from a stale node`
	status, err := model.GetAnnouncementStatus(user.Id)
	require.NoError(t, err)
	require.Len(t, status, 1)
	require.Equal(t, "Persisted notice", status[0].Content)
	settings.Announcements = `[{"id":2,"content":"Other node's stale notice","publishDate":"2025-02-01T00:00:00Z","mandatory":true}]`
	acknowledged, err := model.AcknowledgeAnnouncementStatus(user.Id, status[0].ID, status[0].Revision)
	require.NoError(t, err)
	require.Len(t, acknowledged, 1)
	require.Equal(t, status[0].Revision, acknowledged[0].Revision)
	require.Positive(t, acknowledged[0].ReadAt)
	retried, err := model.AcknowledgeAnnouncementStatus(user.Id, status[0].ID, status[0].Revision)
	require.NoError(t, err)
	require.Equal(t, acknowledged, retried)
}

func TestMandatoryAnnouncementCanonicalLegacyAndDefaultAuthority(t *testing.T) {
	db := setupMandatoryAnnouncementTestDB(t)
	settings := console_setting.GetConsoleSetting()
	previous := settings.Announcements
	t.Cleanup(func() { settings.Announcements = previous })
	settings.Announcements = `[{"id":99,"content":"Unpersisted cache notice","publishDate":"2025-03-01T00:00:00Z","mandatory":true}]`
	user := model.User{Username: "announcement-default", AffCode: "anndef"}
	require.NoError(t, db.Create(&user).Error)
	status, err := model.GetAnnouncementStatus(user.Id)
	require.NoError(t, err)
	require.Empty(t, status)

	saveMandatoryAnnouncementOption(t, db, "Announcements", `[{"id":1,"content":"Legacy persisted notice","publishDate":"2025-03-01T00:00:00Z","mandatory":true}]`)
	legacy, err := model.GetAnnouncementStatus(user.Id)
	require.NoError(t, err)
	require.Len(t, legacy, 1)
	require.NoError(t, model.AcknowledgeAnnouncement(user.Id, legacy[0].ID, legacy[0].Revision))

	// An explicitly empty canonical option disables the legacy notice.
	saveMandatoryAnnouncementOption(t, db, "console_setting.announcements", "")
	status, err = model.GetAnnouncementStatus(user.Id)
	require.NoError(t, err)
	require.Empty(t, status)
}

func TestMandatoryAnnouncementConfirmationReturnsCommittedSnapshotWithoutAnotherRead(t *testing.T) {
	db := setupMandatoryAnnouncementTestDB(t)
	saveMandatoryAnnouncementOption(t, db, "console_setting.announcements", `[{"id":1,"content":"Confirm this notice","publishDate":"2025-03-01T00:00:00Z","mandatory":true}]`)
	user := model.User{Username: "announcement-receipt", AffCode: "annrec"}
	require.NoError(t, db.Create(&user).Error)
	status, err := model.GetAnnouncementStatus(user.Id)
	require.NoError(t, err)

	saved := false
	require.NoError(t, db.Callback().Create().After("gorm:create").Register("test:announcement-saved", func(tx *gorm.DB) {
		if tx.Statement.Table == "announcement_reads" && tx.Error == nil {
			saved = true
		}
	}))
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:announcement-after-save-read", func(tx *gorm.DB) {
		if saved {
			tx.AddError(errors.New("status read unavailable after receipt write"))
		}
	}))
	t.Cleanup(func() {
		_ = db.Callback().Create().Remove("test:announcement-saved")
		_ = db.Callback().Query().Remove("test:announcement-after-save-read")
	})
	body, err := json.Marshal(map[string]any{"id": status[0].ID, "revision": status[0].Revision})
	require.NoError(t, err)
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Set("id", user.Id)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/self/announcements/read", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	AcknowledgeSelfAnnouncement(c)
	require.Equal(t, http.StatusOK, response.Code)
	var result struct {
		Success bool                          `json:"success"`
		Data    []model.MandatoryAnnouncement `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.True(t, saved)
	require.True(t, result.Success, response.Body.String())
	require.Len(t, result.Data, 1)
	require.Positive(t, result.Data[0].ReadAt)
	require.Equal(t, status[0].Revision, result.Data[0].Revision)

	// Demonstrate that a follow-up status read really would fail here.
	_, err = model.GetAnnouncementStatus(user.Id)
	require.ErrorContains(t, err, "status read unavailable after receipt write")
	require.NoError(t, db.Callback().Query().Remove("test:announcement-after-save-read"))
	var receipt model.AnnouncementRead
	require.NoError(t, db.Where("user_id = ?", user.Id).First(&receipt).Error)
	require.Equal(t, result.Data[0].ReadAt, receipt.ReadAt)
}

func TestMandatoryAnnouncementWriteFailureDoesNotReturnAnAcknowledgedSnapshot(t *testing.T) {
	db := setupMandatoryAnnouncementTestDB(t)
	saveMandatoryAnnouncementOption(t, db, "console_setting.announcements", `[{"id":1,"content":"Confirm this notice","publishDate":"2025-03-01T00:00:00Z","mandatory":true}]`)
	user := model.User{Username: "announcement-failure", AffCode: "annfail"}
	require.NoError(t, db.Create(&user).Error)
	status, err := model.GetAnnouncementStatus(user.Id)
	require.NoError(t, err)
	require.NoError(t, db.Callback().Create().After("gorm:create").Register("test:announcement-write-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "announcement_reads" && tx.Error == nil {
			tx.AddError(errors.New("receipt transaction failed after write"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Create().Remove("test:announcement-write-failure") })
	snapshot, err := model.AcknowledgeAnnouncementStatus(user.Id, status[0].ID, status[0].Revision)
	require.ErrorContains(t, err, "receipt transaction failed after write")
	require.Nil(t, snapshot)
	var count int64
	require.NoError(t, db.Model(&model.AnnouncementRead{}).Count(&count).Error)
	require.Zero(t, count)
}
