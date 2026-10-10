/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { drawingResultImages, resultImage } from './result-images'

const pngData = Buffer.from('\x89PNG\r\n\x1a\n', 'binary').toString('base64')

test('drawing images render from array and object payloads without expanding base64 in metadata', () => {
  for (const entries of [
    [{ b64_json: pngData, revised_prompt: 'Retain this prompt' }],
    { b64_json: pngData, revised_prompt: 'Retain this prompt' },
  ]) {
    const value = {
      message: 'Completed',
      data: { created: 123, data: entries, usage: { total_tokens: 100 } },
    }
    const view = drawingResultImages(value, 'Image result')
    assert.equal(view.images.length, 1)
    assert.equal(view.images[0].mimeType, 'image/png')
    assert.equal(view.images[0].data, pngData)
    assert.equal(JSON.stringify(view.metadata).includes(pngData), false)
    assert.equal(
      JSON.stringify(view.metadata).includes('Retain this prompt'),
      true
    )
    assert.equal(JSON.stringify(view.metadata).includes('total_tokens'), true)
    assert.equal(
      JSON.stringify(value).includes(pngData),
      true,
      'the retained response is not mutated'
    )
  }
})

test('native and drawing image ceilings cover large images while rejecting unsupported types and excess data', () => {
  const data = Buffer.concat([
    Buffer.from('\x89PNG\r\n\x1a\n', 'binary'),
    Buffer.alloc(3 * 1024 * 1024),
  ]).toString('base64')
  assert.ok(resultImage(data, 'image/png'))
  assert.equal(resultImage(data, 'image/svg+xml'), null)
  assert.equal(resultImage(Buffer.from('<svg></svg>').toString('base64')), null)
  assert.equal(resultImage('not base64!'), null)
  assert.equal(resultImage('A'.repeat(32 * 1024 * 1024 + 1)), null)
})
