import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import type { Row } from '@tanstack/react-table'
import { Window } from 'happy-dom'

import type { User } from '../types'

const dom = new Window({ url: 'https://console.example.test/users' })
dom.document.write('<!doctype html><html><head></head><body></body></html>')
Object.defineProperty(dom.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'PointerEvent',
  'KeyboardEvent',
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
    value:
      key === 'getComputedStyle' ? dom.getComputedStyle.bind(dom) : dom[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { toast } = await import('sonner')
const { api } = await import('@/lib/api')
const { DataTableRowActions } = await import('./data-table-row-actions')
const { UsersProvider, useUsers } = await import('./users-provider')

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const originalPost = api.post
const originalGet = api.get
const originalToastSuccess = toast.success
const originalToastError = toast.error
const roots: Array<ReturnType<typeof createRoot>> = []
const flush = () => new Promise((resolve) => setTimeout(resolve, 25))

afterEach(async () => {
  for (const root of roots.splice(0)) {
    await act(async () => {
      root.unmount()
      await flush()
    })
  }
  api.post = originalPost
  api.get = originalGet
  toast.success = originalToastSuccess
  toast.error = originalToastError
  document.body.replaceChildren()
})
after(() => dom.close())

function makeUser(patch: Partial<User> = {}): User {
  return {
    id: 7,
    username: 'level-user',
    display_name: 'Level User',
    quota: 500000,
    used_quota: 100000,
    request_count: 3,
    group: 'default',
    status: 1,
    role: 1,
    trust_level_override: 0,
    trust_level_info: { level: 0, overridden: true },
    ...patch,
  }
}

function RefreshCount() {
  const { refreshTrigger } = useUsers()
  return <output data-testid='refresh-count'>{refreshTrigger}</output>
}

async function renderActions(user: User) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  roots.push(root)
  await act(async () => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <UsersProvider>
          <DataTableRowActions row={{ original: user } as Row<User>} />
          <RefreshCount />
        </UsersProvider>
      </I18nextProvider>
    )
    await flush()
  })
  return container
}

async function openMenu(container: HTMLElement) {
  const trigger = container.querySelector<HTMLButtonElement>(
    'button[aria-label="Open menu"]'
  )
  assert.ok(trigger)
  await act(async () => {
    trigger.click()
    await flush()
  })
}

function restoreMenuItem() {
  return [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].find(
    (node) => node.textContent?.trim() === 'Restore automatic trust level'
  )
}

async function openRestoreConfirmation(container: HTMLElement) {
  await openMenu(container)
  const item = restoreMenuItem()
  assert.ok(item)
  await act(async () => {
    item.click()
    await flush()
  })
  const dialog = document.querySelector<HTMLElement>('[role="alertdialog"]')
  assert.ok(dialog)
  return dialog
}

function dialogButton(dialog: HTMLElement, label: string) {
  const button = [...dialog.querySelectorAll('button')].find(
    (node) => node.textContent?.trim() === label
  )
  assert.ok(button)
  return button
}

describe('restore automatic trust level', () => {
  test('requires confirmation, sends the standard clear-override payload once, and refreshes', async () => {
    const requests: unknown[] = []
    const successes: string[] = []
    let resolveRequest: ((value: unknown) => void) | undefined
    const request = new Promise((resolve) => {
      resolveRequest = resolve
    })
    api.post = (async (url: string, payload: unknown) => {
      requests.push({ url, payload })
      return request
    }) as typeof api.post
    toast.success = ((message: unknown) => {
      successes.push(String(message))
      return 1
    }) as typeof toast.success
    const container = await renderActions(makeUser())
    const dialog = await openRestoreConfirmation(container)
    assert.equal(requests.length, 0)
    assert.match(dialog.textContent ?? '', /for level-user/)
    assert.match(
      dialog.textContent ?? '',
      /account activation and eligible paid recharge history/
    )
    assert.match(
      dialog.textContent ?? '',
      /Existing balance and account history are preserved/
    )
    const confirm = dialogButton(dialog, 'Restore automatic trust level')
    await act(async () => {
      confirm.click()
      confirm.click()
      await flush()
    })
    assert.deepEqual(requests, [
      {
        url: '/api/user/manage',
        payload: { id: 7, action: 'set_trust_level', value: -1 },
      },
    ])
    assert.equal(confirm.disabled, true)
    assert.equal(dialogButton(dialog, 'Cancel').disabled, true)
    await act(async () => {
      resolveRequest?.({ data: { success: true } })
      await flush()
    })
    assert.deepEqual(successes, ['Trust level updated successfully'])
    assert.equal(
      container.querySelector('[data-testid="refresh-count"]')?.textContent,
      '1'
    )
  })

  test('cancelling the confirmation never posts or refreshes', async () => {
    const requests: unknown[] = []
    api.post = (async (...args: unknown[]) => {
      requests.push(args)
      throw new Error('Cancelled action must not post')
    }) as typeof api.post
    const container = await renderActions(makeUser())
    const dialog = await openRestoreConfirmation(container)
    await act(async () => {
      dialogButton(dialog, 'Cancel').click()
      await flush()
    })
    assert.deepEqual(requests, [])
    assert.equal(
      container.querySelector('[data-testid="refresh-count"]')?.textContent,
      '0'
    )
    assert.equal(
      document.querySelector('[role="alertdialog"][data-open]'),
      null
    )
  })

  test('hides restoration for administrators, deleted accounts, and accounts without an override', async () => {
    const requests: unknown[] = []
    api.post = (async (...args: unknown[]) => {
      requests.push(args)
      throw new Error('Ineligible account must not post')
    }) as typeof api.post
    for (const patch of [
      { role: 10 },
      { role: 100 },
      {
        trust_level_override: null,
        trust_level_info: { level: 1, overridden: false },
      },
      {
        trust_level_override: -1,
        trust_level_info: { level: 1, overridden: false },
      },
      { DeletedAt: '2026-10-09T00:00:00Z' },
    ]) {
      const container = await renderActions(makeUser(patch))
      if (patch.DeletedAt) {
        assert.equal(
          container.querySelector('button[aria-label="Open menu"]'),
          null
        )
      } else {
        await openMenu(container)
        assert.equal(restoreMenuItem(), undefined)
      }
      const root = roots.pop()
      assert.ok(root)
      await act(async () => {
        root.unmount()
        await flush()
      })
      container.remove()
    }
    assert.deepEqual(requests, [])
  })

  test('shows a refused restoration without refreshing the account', async () => {
    const errors: string[] = []
    let requests = 0
    api.post = (async () => ({
      data: { success: ++requests > 1, message: 'backend refused' },
    })) as typeof api.post
    toast.error = ((message: unknown) => {
      errors.push(String(message))
      return 1
    }) as typeof toast.error
    const container = await renderActions(makeUser())
    const dialog = await openRestoreConfirmation(container)
    await act(async () => {
      dialogButton(dialog, 'Restore automatic trust level').click()
      await flush()
    })
    assert.deepEqual(errors, ['backend refused'])
    assert.equal(
      container.querySelector('[data-testid="refresh-count"]')?.textContent,
      '0'
    )
    const retryDialog = await openRestoreConfirmation(container)
    await act(async () => {
      dialogButton(retryDialog, 'Restore automatic trust level').click()
      await flush()
    })
    assert.equal(requests, 2)
    assert.equal(
      container.querySelector('[data-testid="refresh-count"]')?.textContent,
      '1'
    )
  })
})
