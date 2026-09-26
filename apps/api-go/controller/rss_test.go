/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRSSDocument(t *testing.T) {
	body := []byte(`<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <title>Example</title>
    <item>
      <guid>post-1</guid>
      <title>First &amp; best</title>
      <link>/posts/1</link>
      <description>&lt;p&gt;Hello &lt;strong&gt;world&lt;/strong&gt;.&lt;/p&gt;</description>
      <pubDate>Fri, 25 Sep 2026 08:00:00 +0000</pubDate>
    </item>
  </channel>
</rss>`)
	items, err := parseRSSDocument(body, "https://example.com/feed.xml")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "post-1", items[0].ID)
	assert.Equal(t, "First & best", items[0].Title)
	assert.Equal(t, "https://example.com/posts/1", items[0].URL)
	assert.Equal(t, "Hello world .", items[0].Summary)
	assert.Equal(t, "2026-09-25T08:00:00Z", items[0].PublishedAt)
}

func TestParseAtomDocument(t *testing.T) {
	body := []byte(`<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Example</title>
  <entry>
    <id>tag:example.com,2026:1</id>
    <title>Atom item</title>
    <link rel="alternate" href="/posts/atom"/>
    <summary type="html">&lt;p&gt;Atom summary&lt;/p&gt;</summary>
    <updated>2026-09-25T09:00:00Z</updated>
  </entry>
</feed>`)
	items, err := parseRSSDocument(body, "https://example.com/atom.xml")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "tag:example.com,2026:1", items[0].ID)
	assert.Equal(t, "https://example.com/posts/atom", items[0].URL)
	assert.Equal(t, "Atom summary", items[0].Summary)
	assert.Equal(t, "2026-09-25T09:00:00Z", items[0].PublishedAt)
}

func TestParseRSSDocumentRejectsUnknownRoot(t *testing.T) {
	_, err := parseRSSDocument([]byte(`<html><body>not a feed</body></html>`), "https://example.com/feed")
	assert.Error(t, err)
}
