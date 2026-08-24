/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({
  url: 'https://console.example.test/admin/users',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'HTMLLabelElement',
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
const { api } = await import('@/lib/api')
const { UserQuotaDialog } = await import('./user-quota-dialog')

const originalPost = api.post
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

function findButton(text: string) {
  const button = [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
  ].find((candidate) => candidate.textContent?.includes(text))
  assert.ok(button, `Could not find button containing ${text}`)
  return button
}

function findAmountInput() {
  const input = document.querySelector<HTMLInputElement>('#user-quota-amount')
  assert.ok(input, 'Could not find the quota amount input')
  return input
}

async function setInputValue(input: HTMLInputElement, value: string) {
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

async function renderDialog() {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <UserQuotaDialog
          open
          onOpenChange={() => {}}
          userId={41}
          currentQuota={100}
          onSuccess={() => {}}
        />
      </I18nextProvider>
    )
    await flushEffects()
  })
  return { container, root }
}

afterEach(() => {
  api.post = originalPost
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('UserQuotaDialog amount validation', () => {
  test('blocks an empty override, accepts explicit zero, and associates the label', async () => {
    const requests: unknown[] = []
    api.post = (async (url: string, data: unknown) => {
      requests.push({ url, data })
      return { data: { success: true, data: {} } }
    }) as typeof api.post

    const { root } = await renderDialog()
    try {
      const input = findAmountInput()
      const label = document.querySelector<HTMLLabelElement>(
        'label[for="user-quota-amount"]'
      )
      assert.ok(label)
      assert.equal(label.htmlFor, input.id)

      await act(async () => {
        findButton('Override').click()
        await flushEffects()
      })
      const confirm = findButton('Confirm')
      assert.equal(confirm.disabled, true)
      confirm.click()
      await flushEffects()
      assert.equal(requests.length, 0)

      await setInputValue(input, '0')
      assert.equal(findButton('Confirm').disabled, false)
      await act(async () => {
        findButton('Confirm').click()
        await flushEffects()
      })

      assert.deepEqual(requests, [
        {
          url: '/api/user/manage',
          data: { id: 41, action: 'add_quota', mode: 'override', value: 0 },
        },
      ])
    } finally {
      await act(async () => root.unmount())
    }
  })

  test('rejects NaN and non-finite values before submission', async () => {
    const requests: unknown[] = []
    api.post = (async (url: string, data: unknown) => {
      requests.push({ url, data })
      return { data: { success: true, data: {} } }
    }) as typeof api.post

    for (const value of ['NaN', 'Infinity', '1e309']) {
      const { root } = await renderDialog()
      try {
        const input = findAmountInput()
        await setInputValue(input, value)
        assert.equal(
          findButton('Confirm').disabled,
          true,
          `Expected ${value} to be rejected`
        )
        findButton('Confirm').click()
        await flushEffects()
      } finally {
        await act(async () => root.unmount())
        document.body.replaceChildren()
      }
    }

    assert.equal(requests.length, 0)
  })

  test('keeps positive add and subtract submissions unchanged', async () => {
    const requests: unknown[] = []
    api.post = (async (url: string, data: unknown) => {
      requests.push({ url, data })
      return { data: { success: true, data: {} } }
    }) as typeof api.post

    for (const mode of ['add', 'subtract'] as const) {
      const { root } = await renderDialog()
      try {
        if (mode === 'subtract') {
          await act(async () => {
            findButton('Subtract').click()
            await flushEffects()
          })
        }
        await setInputValue(findAmountInput(), '2')
        assert.equal(findButton('Confirm').disabled, false)
        await act(async () => {
          findButton('Confirm').click()
          await flushEffects()
        })
      } finally {
        await act(async () => root.unmount())
        document.body.replaceChildren()
      }
    }

    assert.deepEqual(requests, [
      {
        url: '/api/user/manage',
        data: { id: 41, action: 'add_quota', mode: 'add', value: 1000000 },
      },
      {
        url: '/api/user/manage',
        data: { id: 41, action: 'add_quota', mode: 'subtract', value: 1000000 },
      },
    ])
  })
})
