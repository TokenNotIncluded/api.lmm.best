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
  act,
  api,
  findButton,
  flushEffects,
  groupsPayload,
  renderTool,
  unmount,
  waitFor,
} from './assistant-key-tool-test-support'

describe('AssistantKeyTool fail-closed behavior', () => {
  test('fails closed for empty and stale group catalogues', async () => {
    api.get = (async () => groupsPayload({})) as typeof api.get
    let posts = 0
    api.post = (async () => {
      posts += 1
      throw new Error('must not post')
    }) as typeof api.post
    const empty = await renderTool(true)
    await waitFor(
      () =>
        (empty.container.textContent ?? '').includes(
          'No selectable key groups are available'
        ),
      'empty live group response should replace the loading state'
    )
    assert.equal(findButton('Review key creation').disabled, true)
    assert.match(
      empty.container.textContent ?? '',
      /No selectable key groups are available/
    )
    assert.equal(posts, 0)
    await unmount(empty)

    let invalidated = 0
    api.get = (async () =>
      groupsPayload({ current: { ratio: 1 } })) as typeof api.get
    const stale = await renderTool(
      true,
      () => {},
      {
        type: 'create_key',
        confirmation_token: 'stale-token',
        requires_confirmation: true,
        expires_in_seconds: 600,
        name: 'stale-key',
        group: 'removed-group',
      },
      true,
      () => {
        invalidated += 1
      }
    )
    await act(async () => {
      await flushEffects()
    })
    assert.equal(posts, 0)
    assert.equal(invalidated, 1)
    await unmount(stale)
  })

  test('auto-confirm never posts when any catalogue entry is malformed', async () => {
    let posts = 0
    api.get = (async () => ({
      data: {
        success: true,
        data: {
          default: { desc: 'Default', ratio: 1 },
          malformed: null,
        },
      },
    })) as typeof api.get
    api.post = (async () => {
      posts += 1
      throw new Error('must not post')
    }) as typeof api.post
    const rendered = await renderTool(
      true,
      () => {},
      {
        type: 'create_key',
        confirmation_token: 'malformed-catalogue-token',
        requires_confirmation: true,
        expires_in_seconds: 600,
        name: 'assistant-key',
        group: 'default',
      },
      true
    )
    await waitFor(
      () =>
        (rendered.container.textContent ?? '').includes(
          'No selectable key groups are available'
        ),
      'malformed live catalogue should fail closed before auto-confirm'
    )
    assert.equal(posts, 0)
    await unmount(rendered)
  })

  test('rejects a tampered prepare response before confirmation POST', async () => {
    let invalidated = 0
    const posted: Array<{ url: string; data: unknown }> = []
    api.get = (async () =>
      groupsPayload({ default: { ratio: 1 } })) as typeof api.get
    api.post = (async (url: string, data: unknown) => {
      posted.push({ url, data })
      return {
        data: {
          success: true,
          data: {
            type: 'create_key',
            confirmation_token: 'tampered-token',
            requires_confirmation: true,
            expires_in_seconds: 600,
            name: 'AI assistant key',
            group: 'auto',
          },
        },
      }
    }) as typeof api.post
    const rendered = await renderTool(
      true,
      () => {},
      undefined,
      false,
      () => {
        invalidated += 1
      }
    )
    await waitFor(
      () => !findButton('Review key creation').disabled,
      'valid live groups should enable manual preparation'
    )

    await act(async () => {
      findButton('Review key creation').click()
      await flushEffects()
    })
    assert.equal(posted.length, 1)
    assert.equal(posted[0]?.url, '/api/assistant/tools/prepare-key')
    assert.equal(invalidated, 1)
    assert.doesNotMatch(
      document.body.textContent ?? '',
      /Create this API key\?/
    )
    await unmount(rendered)
  })
})
