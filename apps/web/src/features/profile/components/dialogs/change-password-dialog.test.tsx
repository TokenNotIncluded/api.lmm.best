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

const domWindow = new Window({
  url: 'https://console.example.test/profile',
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
  'FocusEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.defineProperties(globalThis, {
  requestAnimationFrame: {
    configurable: true,
    value: (callback: FrameRequestCallback) => setTimeout(() => callback(0), 0),
  },
  cancelAnimationFrame: {
    configurable: true,
    value: (handle: number) => clearTimeout(handle),
  },
  getComputedStyle: {
    configurable: true,
    value: domWindow.getComputedStyle.bind(domWindow),
  },
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { toast } = await import('sonner')
const { api } = await import('@/lib/api')
const { ChangePasswordDialog } = await import('./change-password-dialog')

const originalPut = api.put
const originalToastSuccess = toast.success
const originalToastError = toast.error
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

async function flushEffects() {
  await new Promise((resolve) => setTimeout(resolve, 20))
}

function dialogElement(
  open: boolean,
  onOpenChange: (open: boolean) => void,
  username = 'alice'
) {
  return (
    <I18nextProvider i18n={i18n}>
      <ChangePasswordDialog
        open={open}
        onOpenChange={onOpenChange}
        username={username}
      />
    </I18nextProvider>
  )
}

async function renderDialog(onOpenChange: (open: boolean) => void) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(dialogElement(true, onOpenChange))
    await flushEffects()
  })
  return { root }
}

async function rerenderDialog(
  root: ReturnType<typeof createRoot>,
  open: boolean,
  onOpenChange: (open: boolean) => void,
  username = 'alice'
) {
  await act(async () => {
    root.render(dialogElement(open, onOpenChange, username))
    await flushEffects()
  })
}

async function setInputValue(id: string, value: string) {
  const input = document.querySelector<HTMLInputElement>(`#${id}`)
  assert.ok(input)
  const setValue = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setValue)
  await act(async () => {
    setValue.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
    input.dispatchEvent(new Event('change', { bubbles: true }))
    await flushEffects()
  })
}

function passwordValues() {
  return ['currentPassword', 'newPassword', 'confirmPassword'].map((id) => {
    const input = document.querySelector<HTMLInputElement>(`#${id}`)
    assert.ok(input)
    return input.value
  })
}

function findButton(text: string) {
  const button = [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
  ].find((candidate) => candidate.textContent?.trim() === text)
  assert.ok(button, `Could not find button ${text}`)
  return button
}

async function enterPasswords() {
  await setInputValue('currentPassword', 'old-password')
  await setInputValue('newPassword', 'new-password')
  await setInputValue('confirmPassword', 'new-password')
}

async function unmount(root: ReturnType<typeof createRoot>) {
  await act(async () => root.unmount())
}

afterEach(() => {
  api.put = originalPut
  toast.success = originalToastSuccess
  toast.error = originalToastError
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('ChangePasswordDialog secret reset', () => {
  test('keeps fields while open but clears them across controlled close and reopen', async () => {
    const onOpenChange = () => undefined
    const { root } = await renderDialog(onOpenChange)
    try {
      await enterPasswords()
      await rerenderDialog(root, true, onOpenChange, 'alice-renamed')
      assert.deepEqual(passwordValues(), [
        'old-password',
        'new-password',
        'new-password',
      ])

      await rerenderDialog(root, false, onOpenChange)
      await rerenderDialog(root, true, onOpenChange)
      assert.deepEqual(passwordValues(), ['', '', ''])
    } finally {
      await unmount(root)
    }
  })

  test('clears secrets immediately when Cancel requests close', async () => {
    const openChanges: boolean[] = []
    const onOpenChange = (open: boolean) => openChanges.push(open)
    const { root } = await renderDialog(onOpenChange)
    try {
      await enterPasswords()
      await act(async () => {
        findButton('Cancel').click()
        await flushEffects()
      })
      assert.deepEqual(openChanges, [false])
      assert.deepEqual(passwordValues(), ['', '', ''])
    } finally {
      await unmount(root)
    }
  })

  test('clears secrets immediately after a successful password change', async () => {
    const requests: unknown[] = []
    api.put = (async (url: string, data: unknown) => {
      requests.push({ url, data })
      return { data: { success: true } }
    }) as typeof api.put
    toast.success = (() => 'success-toast') as typeof toast.success
    toast.error = (() => 'error-toast') as typeof toast.error
    const openChanges: boolean[] = []
    const onOpenChange = (open: boolean) => openChanges.push(open)
    const { root } = await renderDialog(onOpenChange)
    try {
      await enterPasswords()
      await act(async () => {
        findButton('Change Password').click()
        await flushEffects()
      })
      assert.deepEqual(requests, [
        {
          url: '/api/user/self',
          data: {
            original_password: 'old-password',
            password: 'new-password',
          },
        },
      ])
      assert.deepEqual(openChanges, [false])
      assert.deepEqual(passwordValues(), ['', '', ''])
    } finally {
      await unmount(root)
    }
  })
})
