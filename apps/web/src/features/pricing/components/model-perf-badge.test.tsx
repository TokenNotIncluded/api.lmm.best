/*
Copyright (C) 2026 LIghtJUNction

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
import { describe, test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import type { ModelPerfBadgeData } from '../lib/model-perf'
import { ModelPerfBadge } from './model-perf-badge'

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'All Groups': 'All Groups',
        'Average latency': 'Average latency',
        'Latency short': 'Latency',
        'Status short': 'Status',
        'Success rate': 'Success rate',
        Throughput: 'Throughput',
        'Throughput short': 'TPS',
      },
    },
  },
})

function renderBadge(perf?: ModelPerfBadgeData) {
  return renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <ModelPerfBadge perf={perf} />
    </I18nextProvider>
  )
}

describe('ModelPerfBadge', () => {
  test('shows real latency, token throughput, and textual health status', () => {
    const html = renderBadge({
      avg_latency_ms: 420,
      avg_tps: 37.5,
      success_rate: 98.5,
      recent_success_rates: [100, 96, 98.5],
    })

    assert.match(html, />Latency</)
    assert.match(html, />420ms</)
    assert.match(html, />TPS</)
    assert.match(html, />37\.5 t\/s</)
    assert.match(html, />Status</)
    assert.match(html, />98\.5%</)
    assert.match(html, /aria-label="Average latency[^"]*420ms"/)
    assert.match(html, /aria-label="Success rate[^"]*98\.5%"/)
    assert.doesNotMatch(html, /class="[^"]*\bhidden\b/)
  })

  test('keeps all three attributes visible when metrics are unavailable', () => {
    const html = renderBadge()

    assert.match(html, />Latency</)
    assert.match(html, />TPS</)
    assert.match(html, />Status</)
    assert.equal(html.match(/>—</g)?.length, 3)
    assert.doesNotMatch(html, /class="[^"]*\bhidden\b/)
  })
})
