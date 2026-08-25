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

describe('AssistantKeyTool confirmation flow', () => {
  test('requires the authoritative warning confirmation count before prepare', async () => {
    const posted: Array<{ url: string; data: unknown }> = []
    api.get = (async () =>
      groupsPayload({
        default: {
          ratio: 0,
          warning: {
            enabled: true,
            message: 'Community routing warning',
            mode: 'modal',
            confirmations: 2,
          },
        },
      })) as typeof api.get
    api.post = (async (url: string, data: unknown) => {
      posted.push({ url, data })
      return {
        data: {
          success: true,
          data: {
            type: 'create_key',
            confirmation_token: 'warned-token',
            requires_confirmation: true,
            expires_in_seconds: 600,
            name: 'AI assistant key',
            group: 'default',
          },
        },
      }
    }) as typeof api.post
    const rendered = await renderTool(true)
    await waitFor(
      () => !findButton('Review key creation').disabled,
      'warned selectable group should finish loading'
    )

    await act(async () => {
      findButton('Review key creation').click()
      await flushEffects()
    })
    assert.equal(posted.length, 0)
    assert.match(document.body.textContent ?? '', /Community routing warning/)
    await act(async () => {
      findButton('Continue').click()
      await flushEffects()
    })
    assert.equal(posted.length, 0)
    await act(async () => {
      findButton('I understand, continue').click()
      await flushEffects()
    })
    assert.deepEqual(posted, [
      {
        url: '/api/assistant/tools/prepare-key',
        data: {
          name: 'AI assistant key',
          group: 'default',
          group_warning_confirmations: 2,
        },
      },
    ])
    await unmount(rendered)
  })

  test('uses a server-prepared action without reposting mutable name or group', async () => {
    const posted: Array<{ url: string; data: unknown }> = []
    api.get = (async (url: string) => {
      assert.equal(url, '/api/user/self/groups')
      return groupsPayload({ 'GPT-Pro': { ratio: 1 } })
    }) as typeof api.get
    api.post = (async (url: string, data: unknown) => {
      posted.push({ url, data })
      return {
        data: {
          success: true,
          data: {
            id: 10,
            name: 'server-owned',
            group: 'GPT-Pro',
            expired_time: -1,
            card: { id: 'card-10', label: 'Private API key' },
          },
        },
      }
    }) as typeof api.post
    const rendered = await renderTool(true, () => {}, {
      type: 'create_key',
      confirmation_token: 'preview-token',
      requires_confirmation: true,
      expires_in_seconds: 600,
      name: 'server-owned',
      group: 'GPT-Pro',
    })

    assert.equal(
      rendered.container.querySelector<HTMLInputElement>('#assistant-key-name')
        ?.disabled,
      true
    )
    await waitFor(
      () => !findButton('Review key creation').disabled,
      'server-prepared action should become reviewable after the live group read'
    )
    await act(async () => {
      findButton('Review key creation').click()
      await flushEffects()
    })
    await waitFor(
      () =>
        [...document.querySelectorAll('button')].some((button) =>
          button.textContent?.includes('Confirm and create')
        ),
      'confirmation dialog should open after live validation'
    )
    await act(async () => {
      findButton('Confirm and create').click()
      await flushEffects()
    })
    assert.deepEqual(posted, [
      {
        url: '/api/assistant/tools/create-key',
        data: {
          confirmation_token: 'preview-token',
          two_factor_code: '',
        },
      },
    ])
    await unmount(rendered)
  })

  test('auto-confirm waits for a successful live group read before POST', async () => {
    let resolveGroups:
      | ((value: ReturnType<typeof groupsPayload>) => void)
      | null = null
    let firstRead = true
    const posted: Array<{ url: string; data: unknown }> = []
    api.get = (async () => {
      if (!firstRead) return groupsPayload({ 'GPT-Pro': { ratio: 1 } })
      firstRead = false
      return await new Promise<ReturnType<typeof groupsPayload>>((resolve) => {
        resolveGroups = resolve
      })
    }) as typeof api.get
    api.post = (async (url: string, data: unknown) => {
      posted.push({ url, data })
      return {
        data: {
          success: true,
          data: {
            id: 11,
            name: 'chat-created',
            group: 'GPT-Pro',
            expired_time: -1,
            card: { id: 'card-11', label: 'Private API key' },
          },
        },
      }
    }) as typeof api.post
    const rendered = await renderTool(
      true,
      () => {},
      {
        type: 'create_key',
        confirmation_token: 'chat-token',
        requires_confirmation: true,
        expires_in_seconds: 600,
        name: 'chat-created',
        group: 'GPT-Pro',
      },
      true
    )

    assert.equal(posted.length, 0)
    await act(async () => {
      assert.ok(resolveGroups)
      resolveGroups(groupsPayload({ 'GPT-Pro': { ratio: 1 } }))
      await flushEffects()
    })
    assert.deepEqual(posted, [
      {
        url: '/api/assistant/tools/create-key',
        data: {
          confirmation_token: 'chat-token',
          two_factor_code: '',
        },
      },
    ])
    await unmount(rendered)
  })
})
