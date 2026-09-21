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

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'

import { usageLogSchema } from '../../data/schema'
import { LogRequestSummary } from '../log-request-summary'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  fallbackLng: 'en',
  resources: { en: { translation: {} } },
})
const render = (type: number, other = {}) =>
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
