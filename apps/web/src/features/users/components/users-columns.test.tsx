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

import type { User } from '../types'

const domWindow = new Window({
  url: 'https://console.example.test/admin/users',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'PointerEvent',
  'FocusEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { toast } = await import('sonner')
const { api } = await import('@/lib/api')
const { UserTrustLevelCell } = await import('./user-trust-level-cell')
const { UsersProvider, useUsers } = await import('./users-provider')

const originalPost = api.post
const originalToastSuccess = toast.success
const originalToastError = toast.error
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Decrease trust level': 'Decrease trust level',
        'Increase trust level': 'Increase trust level',
      },
    },
  },
})

function createUser(level: number, role = 1): User {
  return {
    id: 7,
    username: 'level-user',
    display_name: 'Level User',
    quota: 0,
    used_quota: 0,
    request_count: 0,
    group: 'default',
    status: 1,
    role,
    trust_level_info: { level },
  }
}

function RefreshCount() {
  const { refreshTrigger } = useUsers()
  return <output data-testid='refresh-count'>{refreshTrigger}</output>
}

async function renderCell(user: User) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <UsersProvider>
          <UserTrustLevelCell user={user} />
          <RefreshCount />
        </UsersProvider>
      </I18nextProvider>
    )
  })
  return { container, root }
}

async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 0))
}

function levelButton(container: HTMLElement, label: string) {
  const button = container.querySelector<HTMLButtonElement>(
    `button[aria-label="${label}"]`
  )
  assert.ok(button, `Could not find ${label} button`)
  return button
}

afterEach(() => {
  api.post = originalPost
  toast.success = originalToastSuccess
  toast.error = originalToastError
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('UserTrustLevelCell', () => {
  test('account roles remain L5 or L6 even when an automatic-level projection is also present', async () => {
    for (const [role, level] of [
      [10, 5],
      [100, 6],
    ]) {
      const user = { ...createUser(2), role }
      const { container, root } = await renderCell(user)
      assert.match(container.textContent ?? '', new RegExp(`L${level}`))
      assert.equal(
        levelButton(container, 'Decrease trust level').disabled,
        true
      )
      assert.equal(
        levelButton(container, 'Increase trust level').disabled,
        true
      )
      await act(async () => root.unmount())
    }
  })
  test('places decrease on the left and increase on the right', async () => {
    const { container, root } = await renderCell(createUser(2))
    const buttons = [...container.querySelectorAll('button')]
    assert.equal(buttons[0]?.getAttribute('aria-label'), 'Decrease trust level')
    assert.equal(buttons[1]?.getAttribute('aria-label'), 'Increase trust level')
    await act(async () => root.unmount())
  })

  test('updates by one level, blocks duplicate requests, and refreshes', async () => {
    const requests: unknown[] = []
    const successMessages: string[] = []
    let resolveRequest: ((value: unknown) => void) | undefined
    const request = new Promise((resolve) => {
      resolveRequest = resolve
    })
    api.post = (async (url: string, payload: unknown) => {
      requests.push({ url, payload })
      return request
    }) as typeof api.post
    toast.success = ((message: unknown) => {
      successMessages.push(String(message))
      return 1
    }) as typeof toast.success

    const { container, root } = await renderCell(createUser(2))
    const decrease = levelButton(container, 'Decrease trust level')
    const increase = levelButton(container, 'Increase trust level')
    await act(async () => {
      increase.click()
      increase.click()
      await flush()
    })

    assert.deepEqual(requests, [
      {
        url: '/api/user/manage',
        payload: { id: 7, action: 'set_trust_level', value: 3 },
      },
    ])
    assert.equal(decrease.disabled, true)
    assert.equal(increase.disabled, true)

    await act(async () => {
      resolveRequest?.({ data: { success: true } })
      await flush()
    })

    assert.deepEqual(successMessages, ['Trust level updated successfully'])
    assert.equal(
      container.querySelector('[data-testid="refresh-count"]')?.textContent,
      '1'
    )
    await act(async () => root.unmount())
  })

  test('shows a failed request and re-enables both controls', async () => {
    const errorMessages: string[] = []
    api.post = (async () => ({
      data: { success: false, message: 'backend refused' },
    })) as typeof api.post
    toast.error = ((message: unknown) => {
      errorMessages.push(String(message))
      return 1
    }) as typeof toast.error

    const { container, root } = await renderCell(createUser(2))
    const decrease = levelButton(container, 'Decrease trust level')
    const increase = levelButton(container, 'Increase trust level')
    await act(async () => {
      decrease.click()
      await flush()
    })

    assert.deepEqual(errorMessages, ['backend refused'])
    assert.equal(decrease.disabled, false)
    assert.equal(increase.disabled, false)
    assert.equal(
      container.querySelector('[data-testid="refresh-count"]')?.textContent,
      '0'
    )
    await act(async () => root.unmount())
  })

  test('disables unavailable boundaries and administrator controls', async () => {
    for (const fixture of [
      { user: createUser(0), decrease: true, increase: false },
      { user: createUser(4), decrease: false, increase: true },
      { user: createUser(5, 10), decrease: true, increase: true },
      { user: createUser(6, 100), decrease: true, increase: true },
    ]) {
      const rendered = await renderCell(fixture.user)
      assert.equal(
        levelButton(rendered.container, 'Decrease trust level').disabled,
        fixture.decrease
      )
      assert.equal(
        levelButton(rendered.container, 'Increase trust level').disabled,
        fixture.increase
      )
      await act(async () => rendered.root.unmount())
      rendered.container.remove()
    }
  })
})
