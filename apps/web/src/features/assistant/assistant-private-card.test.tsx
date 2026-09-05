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

const domWindow = new Window({ url: 'https://console.example.test/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLAnchorElement',
  'HTMLButtonElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
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
const { api } = await import('@/lib/api')
const { AssistantPrivateCard } = await import('./assistant-private-card')

const originalGet = api.get
const originalWindowOpen = window.open
const originalAnchorClick = HTMLAnchorElement.prototype.click
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((nextResolve) => {
    resolve = nextResolve
  })
  return { promise, resolve }
}

async function flushEffects() {
  await new Promise((resolve) => setTimeout(resolve, 10))
}

function findButton(container: HTMLElement, label: string) {
  const button = [...container.querySelectorAll('button')].find(
    (item) => item.textContent?.trim() === label
  )
  assert.ok(button, `Missing button: ${label}`)
  return button
}

afterEach(() => {
  api.get = originalGet
  window.open = originalWindowOpen
  HTMLAnchorElement.prototype.click = originalAnchorClick
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('AssistantPrivateCard external import', () => {
  test('falls back to same-tab navigation when an async import popup is blocked', async () => {
    const reveal = deferred<{
      data: {
        success: true
        data: { payload: { api_key: string } }
      }
    }>()
    let revealResolved = false
    const popupAttempts: Array<'before-reveal' | 'after-reveal'> = []
    const navigations: string[] = []

    api.get = (async () => reveal.promise) as typeof api.get
    window.open = (() => {
      popupAttempts.push(revealResolved ? 'after-reveal' : 'before-reveal')
      return null
    }) as typeof window.open
    HTMLAnchorElement.prototype.click = function () {
      navigations.push(this.href)
    }

    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    try {
      await act(async () => {
        root.render(
          <I18nextProvider i18n={i18n}>
            <AssistantPrivateCard
              card={{ id: 'private-card-1', label: 'Created API key' }}
              onContinue={() => undefined}
              onImportToCCSwitch={() =>
                'ccswitch://v1/import?resource=provider&app=claude'
              }
            />
          </I18nextProvider>
        )
      })

      await act(async () => {
        findButton(container, 'Import to CC Switch').click()
      })

      assert.deepEqual(popupAttempts, ['before-reveal'])
      assert.deepEqual(navigations, [])

      await act(async () => {
        revealResolved = true
        reveal.resolve({
          data: {
            success: true,
            data: { payload: { api_key: 'private-secret' } },
          },
        })
        await flushEffects()
      })

      assert.deepEqual(navigations, [
        'ccswitch://v1/import?resource=provider&app=claude',
      ])
      assert.equal(
        container.querySelector('[data-testid="assistant-private-card-value"]'),
        null
      )
    } finally {
      await act(async () => root.unmount())
    }
  })

  test('keeps a revealed credential when an import URL cannot be prepared', async () => {
    let popupClosed = false
    const popup = {
      closed: false,
      close: () => {
        popupClosed = true
      },
      focus: () => undefined,
      location: { href: '' },
      opener: {} as Window | null,
    }

    api.get = (async () => ({
      data: {
        success: true,
        data: { payload: { api_key: 'private-secret' } },
      },
    })) as typeof api.get
    window.open = (() =>
      popup as unknown as Window) as unknown as typeof window.open

    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    try {
      await act(async () => {
        root.render(
          <I18nextProvider i18n={i18n}>
            <AssistantPrivateCard
              card={{ id: 'private-card-2', label: 'Created API key' }}
              onContinue={() => undefined}
              onImportToCCSwitch={() => null}
            />
          </I18nextProvider>
        )
      })

      await act(async () => {
        findButton(container, 'Show securely').click()
        await flushEffects()
      })
      assert.match(container.textContent ?? '', /private-secret/)

      await act(async () => {
        findButton(container, 'Import to CC Switch').click()
        await flushEffects()
      })

      assert.equal(popupClosed, true)
      assert.match(container.textContent ?? '', /private-secret/)
    } finally {
      await act(async () => root.unmount())
    }
  })
})
