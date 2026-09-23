/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { splitLegalSections } from './legal-reader'

test('HTML section reads retain content when the document is minified', () => {
  const result = splitLegalSections(
    '<p>Before</p><h1>Terms</h1><p>First clause</p><h2>Privacy</h2><p>Second clause</p>',
    'html'
  )
  assert.deepEqual(
    result.headings.map((heading) => heading.text),
    ['Terms', 'Privacy']
  )
  assert.deepEqual(
    result.sections.map((section) => section.body),
    ['<p>Before</p>', '<p>First clause</p>', '<p>Second clause</p>']
  )
})

test('markdown code comments do not become legal headings', () => {
  const result = splitLegalSections(
    '# Terms\n```\n# example\n```\n## Privacy\nText',
    'markdown'
  )
  assert.deepEqual(
    result.headings.map((heading) => heading.text),
    ['Terms', 'Privacy']
  )
  assert.match(result.sections[0].body, /# example/)
})
