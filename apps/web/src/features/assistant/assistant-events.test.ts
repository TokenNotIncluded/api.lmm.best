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
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

import {
  consumeQueuedAssistantRequest,
  peekQueuedAssistantRequest,
  requestAssistantOpen,
  requestAssistantSend,
  subscribeToAssistantOpen,
} from './assistant-events'

const domWindow = new Window({ url: 'https://console.example.test/' })
Object.defineProperty(globalThis, 'window', {
  configurable: true,
  value: domWindow,
})
Object.defineProperty(globalThis, 'CustomEvent', {
  configurable: true,
  value: domWindow.CustomEvent,
})

afterEach(() => {
  consumeQueuedAssistantRequest()
  window.sessionStorage.clear()
})

after(() => domWindow.close())

describe('assistant open events', () => {
  test('queues and atomically consumes a preset across redirects', () => {
    requestAssistantOpen('api-key')
    assert.equal(peekQueuedAssistantRequest()?.preset, 'api-key')
    assert.equal(consumeQueuedAssistantRequest()?.preset, 'api-key')
    assert.equal(consumeQueuedAssistantRequest(), undefined)
  })

  test('notifies an already-mounted launcher with the complete request', () => {
    let receivedPreset: string | undefined
    const unsubscribe = subscribeToAssistantOpen((request) => {
      receivedPreset = request.preset
    })

    requestAssistantOpen('plan')
    unsubscribe()

    assert.equal(receivedPreset, 'plan')
    assert.equal(consumeQueuedAssistantRequest()?.preset, 'plan')
  })

  test('queues one automatic send across an authentication redirect', () => {
    requestAssistantSend('onboarding', '  Help me choose a client.  ')

    const request = consumeQueuedAssistantRequest()
    assert.ok(request)
    assert.equal(request.preset, 'onboarding')
    assert.equal(request.message, 'Help me choose a client.')
    assert.equal(request.autoSend, true)
    assert.equal(consumeQueuedAssistantRequest(), undefined)
  })

  test('redacts credentials before session storage', () => {
    const rawEmail = 'alice@example.test'
    const rawKey = 'sk-' + 'secret1234567890'
    requestAssistantSend(
      undefined,
      `Configure the SDK for ${rawEmail} with ${rawKey}`
    )

    const stored = JSON.stringify({ ...window.sessionStorage })
    assert.doesNotMatch(stored, new RegExp(rawEmail, 'i'))
    assert.doesNotMatch(stored, new RegExp(rawKey, 'i'))
    const request = consumeQueuedAssistantRequest()
    assert.match(request?.message ?? '', /\[REDACTED_EMAIL\]/)
    assert.match(request?.message ?? '', /\[REDACTED_API_KEY\]/)
  })

  test('a welcome-only open preserves a pending automatic send', () => {
    requestAssistantSend(undefined, 'Send me once')
    const before = peekQueuedAssistantRequest()
    requestAssistantOpen('onboarding')
    const after = consumeQueuedAssistantRequest()

    assert.equal(after?.id, before?.id)
    assert.equal(after?.message, 'Send me once')
    assert.equal(after?.autoSend, true)
  })

  test('an explicit normal message replaces stale automatic-send intent', () => {
    requestAssistantSend('service', 'Send once')
    requestAssistantOpen('plan', 'Prefill only')
    const request = consumeQueuedAssistantRequest()

    assert.equal(request?.preset, 'plan')
    assert.equal(request?.message, 'Prefill only')
    assert.equal(request?.autoSend, false)
  })
})
