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
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({ url: 'https://console.example.test/' })
dom.document.write('<!doctype html><html><body></body></html>')
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Element',
  'Node',
  'Event',
  'MutationObserver',
  'localStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: key === 'window' ? dom : dom[key],
  })
}
Object.assign(globalThis, {
  IS_REACT_ACT_ENVIRONMENT: true,
  __LMM_PERSONA_DEBUG__: false,
})
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { initReactI18next } = await import('react-i18next')
await createInstance()
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const { api } = await import('@/lib/api')
const { AcquisitionFunnelPanel } = await import('./funnel-panel')
const { AcquisitionVisitorPanel } = await import('./visitor-panel')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
after(() => dom.close())

test('partial visitor coverage and unknown funnel evidence remain explicit', async () => {
  const originalGet = api.get
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  api.get = (async (path: string) => ({
    data: {
      success: true,
      data: path.includes('/visitors?')
        ? {
            available_from: 100,
            coverage_complete: false,
            observed_visitors: 2,
            channels: [{ source: 'unknown', visitors: 2 }],
          }
        : {
            observed_until: 200,
            observation_days: 30,
            unavailable: ['client_configured'],
            payment_snapshot_updated_at: 0,
            payment_snapshot_status: 'unavailable',
            first_payment_sources: [],
            channels: [
              {
                source: 'documentation',
                registrations: 3,
                mature_accounts: 1,
                observing_accounts: 2,
                stages: [
                  {
                    id: 'client_configured',
                    observed: 0,
                    not_observed: 0,
                    unknown: 3,
                    mature_observed: 0,
                    mature_known: 0,
                    observing: 2,
                    conversion_rate: null,
                    timing_accounts: 0,
                    mean_seconds_from_registration: null,
                  },
                ],
              },
            ],
          },
    },
  })) as typeof api.get
  try {
    await act(async () =>
      root.render(
        <QueryClientProvider client={client}>
          <AcquisitionVisitorPanel from={1} to={200} />
          <AcquisitionFunnelPanel from={1} to={200} />
        </QueryClientProvider>
      )
    )
    for (let i = 0; i < 30 && container.textContent?.includes('Loading'); i++) {
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 20))
      })
    }
    assert.match(
      container.textContent || '',
      /Visitor records do not cover this entire period/
    )
    assert.match(
      container.textContent || '',
      /Unknown accounts are not failed conversions/
    )
    assert.match(container.textContent || '', /0 \/ 0 · Unknown/)
    assert.doesNotMatch(container.textContent || '', /0\.0%/)
  } finally {
    await act(async () => root.unmount())
    client.clear()
    container.remove()
    api.get = originalGet
  }
})
