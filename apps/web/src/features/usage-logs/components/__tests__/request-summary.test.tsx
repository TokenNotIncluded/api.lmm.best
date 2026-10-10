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
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { Window } from 'happy-dom'
import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'

import { usageLogSchema } from '../../data/schema'
import type { LogOtherData } from '../../types'
import { LogRequestSummary } from '../log-request-summary'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  fallbackLng: 'en',
  resources: { en: { translation: {} } },
})
const render = (type: number, other: LogOtherData | null = {}) =>
  renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <LogRequestSummary
        log={usageLogSchema.parse({
          id: 1,
          user_id: 1,
          created_at: 1,
          type,
          content: 'error',
          quota: 12,
        })}
        other={other}
        onClose={() => {}}
      />
    </I18nextProvider>
  )

function summaryRows(html: string) {
  const window = new Window()
  window.document.body.innerHTML = html
  const rows = Object.fromEntries(
    [...window.document.querySelectorAll('dt')].map((label) => [
      label.textContent,
      label.nextElementSibling?.textContent,
    ])
  )
  window.close()
  return rows
}

test('error receipt cannot claim final zero charge or a completed refund', () => {
  const html = render(5, { status_code: 429 })
  assert.match(html, /Not recorded/)
  assert.match(html, /Final net charge is not available here/)
  assert.match(html, /does not identify whether/)
  assert.doesNotMatch(html, /Refund recorded in this entry/)
})

test('subscription and refund receipts have distinct accounting labels', () => {
  assert.match(
    render(2, { billing_source: 'subscription', subscription_consumed: 99 }),
    /Plan quota used in this entry/
  )
  assert.match(render(6), /Refund recorded in this entry/)
})

test('reported cache splits retain measured zero and use only their own counts', () => {
  const rows = summaryRows(
    render(2, {
      cache_tokens: 9000,
      cache_read_details_status: 'reported',
      cache_text_tokens: 1200,
      cache_image_tokens: 500,
      cache_audio_tokens: 0,
    })
  )
  assert.equal(rows['Cache read tokens'], '9.0K')
  assert.equal(rows['Text cache read tokens'], '1.2K')
  assert.equal(rows['Image cache read tokens'], '500')
  assert.equal(rows['Audio cache read tokens'], '0')
})

test('aggregate cache counts do not imply a native split for historical logs', () => {
  const rows = summaryRows(render(2, { cache_tokens: 4200 }))
  assert.equal(rows['Cache read tokens'], '4.2K')
  assert.equal(rows['Text cache read tokens'], undefined)
  assert.equal(rows['Image cache read tokens'], undefined)
  assert.equal(rows['Audio cache read tokens'], undefined)
})

for (const status of ['unknown', 'invalid'] as const) {
  test(`${status} cache split status cannot claim measured counts`, () => {
    const rows = summaryRows(
      render(2, {
        cache_tokens: 1000,
        cache_read_details_status: status,
        cache_text_tokens: 1000,
        cache_image_tokens: 0,
        cache_audio_tokens: 0,
      })
    )
    for (const label of [
      'Text cache read tokens',
      'Image cache read tokens',
      'Audio cache read tokens',
    ]) {
      assert.equal(rows[label], 'Not recorded')
    }
  })
}

test('missing reported cache components remain unrecorded individually', () => {
  const rows = summaryRows(
    render(2, {
      cache_tokens: 1500,
      cache_read_details_status: 'reported',
      cache_text_tokens: 200,
      cache_image_tokens: null,
    })
  )
  assert.equal(rows['Text cache read tokens'], '200')
  assert.equal(rows['Image cache read tokens'], 'Not recorded')
  assert.equal(rows['Audio cache read tokens'], 'Not recorded')
})

test('invalid reported cache numbers stay unrecorded', () => {
  for (const count of [
    -1,
    0.5,
    Number.NaN,
    Number.POSITIVE_INFINITY,
    '10',
    true,
  ]) {
    const rows = summaryRows(
      render(2, {
        cache_read_details_status: 'reported',
        cache_text_tokens: count as number,
        cache_image_tokens: count as number,
        cache_audio_tokens: count as number,
      })
    )
    assert.equal(rows['Text cache read tokens'], 'Not recorded')
    assert.equal(rows['Image cache read tokens'], 'Not recorded')
    assert.equal(rows['Audio cache read tokens'], 'Not recorded')
  }
})

test('audio duration preserves measured zero and identifies estimates', () => {
  const reported = summaryRows(
    render(2, { audio_seconds: 0, audio_usage_status: 'reported' })
  )
  assert.equal(reported['Audio duration (seconds) (Reported)'], '0')
  const estimated = summaryRows(
    render(2, { audio_seconds: 12.5, audio_usage_status: 'estimated' })
  )
  assert.equal(estimated['Audio duration (seconds) (Estimated)'], '12.5')
})

test('audio duration status without a valid duration remains unrecorded', () => {
  for (const duration of [
    undefined,
    null,
    -1,
    Number.NaN,
    Number.POSITIVE_INFINITY,
    '1.5',
    true,
  ]) {
    const rows = summaryRows(
      render(2, {
        audio_seconds: duration as number | null | undefined,
        audio_usage_status: 'reported',
      })
    )
    assert.equal(rows['Audio duration (seconds) (Reported)'], 'Not recorded')
  }
  const missingStatus = summaryRows(render(2, { audio_seconds: 0 }))
  assert.equal(missingStatus['Audio duration (seconds)'], 'Not recorded')
  const historical = summaryRows(render(2))
  assert.equal(historical['Audio duration (seconds)'], undefined)
})
