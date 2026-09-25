/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
package model

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRSSFeeds(t *testing.T) {
	valid := `[{"id":"openai","name":"OpenAI","url":"https://example.com/feed.xml","enabled":true}]`
	require.NoError(t, ValidateRSSFeeds(valid))
	require.NoError(t, ValidateRSSFeeds("[]"))
	require.NoError(t, ValidateRSSFeeds(""))

	for name, value := range map[string]string{
		"object":       `{"feeds":[]}`,
		"null":         `null`,
		"credentials":  `[{"id":"x","name":"X","url":"https://user:pass@example.com/feed","enabled":true}]`,
		"scheme":       `[{"id":"x","name":"X","url":"file:///tmp/feed","enabled":true}]`,
		"duplicate id": `[{"id":"x","name":"A","url":"https://a.example/feed","enabled":true},{"id":"x","name":"B","url":"https://b.example/feed","enabled":true}]`,
		"duplicate url": `[{"id":"a","name":"A","url":"https://a.example/feed","enabled":true},{"id":"b","name":"B","url":"https://a.example/feed","enabled":true}]`,
		"missing field": `[{"id":"x","name":"X","url":"https://example.com/feed"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, ValidateRSSFeeds(value))
		})
	}
}

func TestValidateRSSFeedsCapsFeedCount(t *testing.T) {
	feeds := make([]RSSFeedConfig, maxRSSFeeds+1)
	for index := range feeds {
		feeds[index] = RSSFeedConfig{
			ID:      fmt.Sprintf("feed-%d", index),
			Name:    fmt.Sprintf("Feed %d", index),
			URL:     fmt.Sprintf("https://example.com/%d.xml", index),
			Enabled: true,
		}
	}
	raw, err := json.Marshal(feeds)
	require.NoError(t, err)
	assert.Error(t, ValidateRSSFeeds(string(raw)))
}
