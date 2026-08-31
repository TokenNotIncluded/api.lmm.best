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
import { readFileSync } from 'node:fs'
import { describe, test } from 'node:test'

const columnsSource = readFileSync(
  new URL('./pricing-columns.tsx', import.meta.url),
  'utf8'
)
const tableSource = readFileSync(
  new URL('./pricing-table.tsx', import.meta.url),
  'utf8'
)
const hookSource = readFileSync(
  new URL('../hooks/use-perf-map.ts', import.meta.url),
  'utf8'
)
const indexSource = readFileSync(
  new URL('../index.tsx', import.meta.url),
  'utf8'
)

describe('Pricing perf columns', () => {
  test('table exposes latency / throughput / status columns', () => {
    assert.match(columnsSource, /id: 'perf_latency'/)
    assert.match(columnsSource, /id: 'perf_throughput'/)
    assert.match(columnsSource, /id: 'perf_status'/)
    assert.match(columnsSource, /meta: \{ label: t\('Latency'\) \}/)
    assert.match(columnsSource, /meta: \{ label: t\('Throughput'\) \}/)
    assert.match(columnsSource, /meta: \{ label: t\('Status'\) \}/)
    assert.match(
      columnsSource,
      /id: 'perf_latency'[\s\S]*?enableSorting: false/
    )
    assert.match(
      columnsSource,
      /id: 'perf_throughput'[\s\S]*?enableSorting: false/
    )
    assert.match(columnsSource, /id: 'perf_status'[\s\S]*?enableSorting: false/)
  })

  test('perf columns share the card formatting and status semantics', () => {
    assert.match(columnsSource, /getModelPerfDisplay/)
    assert.match(columnsSource, /<ModelPerfStatus perf=\{perf\} \/>/)
  })

  test('perf columns use plain non-sortable headers', () => {
    assert.match(
      columnsSource,
      /id: 'perf_latency'[\s\S]*?header: t\('Latency'\)/
    )
    assert.match(
      columnsSource,
      /id: 'perf_throughput'[\s\S]*?header: t\('Throughput'\)/
    )
  })

  test('perfMap is threaded from the page into both views', () => {
    assert.match(hookSource, /getPerfMetricsSummary\(PERF_SUMMARY_HOURS\)/)
    assert.match(hookSource, /buildPerfMap/)
    assert.match(
      tableSource,
      /perfMap\?: ReadonlyMap<string, ModelPerfBadgeData>/
    )
    assert.match(tableSource, /perfMap,/)
    assert.match(indexSource, /usePerfMap\(\)/)
    assert.match(indexSource, /perfMap=\{perfMap\}/)
    assert.match(hookSource, /'perf-metrics-summary'/)
  })
})
