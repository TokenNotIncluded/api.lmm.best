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

import type { AssistantCreateKeyAction, AssistantCreatedKey } from './api'
import { assistantKeyCreationMachine } from './assistant-key-creation-machine'

const preparedAction: AssistantCreateKeyAction = {
  type: 'create_key',
  confirmation_token: 'opaque-token',
  requires_confirmation: true,
  expires_in_seconds: 600,
  name: 'server-name',
  group: 'default',
}

const createdKey: AssistantCreatedKey = {
  id: 7,
  name: 'server-name',
  group: 'default',
  expired_time: -1,
  card: { id: 'secure-card', label: 'Private API key' },
}

describe('assistant key creation machine', () => {
  test('loads external actions as server-owned immutable draft values', () => {
    let state = assistantKeyCreationMachine.initialState(
      'local-name',
      preparedAction
    )
    assert.deepEqual(state, {
      name: 'server-name',
      group: 'default',
      phase: { kind: 'external', action: preparedAction },
    })

    state = assistantKeyCreationMachine.reducer(state, {
      type: 'set-name',
      name: 'tampered-name',
    })
    state = assistantKeyCreationMachine.reducer(state, {
      type: 'set-group',
      group: 'auto',
    })
    assert.equal(state.name, 'server-name')
    assert.equal(state.group, 'default')
  })

  test('keeps warning acknowledgement and local preparation explicit', () => {
    let state = assistantKeyCreationMachine.initialState('local-name')
    state = assistantKeyCreationMachine.reducer(state, {
      type: 'set-group',
      group: 'default',
    })
    state = assistantKeyCreationMachine.reducer(state, {
      type: 'show-warning',
      warning: {
        enabled: true,
        message: 'Community routing warning',
        mode: 'modal',
        confirmations: 2,
      },
      count: 1,
    })
    assert.equal(state.phase.kind, 'warning')

    state = assistantKeyCreationMachine.reducer(state, {
      type: 'start-preparing',
    })
    assert.equal(state.phase.kind, 'preparing')
    state = assistantKeyCreationMachine.reducer(state, {
      type: 'prepared',
      action: preparedAction,
    })
    assert.deepEqual(state.phase, {
      kind: 'reviewing',
      action: preparedAction,
      source: 'local',
      twoFactorCode: '',
    })
  })

  test('preserves 2FA input across a recoverable confirmation failure', () => {
    let state = assistantKeyCreationMachine.initialState(
      'local-name',
      preparedAction
    )
    state = assistantKeyCreationMachine.reducer(state, {
      type: 'review-external',
      action: preparedAction,
    })
    state = assistantKeyCreationMachine.reducer(state, {
      type: 'set-two-factor',
      code: 'ABCD-EFGH',
    })
    state = assistantKeyCreationMachine.reducer(state, {
      type: 'start-confirming',
      action: preparedAction,
      source: 'external',
      twoFactorCode: 'ABCD-EFGH',
    })
    assert.equal(state.phase.kind, 'confirming')

    state = assistantKeyCreationMachine.reducer(state, {
      type: 'confirmation-failed',
    })
    assert.deepEqual(state.phase, {
      kind: 'reviewing',
      action: preparedAction,
      source: 'external',
      twoFactorCode: 'ABCD-EFGH',
    })
  })

  test('does not erase a committed result when the parent clears its action', () => {
    let state = assistantKeyCreationMachine.initialState(
      'local-name',
      preparedAction
    )
    state = assistantKeyCreationMachine.reducer(state, {
      type: 'created',
      key: createdKey,
    })
    state = assistantKeyCreationMachine.reducer(state, {
      type: 'clear-external',
    })
    assert.deepEqual(state.phase, { kind: 'created', key: createdKey })
  })

  test('never synthesizes auto or a removed group into the live selection', () => {
    const state = assistantKeyCreationMachine.initialState('local-name')
    assert.equal(
      assistantKeyCreationMachine.selectedGroup(state, [
        { id: 'default' },
        { id: 'vip' },
      ]),
      'default'
    )
    assert.equal(assistantKeyCreationMachine.selectedGroup(state, []), '')

    const external = assistantKeyCreationMachine.initialState(
      'local-name',
      preparedAction
    )
    assert.equal(
      assistantKeyCreationMachine.selectedGroup(external, [{ id: 'vip' }]),
      'default'
    )
  })
})
