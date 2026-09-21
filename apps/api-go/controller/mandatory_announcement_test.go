package controller

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/console_setting"
	"github.com/stretchr/testify/require"
)

func TestMandatoryAnnouncementOrderRevisionAndAccountIsolation(t *testing.T) {
	db := setupUserOnboardingTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AnnouncementRead{}))
	settings := console_setting.GetConsoleSetting()
	previous := settings.Announcements
	t.Cleanup(func() { settings.Announcements = previous })
	items := []model.MandatoryAnnouncement{
		{ID: 2, Content: "Second", PublishDate: "2025-02-02T00:00:00Z", Mandatory: true},
		{ID: 1, Content: "First", PublishDate: "2025-02-01T00:00:00Z", Mandatory: true},
		{ID: 3, Content: "Future", PublishDate: "2099-01-01T00:00:00Z", Mandatory: true},
	}
	save := func() { raw, err := json.Marshal(items); require.NoError(t, err); settings.Announcements = string(raw) }
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
	db := setupUserOnboardingTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AnnouncementRead{}))
	settings := console_setting.GetConsoleSetting()
	previous := settings.Announcements
	t.Cleanup(func() { settings.Announcements = previous })
	items := []model.MandatoryAnnouncement{
		{ID: 1, Content: "Teh terms changed", PublishDate: "2025-03-01T00:00:00Z", Mandatory: true, AckRevision: "1"},
	}
	save := func() { raw, err := json.Marshal(items); require.NoError(t, err); settings.Announcements = string(raw) }
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
	db := setupUserOnboardingTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AnnouncementRead{}))
	settings := console_setting.GetConsoleSetting()
	previous := settings.Announcements
	t.Cleanup(func() { settings.Announcements = previous })
	settings.Announcements = `[{"id":1,"content":"Legacy notice","publishDate":"2025-03-01T00:00:00Z","mandatory":true}]`
	user := model.User{Username: "announcement-legacy", AffCode: "annleg"}
	require.NoError(t, db.Create(&user).Error)

	status, err := model.GetAnnouncementStatus(user.Id)
	require.NoError(t, err)
	require.Len(t, status, 1)
	legacy := status[0].Revision

	settings.Announcements = `[{"id":1,"content":"Legacy notice","publishDate":"2025-03-01T00:00:00Z","mandatory":true,"ackRevision":""}]`
	unchanged, err := model.GetAnnouncementStatus(user.Id)
	require.NoError(t, err)
	require.Equal(t, legacy, unchanged[0].Revision)
}
