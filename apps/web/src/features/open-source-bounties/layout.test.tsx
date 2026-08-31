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

const domWindow = new Window()
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
  'HTMLElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
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
const { OpenSourceBounties } = await import('./index')

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

const description =
  'A complete bounty description that must remain readable instead of being clamped after three lines.'
const ownerUsername = `maintainer${'x'.repeat(96)}`
const title = `Fix${'x'.repeat(128)}`
const project = {
  id: 42,
  owner_user_id: 21,
  owner_username: ownerUsername,
  repository_url: 'https://github.com/example/a-very-long-repository-name',
  title,
  description,
  rules: 'Submit a focused fix with tests.',
  reward_quota: 500_000,
  net_reward_quota: 450_000,
  reward_slots: 3,
  escrow_quota: 1_350_000,
  platform_fee_rate_bps: 1_000,
  platform_fee_quota: 150_000,
  status: 'published',
  created_at: 1,
  updated_at: 1,
  published_at: 1,
  closed_at: 0,
  archived_at: 0,
  active_challenge_count: 1,
  approved_challenge_count: 0,
  owner_rating_average: 4.8,
  owner_rating_count: 18,
  owner_thank_heart_count: 12,
}

async function flushQueries() {
  await new Promise((resolve) => setTimeout(resolve, 20))
}

afterEach(() => {
  api.get = originalGet
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('open-source bounty layout', () => {
  test('keeps all view tabs readable and renders complete descriptions', async () => {
    api.get = (async (url: string) => {
      let data: unknown = []
      if (url === '/api/status') {
        data = {
          backend_capabilities: {
            bounty_public_read: true,
            bounty_challenge_cancel: true,
          },
        }
      } else if (url === '/api/open-source-bounties?page=1&page_size=50') {
        data = { items: [project], total: 1, page: 1, page_size: 50 }
      } else if (url.startsWith('/api/todos?')) {
        data = { total: 0 }
      } else if (url === '/api/open-source-bounties/config') {
        data = { rate_percent: 10, rate_basis_points: 1_000 }
      } else if (url === '/api/open-source-bounties/mcp-token') {
        data = {
          status: {
            configured: false,
            token_hint: '',
            created_at: 0,
            last_used_at: 0,
          },
          endpoint: '/mcp',
          protocol_version: '2026-07-28',
        }
      }
      return { data: { success: true, data } }
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
            <OpenSourceBounties />
          </I18nextProvider>
        </QueryClientProvider>
      )
      await flushQueries()
    })

    const tabsList = container.querySelector<HTMLElement>(
      '[data-slot="tabs-list"]'
    )
    assert.ok(tabsList)
    assert.match(tabsList.className, /(?:^|\s)grid(?:\s|$)/)
    assert.match(tabsList.className, /group-data-horizontal\/tabs:!h-auto/)

    const tabs = [...container.querySelectorAll<HTMLElement>('[role="tab"]')]
    assert.equal(tabs.length, 6)
    for (const tab of tabs) {
      assert.match(tab.className, /(?:^|\s)min-h-11(?:\s|$)/)
      assert.match(tab.className, /(?:^|\s)whitespace-normal(?:\s|$)/)
    }

    const titleElement = [
      ...container.querySelectorAll<HTMLElement>('[data-slot="card-title"]'),
    ].find((element) => element.textContent === title)
    assert.ok(titleElement)
    assert.match(titleElement.className, /\[overflow-wrap:anywhere\]/)

    const ownerElement = [
      ...container.querySelectorAll<HTMLElement>(
        '[data-slot="card-description"]'
      ),
    ].find((element) => element.textContent?.startsWith(ownerUsername))
    assert.ok(ownerElement)
    assert.match(ownerElement.className, /\[overflow-wrap:anywhere\]/)

    const descriptionElement = [...container.querySelectorAll('p')].find(
      (element) => element.textContent === description
    )
    assert.ok(descriptionElement)
    assert.match(descriptionElement.className, /whitespace-pre-wrap/)
    assert.match(descriptionElement.className, /\[overflow-wrap:anywhere\]/)
    assert.doesNotMatch(descriptionElement.className, /line-clamp/)

    await act(async () => root.unmount())
    queryClient.clear()
  })
})
