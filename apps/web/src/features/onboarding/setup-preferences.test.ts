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
import { test } from 'node:test'

import { readSetupPreferences, saveSetupPreferences } from './setup-preferences'

test('setup preferences survive navigation without storing account or key data', () => {
  let stored = ''
  Object.defineProperty(globalThis, 'sessionStorage', {
    configurable: true,
    value: {
      getItem: () => stored,
      setItem: (_key: string, value: string) => {
        stored = value
      },
    },
  })
  try {
    saveSetupPreferences({
      platform: 'windows',
      client: 'claude-code',
      secret: 'must-not-save',
    } as never)
    assert.equal(stored.includes('secret'), false)
    assert.deepEqual(readSetupPreferences(), {
      platform: 'windows',
      client: 'claude-code',
    })
    for (const client of [
      'pi',
      'dsh',
      'astrbot',
      'openai-sdk',
      'anthropic-sdk',
    ] as const) {
      saveSetupPreferences({ platform: 'linux', client })
      assert.equal(readSetupPreferences()?.client, client)
    }
    stored = '{broken'
    assert.equal(readSetupPreferences(), null)
    stored = JSON.stringify({ platform: 'ios', client: 'claude-code' })
    assert.equal(readSetupPreferences(), null)
  } finally {
    Reflect.deleteProperty(globalThis, 'sessionStorage')
  }
  assert.doesNotThrow(() =>
    saveSetupPreferences({ platform: 'linux', client: 'codex' })
  )
  assert.equal(readSetupPreferences(), null)
})
