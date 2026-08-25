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

import type { AssistantCreateKeyAction } from './api'
import {
  isAuthoritativePreparedKeyAction,
  selectableAssistantKeyGroups,
} from './assistant-key-contract'

const action: AssistantCreateKeyAction = {
  type: 'create_key',
  confirmation_token: 'opaque-token',
  requires_confirmation: true,
  expires_in_seconds: 600,
  name: 'server-name',
  group: 'default',
}

describe('assistant key contract', () => {
  test('exposes only exact real groups with valid warning metadata', () => {
    const groups = selectableAssistantKeyGroups({
      success: true,
      data: {
        auto: { desc: 'virtual', ratio: 1 },
        AUTO: { desc: 'virtual', ratio: 1 },
        ' padded ': { desc: 'invalid', ratio: 1 },
        default: { desc: 'Default', ratio: 1 },
        stringRatio: { desc: 'String ratio', ratio: '0.5' },
        warned: {
          desc: 'Warned',
          ratio: 0,
          warning: {
            enabled: true,
            message: 'Community routing warning',
            mode: 'modal',
            confirmations: 3,
          },
        },
        disabled: {
          desc: 'Disabled warning',
          ratio: 0,
          warning: {
            enabled: false,
            message: 'Explicitly disabled',
            mode: 'modal',
            confirmations: 3,
          },
        },
      },
    })

    assert.deepEqual(groups, [
      { id: 'default' },
      { id: 'disabled' },
      { id: 'stringRatio' },
      {
        id: 'warned',
        warning: {
          enabled: true,
          message: 'Community routing warning',
          mode: 'modal',
          confirmations: 3,
        },
      },
    ])
  })

  test('fails the whole catalogue closed on malformed entries', () => {
    const malformedEntries: unknown[] = [
      null,
      'group',
      {},
      { desc: 'Missing ratio' },
      { desc: 'NaN', ratio: Number.NaN },
      { desc: 'Infinity', ratio: Number.POSITIVE_INFINITY },
      { desc: 'Negative', ratio: -1 },
      { desc: 'Blank ratio', ratio: '' },
      { desc: 'Hex ratio', ratio: '0x10' },
      { desc: 'Infinite ratio', ratio: 'Infinity' },
      {
        desc: 'Invalid warning',
        ratio: 0,
        warning: {
          enabled: true,
          message: '',
          mode: 'modal',
          confirmations: 99,
        },
      },
    ]
    for (const malformed of malformedEntries) {
      const payload = {
        success: true,
        data: { default: { desc: 'Default', ratio: 1 }, malformed },
      }
      assert.deepEqual(selectableAssistantKeyGroups(payload), [])
    }
  })

  test('fails closed when the response or catalogue is malformed', () => {
    const malformedResponses: unknown[] = [
      null,
      { success: 'true', data: {} },
      { success: true },
      { success: true, data: null },
      { success: true, data: 'catalogue' },
      { success: true, data: ['group'] },
      { success: true, data: {}, unexpected: true },
    ]
    for (const payload of malformedResponses) {
      assert.deepEqual(selectableAssistantKeyGroups(payload), [])
    }
  })

  test('accepts only live server-owned preparation fields', () => {
    const groups = [{ id: 'default' }]
    assert.equal(isAuthoritativePreparedKeyAction(action, groups), true)
    assert.equal(
      isAuthoritativePreparedKeyAction(action, groups, {
        name: 'server-name',
        group: 'default',
      }),
      true
    )
    assert.equal(
      isAuthoritativePreparedKeyAction(action, groups, {
        name: 'client-name',
        group: 'default',
      }),
      false
    )
    assert.equal(
      isAuthoritativePreparedKeyAction(action, [{ id: 'vip' }]),
      false
    )
  })

  test('rejects virtual, expired, padded, and oversized action fields', () => {
    const groups = [{ id: 'default' }, { id: 'vip' }]
    const mutations: AssistantCreateKeyAction[] = [
      { ...action, group: 'auto' },
      { ...action, expires_in_seconds: 0 },
      { ...action, confirmation_token: ' padded-token ' },
      { ...action, confirmation_token: 'x'.repeat(513) },
      { ...action, name: 'x'.repeat(51) },
      { ...action, api_key: 'must-not-appear' } as AssistantCreateKeyAction,
    ]
    for (const mutation of mutations) {
      assert.equal(isAuthoritativePreparedKeyAction(mutation, groups), false)
    }
  })
})
