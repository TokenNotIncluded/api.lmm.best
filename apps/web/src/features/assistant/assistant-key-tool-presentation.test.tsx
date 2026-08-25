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

describe('AssistantKeyTool presentation and prepared flow', () => {
  test('explains and exposes connection values to L0 without a creation action', async () => {
    const rendered = await renderTool(false)

    assert.match(rendered.container.textContent ?? '', /Connection details/)
    assert.match(
      rendered.container.textContent ?? '',
      /Base URL tells your client where to connect/
    )
    assert.match(
      rendered.container.textContent ?? '',
      /https:\/\/api\.example\.test\/v1/
    )
    assert.match(rendered.container.textContent ?? '', /<MODEL_ID>/)
    assert.match(
      rendered.container.textContent ?? '',
      /API key creation requires L1/
    )
    assert.equal(rendered.container.querySelector('#assistant-key-name'), null)
    assert.equal(
      [...rendered.container.querySelectorAll('button')].some((button) =>
        button.textContent?.includes('Review key creation')
      ),
      false
    )

    await unmount(rendered)
  })

  test('prepares first and confirms with only the opaque token plus 2FA', async () => {
    const posted: Array<{ url: string; data: unknown }> = []
    let continued = 0
    api.get = (async (url: string) => {
      if (url === '/api/user/self/groups') {
        return groupsPayload({
          auto: { ratio: 1 },
          default: { ratio: 1 },
        })
      }
      assert.equal(url, '/api/assistant/cards/card-9/reveal')
      return {
        data: {
          success: true,
          data: { payload: { api_key: 'sk-created-by-test' } },
        },
      }
    }) as typeof api.get
    api.post = (async (url: string, data: unknown) => {
      posted.push({ url, data })
      if (url === '/api/assistant/tools/prepare-key') {
        return {
          data: {
            success: true,
            data: {
              type: 'create_key',
              confirmation_token: 'opaque-prepared-token',
              requires_confirmation: true,
              expires_in_seconds: 600,
              name: 'AI assistant key',
              group: 'default',
            },
          },
        }
      }
      assert.equal(url, '/api/assistant/tools/create-key')
      return {
        data: {
          success: true,
          data: {
            id: 9,
            name: 'AI assistant key',
            group: 'default',
            expired_time: -1,
            card: { id: 'card-9', label: 'Private API key' },
          },
        },
      }
    }) as typeof api.post
    const rendered = await renderTool(true, () => {
      continued += 1
    })

    await waitFor(
      () =>
        rendered.container.querySelector<HTMLSelectElement>(
          '#assistant-key-group'
        )?.options.length === 1,
      'selectable groups should load before manual preparation'
    )
    const groupSelect = rendered.container.querySelector<HTMLSelectElement>(
      '#assistant-key-group'
    )
    assert.ok(groupSelect)
    assert.deepEqual(
      [...groupSelect.options].map((option) => option.value),
      ['default']
    )

    await act(async () => {
      findButton('Review key creation').click()
      await flushEffects()
    })
    assert.match(document.body.textContent ?? '', /Create this API key\?/)
    assert.deepEqual(posted[0], {
      url: '/api/assistant/tools/prepare-key',
      data: {
        name: 'AI assistant key',
        group: 'default',
        group_warning_confirmations: 0,
      },
    })

    await act(async () => {
      findButton('Confirm and create').click()
      await flushEffects()
    })
    assert.deepEqual(posted[1], {
      url: '/api/assistant/tools/create-key',
      data: {
        confirmation_token: 'opaque-prepared-token',
        two_factor_code: '',
      },
    })
    assert.equal(
      Object.hasOwn(posted[1]?.data as object, 'name') ||
        Object.hasOwn(posted[1]?.data as object, 'group'),
      false
    )
    assert.match(rendered.container.textContent ?? '', /API key created/)
    assert.match(rendered.container.textContent ?? '', /Private API key/)
    assert.doesNotMatch(
      rendered.container.textContent ?? '',
      /sk-created-by-test/
    )

    await act(async () => {
      findButton('Show securely').click()
      await flushEffects()
    })
    assert.match(rendered.container.textContent ?? '', /sk-created-by-test/)

    await act(async () => {
      findButton('I copied it — continue setup').click()
      await flushEffects()
    })
    assert.equal(continued, 1)
    await unmount(rendered)
  })
})
