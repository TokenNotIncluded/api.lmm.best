/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  getApiKeyCreationMode,
  getApiKeyCreationSource,
  isAssistantRuntimeKey,
  matchesApiKeyCreationMode,
} from '../api-key-source'

describe('API key creation source', () => {
  test('treats tokens without a source as legacy manual tokens', () => {
    assert.equal(getApiKeyCreationSource({}), 'legacy')
    assert.equal(getApiKeyCreationMode({}), 'manual')
  })

  test('prefers creation_source over the compatibility source field', () => {
    const token = { creation_source: 'drawing_mcp', source: 'assistant' }
    assert.equal(getApiKeyCreationSource(token), 'drawing_mcp')
    assert.equal(getApiKeyCreationMode(token), 'automatic')
  })

  test('classifies explicit manual and legacy sources as manual', () => {
    assert.equal(
      matchesApiKeyCreationMode({ source: 'manual' }, 'manual'),
      true
    )
    assert.equal(
      matchesApiKeyCreationMode({ creation_source: 'legacy' }, 'manual'),
      true
    )
  })

  test('keeps unknown non-empty sources visible as automatic purposes', () => {
    assert.equal(
      getApiKeyCreationSource({ source: 'future_tool' }),
      'future_tool'
    )
    assert.equal(getApiKeyCreationMode({ source: 'future_tool' }), 'automatic')
  })

  test('identifies the root-owned assistant runtime key as automatic', () => {
    const key = { creation_source: 'assistant_runtime' }
    assert.equal(isAssistantRuntimeKey(key), true)
    assert.equal(matchesApiKeyCreationMode(key, 'automatic'), true)
    assert.equal(isAssistantRuntimeKey({ creation_source: 'assistant' }), false)
  })
})
