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
