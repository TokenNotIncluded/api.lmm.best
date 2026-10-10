/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

import { frontendEntry } from './frontend-entry'

const dom = new Window()
after(() => dom.close())
function entry(html: string) {
  return frontendEntry(
    new dom.DOMParser().parseFromString(
      html,
      'text/html'
    ) as unknown as Document,
    'https://example.test'
  )
}

test('entry comparison accepts only this origins content addressed application', () => {
  assert.equal(
    entry('<script src="/static/js/index.abc123.js"></script>'),
    '/static/js/index.abc123.js'
  )
  assert.equal(
    entry(
      '<script src="https://example.test/static/js/index.def456.js"></script>'
    ),
    '/static/js/index.def456.js'
  )
  assert.equal(
    entry(
      '<script src="https://other.test/static/js/index.abc123.js"></script>'
    ),
    null
  )
  assert.equal(
    entry('<script src="/static/js/vendor.abc123.js"></script>'),
    null
  )
  assert.equal(entry('<script src="/index.js"></script>'), null)
  assert.equal(entry('<h1>503 Service unavailable</h1>'), null)
})
