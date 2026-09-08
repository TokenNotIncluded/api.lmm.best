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

import { buildAssistantClientImport } from './client-import'

const options = {
  app: 'claude' as const,
  rootUrl: 'https://api.lmm.best',
  openAIBaseUrl: 'https://api.lmm.best/v1',
  currentOrigin: 'https://api.lmm.best',
  model: 'available-model',
  availableModels: ['available-model'],
  apiKey: 'sk-test-private-key',
}

describe('private client import', () => {
  test('creates documented CC Switch links with protocol-specific endpoints and explicit client activation', () => {
    for (const app of ['claude', 'codex'] as const) {
      const result = buildAssistantClientImport({ ...options, app })
      assert.ok(result)
      const url = new URL(result)
      assert.equal(url.protocol, 'ccswitch:')
      assert.equal(url.hostname, 'v1')
      assert.equal(url.pathname, '/import')
      assert.equal(url.searchParams.get('resource'), 'provider')
      assert.equal(url.searchParams.get('app'), app)
      assert.equal(
        url.searchParams.get('endpoint'),
        app === 'codex' ? options.openAIBaseUrl : options.rootUrl
      )
      assert.equal(url.searchParams.get('model'), options.model)
      assert.equal(url.searchParams.get('apiKey'), options.apiKey)
      assert.equal(url.searchParams.get('enabled'), 'false')
      assert.equal(url.searchParams.has('configUrl'), false)
      assert.equal(url.searchParams.has('usageScript'), false)
    }
  })

  test('blocks insecure, off-origin and malformed destination values', () => {
    for (const rootUrl of [
      'https://attacker.example',
      'http://api.lmm.best',
      'https://user:pass@api.lmm.best',
      'https://api.lmm.best/?redirect=evil',
      'https://api.lmm.best/#secret',
      'javascript:alert(1)',
      '//api.lmm.best',
    ]) {
      assert.equal(
        buildAssistantClientImport({ ...options, rootUrl }),
        null,
        rootUrl
      )
    }
    for (const openAIBaseUrl of [
      'https://attacker.example/v1',
      'https://api.lmm.best/v1/v1',
      'https://api.lmm.best/v1?apiKey=leak',
      'http://api.lmm.best/v1',
    ]) {
      assert.equal(
        buildAssistantClientImport({ ...options, openAIBaseUrl }),
        null,
        openAIBaseUrl
      )
    }
  })

  test('requires a current available model and a manually entered key', () => {
    assert.equal(
      buildAssistantClientImport({ ...options, availableModels: [] }),
      null
    )
    assert.equal(
      buildAssistantClientImport({ ...options, model: 'invented-model' }),
      null
    )
    assert.equal(
      buildAssistantClientImport({
        ...options,
        model: '<MODEL_ID>',
        availableModels: ['<MODEL_ID>'],
      }),
      null
    )
    for (const apiKey of [
      '',
      '   ',
      '<YOUR_API_KEY>',
      'sk-line\nbreak',
      'x'.repeat(513),
    ]) {
      assert.equal(buildAssistantClientImport({ ...options, apiKey }), null)
    }
    const result = buildAssistantClientImport({
      ...options,
      apiKey: ' sk-key&model=injected ',
    })
    assert.ok(result)
    const url = new URL(result)
    assert.equal(url.searchParams.get('apiKey'), 'sk-key&model=injected')
    assert.deepEqual(url.searchParams.getAll('model'), ['available-model'])
  })
})
