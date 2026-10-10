/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { renderToStaticMarkup } from 'react-dom/server'

import { ErrorArtwork } from './error-artwork'

// The error scene must still render when the network and game service fail.
test('error artwork is decorative, self-contained and status-specific', () => {
  const symbols = new Set<string>()
  for (const status of [401, 403, 404, 429, 500, 503]) {
    const html = renderToStaticMarkup(<ErrorArtwork status={status} />)
    assert.match(html, /aria-hidden="true"/)
    assert.match(html, /focusable="false"/)
    assert.doesNotMatch(html, /<(?:img|image|canvas|script|button|a)\b/)
    const symbol = html.match(/class="error-art-symbol" d="([^"]+)"/)
    assert.ok(symbol, String(status))
    symbols.add(symbol[1])
  }
  assert.equal(symbols.size, 6)
})

test('unknown and omitted statuses retain the generic recovery artwork', () => {
  const generic = renderToStaticMarkup(<ErrorArtwork status={500} />)
  assert.equal(renderToStaticMarkup(<ErrorArtwork status={502} />), generic)
  assert.equal(renderToStaticMarkup(<ErrorArtwork />), generic)
  assert.equal(
    renderToStaticMarkup(<ErrorArtwork status='<script>untrusted</script>' />),
    generic
  )
})
