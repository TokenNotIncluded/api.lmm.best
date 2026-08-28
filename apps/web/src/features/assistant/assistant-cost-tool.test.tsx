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
  'HTMLButtonElement',
  'HTMLInputElement',
  'HTMLSelectElement',
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
  'scrollTo',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { AssistantCostTool } = await import('./assistant-cost-tool')

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

const pricingFixture = {
  success: true,
  data: [
    {
      id: 1,
      model_name: 'deepseek-v4-flash',
      quota_type: 0,
      model_ratio: 1.5,
      completion_ratio: 2,
      enable_groups: ['all'],
    },
  ],
  vendors: [],
  group_ratio: { default: 1, vip: 0.5 },
  usable_group: {
    default: { desc: 'Default', ratio: 1 },
    vip: { desc: 'VIP', ratio: 0.5 },
  },
  supported_endpoint: {},
  auto_groups: [],
}

async function flushQueries() {
  await new Promise((resolve) => setTimeout(resolve, 25))
}

async function renderTool(developerAccessGranted: boolean) {
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
          <AssistantCostTool developerAccessGranted={developerAccessGranted} />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flushQueries()
  })
  await act(flushQueries)
  return { container, queryClient, root }
}

function findButton(text: string): HTMLButtonElement {
  const button = [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
  ].find((candidate) => candidate.textContent?.includes(text))
  assert.ok(button, `Could not find button containing ${text}`)
  return button
}

async function unmount(rendered: Awaited<ReturnType<typeof renderTool>>) {
  await act(async () => rendered.root.unmount())
  rendered.queryClient.clear()
  rendered.container.remove()
}

afterEach(() => {
  api.get = originalGet
  document.body.replaceChildren()
})
after(() => domWindow.close())

describe('AssistantCostTool', () => {
  test('formats live costs with internal Chinese locale codes', async () => {
    api.get = (async (url: string) => {
      assert.equal(url, '/api/assistant/pricing')
      return { data: pricingFixture }
    }) as typeof api.get

    await i18n.changeLanguage('zhCN')
    const rendered = await renderTool(true)
    try {
      assert.match(rendered.container.textContent ?? '', /\$0\.36 \(Platform\)/)
    } finally {
      await unmount(rendered)
      await i18n.changeLanguage('en')
    }
  })

  test('keeps live pricing unavailable while L0 is under review', async () => {
    let calls = 0
    api.get = (async (url: string) => {
      assert.equal(url, '/api/assistant/pricing')
      calls += 1
      return { data: pricingFixture }
    }) as typeof api.get

    const rendered = await renderTool(false)
    assert.equal(calls, 0)
    assert.match(
      rendered.container.textContent ?? '',
      /read-only while your L1 request is under review/
    )
    assert.doesNotMatch(rendered.container.textContent ?? '', /\$0\.3600/)
    await unmount(rendered)
  })

  test('expands all usable groups and recovers from a pricing error', async () => {
    let calls = 0
    api.get = (async (url: string) => {
      assert.equal(url, '/api/assistant/pricing')
      calls += 1
      if (calls === 1) throw new Error('offline')
      return { data: pricingFixture }
    }) as typeof api.get

    const rendered = await renderTool(true)
    assert.match(
      rendered.container.textContent ?? '',
      /Unable to load live pricing/
    )

    await act(async () => {
      findButton('Retry').click()
      await flushQueries()
    })
    await act(flushQueries)

    const groupSelect = rendered.container.querySelector<HTMLSelectElement>(
      '#assistant-cost-group'
    )
    assert.ok(groupSelect)
    assert.deepEqual(
      [...groupSelect.options].map((option) => option.textContent),
      ['Default', 'VIP']
    )
    assert.match(rendered.container.textContent ?? '', /\$0\.36 \(Platform\)/)

    await act(async () => {
      groupSelect.value = 'vip'
      groupSelect.dispatchEvent(new Event('change', { bubbles: true }))
      await flushQueries()
    })
    assert.match(rendered.container.textContent ?? '', /\$0\.18 \(Platform\)/)
    assert.equal(calls, 2)
    await unmount(rendered)
  })
})
