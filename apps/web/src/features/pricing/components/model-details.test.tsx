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
*/
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'https://console.example.test/pricing' })
domWindow.document.write(
  '<!doctype html><html><head></head><body></body></html>'
)
Object.defineProperty(domWindow.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'history',
  'location',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'SVGElement',
  'customElements',
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
  'scrollTo',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.defineProperty(globalThis, 'matchMedia', {
  configurable: true,
  value: (media: string) => ({
    matches: false,
    media,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent() {
      return false
    },
  }),
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { ModelDetailsContent } = await import('./model-details')

const originalGet = api.get
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

async function renderModelDetails(modelName = 'free-model') {
  api.get = (async (url: string) => {
    assert.equal(url, '/api/perf-metrics')
    return { data: { data: { groups: [] } } }
  }) as typeof api.get

  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <ModelDetailsContent
            model={{
              id: 1,
              model_name: modelName,
              quota_type: 0,
              model_ratio: 1,
              completion_ratio: 1,
              enable_groups: ['free'],
            }}
            groupRatio={{ free: 0 }}
            usableGroup={{ free: { desc: 'Free group', ratio: 0 } }}
            endpointMap={{}}
            autoGroups={[]}
            priceRate={1}
            usdExchangeRate={1}
            tokenUnit='M'
          />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flushEffects()
  })
  return { container, queryClient, root }
}

async function unmount(
  rendered: Awaited<ReturnType<typeof renderModelDetails>>
) {
  await act(async () => rendered.root.unmount())
  rendered.queryClient.clear()
  rendered.container.remove()
}

afterEach(() => {
  api.get = originalGet
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('ModelDetails group pricing', () => {
  test('does not invent availability when no status observations exist', async () => {
    const rendered = await renderModelDetails()
    const status = rendered.container.querySelector(
      '[aria-label="Model availability"]'
    )
    assert.ok(status)
    assert.match(status.textContent ?? '', /No recent model status/)
    assert.match(status.textContent ?? '', /Sign in to check model access/)
    assert.doesNotMatch(status.textContent ?? '', /Recent calls succeeded/)
    await unmount(rendered)
  })

  test('offers a direct add-funds path from the mobile-friendly detail header', async () => {
    const rendered = await renderModelDetails()

    const addFundsLink = rendered.container.querySelector('a[href="/wallet"]')
    assert.ok(addFundsLink)
    assert.equal(addFundsLink.textContent?.includes('Add Funds'), true)

    const copyModelButton = rendered.container.querySelector(
      '[aria-label="Copy model name"]'
    )
    assert.ok(copyModelButton)
    assert.match(copyModelButton.className, /\bsize-11\b/)

    await unmount(rendered)
  })

  test('renders a configured zero group ratio as zero', async () => {
    const rendered = await renderModelDetails()

    assert.match(rendered.container.textContent ?? '', /0x/)
    assert.doesNotMatch(
      rendered.container.textContent ?? '',
      /free-model[\s\S]*1x/i
    )

    await unmount(rendered)
  })

  test('shows an accessible request estimate with editable token defaults', async () => {
    const rendered = await renderModelDetails()
    const title = [...rendered.container.querySelectorAll('h3')].find(
      (element) => element.textContent === 'Request cost estimate'
    )
    assert.ok(title)
    const section = title.closest('section')
    assert.ok(section)
    assert.equal(section.getAttribute('aria-labelledby'), title.id)
    const inputs = [...section.querySelectorAll('input')]
    assert.deepEqual(
      inputs.map((input) => input.value),
      ['10000', '2000', '0']
    )
    for (const input of inputs) {
      assert.ok(
        [...section.querySelectorAll('label')].some(
          (label) => label.htmlFor === input.id
        )
      )
    }
    assert.equal(section.querySelector('select')?.value, 'free')
    assert.doesNotMatch(
      section.querySelector('[aria-live]')?.textContent ?? '',
      /unavailable/
    )
    await unmount(rendered)
  })

  test('wraps long model IDs inside the narrow detail header', async () => {
    const modelName =
      'provider/model-with-a-very-long-identifier-that-must-remain-readable-on-mobile'
    const rendered = await renderModelDetails(modelName)

    const title = rendered.container.querySelector('h1')
    assert.ok(title)
    assert.equal(title.textContent, modelName)
    assert.match(title.className, /min-w-0/)
    assert.match(title.className, /flex-1/)
    assert.match(title.className, /\[overflow-wrap:anywhere\]/)

    await unmount(rendered)
  })
})
