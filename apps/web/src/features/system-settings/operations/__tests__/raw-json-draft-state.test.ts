/*
Copyright (C) 2025 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/

import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  hasUnsavedJsonDraft,
  shouldApplyRawJsonServerValue,
} from '../raw-json-draft-state'

describe('raw JSON draft state', () => {
  test('distinguishes unsaved editor content from the loaded baseline', () => {
    assert.equal(hasUnsavedJsonDraft('{"value":2}', '{"value":1}'), true)
    assert.equal(hasUnsavedJsonDraft('{"value":1}', '{"value":1}'), false)
  })

  test('preserves a dirty draft when the same setting refetches', () => {
    assert.equal(
      shouldApplyRawJsonServerValue({
        loadedKey: 'setting-a',
        selectedKey: 'setting-a',
        editorValue: '{"draft":true}',
        baselineValue: '{"draft":false}',
      }),
      false
    )
  })

  test('hydrates clean drafts and deliberate setting switches', () => {
    assert.equal(
      shouldApplyRawJsonServerValue({
        loadedKey: 'setting-a',
        selectedKey: 'setting-a',
        editorValue: '{"value":1}',
        baselineValue: '{"value":1}',
      }),
      true
    )
    assert.equal(
      shouldApplyRawJsonServerValue({
        loadedKey: 'setting-a',
        selectedKey: 'setting-b',
        editorValue: '{"draft":true}',
        baselineValue: '{"draft":false}',
      }),
      true
    )
  })
})
