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
/*
Copyright (C) 2026 LIghtJUNction
*/
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createInstance } from 'i18next'
import type { ReactNode } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'

mock.module('@tanstack/react-router', () => ({
  getRouteApi: () => ({
    useSearch: () => ({ hours: 24 }),
    useNavigate: () => () => {},
  }),
}))
mock.module('@/features/forge/forge-public-shell', () => ({
  ForgePublicShell: ({ children }: { children: ReactNode }) => <>{children}</>,
}))
mock.module('@/features/pricing/hooks/use-pricing-data', () => ({
  usePricingData: () => ({ models: [] }),
}))
let latestTimestamp: number | null = 1_790_000_000
mock.module('./hooks/use-status-detection', () => ({
  useStatusDetection: () => ({
    groups: [],
    availableModels: [],
    availableGroups: [],
    hours: 24,
    latestTimestamp,
    modelsWithData: 0,
    modelCount: 0,
    isLoading: false,
    isFetching: false,
    error: null,
    refresh: async () => {},
  }),
}))
const { StatusDetection } = await import('./index')

test('status freshness renders across interface language changes and timestamp units', async () => {
  const i18n = createInstance()
  await i18n.init({
    lng: 'en',
    fallbackLng: false,
    resources: {},
    interpolation: { escapeValue: false },
  })
  for (const [language, expectedLocale] of [
    ['en', 'en'],
    ['zhCN', 'zh-CN'],
    ['zhTW', 'zh-TW'],
    ['ja', 'ja'],
    ['invalid_locale', undefined],
    ['en', 'en'],
  ] as const) {
    await i18n.changeLanguage(language)
    for (const timestamp of [1_790_000_000, 1_790_000_000_000]) {
      latestTimestamp = timestamp
      const markup = renderToStaticMarkup(
        <I18nextProvider i18n={i18n}>
          <StatusDetection />
        </I18nextProvider>
      )
      const expected = new Date(1_790_000_000_000).toLocaleString(
        expectedLocale
      )
      assert.ok(markup.includes(`Latest data: ${expected}`), language)
    }
  }
  for (const timestamp of [null, Number.NaN]) {
    latestTimestamp = timestamp
    const markup = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <StatusDetection />
      </I18nextProvider>
    )
    assert.ok(markup.includes('Performance window: last 24 hours'))
  }
})
