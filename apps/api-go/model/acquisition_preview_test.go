package model

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestAcquisitionLinkPreviewDoesNotRecordVisitsOrConsent(t *testing.T) {
	db := acquisitionDB(t)
	link, err := SaveAcquisitionLink(context.Background(), AcquisitionLink{Name: "Readme", Source: "github", Campaign: "launch", Target: "/guide"})
	require.NoError(t, err)
	preview, err := PreviewAcquisitionLink(context.Background(), link.ID)
	require.NoError(t, err)
	require.True(t, preview.Excluded)
	require.Equal(t, "/guide", preview.Target)
	require.Equal(t, "promotion_link", preview.Evidence)
	require.Equal(t, "github", preview.Source)
	require.Empty(t, preview.ReferrerHost)
	for _, table := range []any{&AcquisitionVisit{}, &AcquisitionVisitor{}, &AcquisitionAccount{}, &AcquisitionConsent{}, &AcquisitionConfig{}} {
		var count int64
		require.NoError(t, db.Model(table).Count(&count).Error)
		require.Zero(t, count)
	}
	raw, err := json.Marshal(preview)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "nonce")
	observation, err := ObserveAcquisition(context.Background(), AcquisitionVisitorHash("preview-parity"), 0, AcquisitionInput{Consent: true, Nonce: strings.Repeat("a", 32), Landing: "/guide", LinkID: link.ID, Source: "tampered", Referrer: "https://forum.example/post?token=private"}, nil)
	require.NoError(t, err)
	require.Equal(t, preview.Source, observation.Source)
	require.Equal(t, preview.Evidence, observation.Evidence)
	require.Equal(t, "forum.example", observation.ReferrerHost)
	require.Empty(t, preview.ReferrerHost)

	require.NoError(t, db.Model(&link).Update("target", "https://evil.example/steal").Error)
	_, err = PreviewAcquisitionLink(context.Background(), link.ID)
	require.ErrorIs(t, err, ErrAcquisitionInvalid)
	_, err = PreviewAcquisitionLink(context.Background(), "invalid")
	require.ErrorIs(t, err, ErrAcquisitionInvalid)
}
