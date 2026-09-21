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
import { describe, test } from 'node:test'

import { renderToStaticMarkup } from 'react-dom/server'

import { SmsServiceIdentity } from './sms-identities'

describe('SMS service identity', () => {
  test('renders the OpenAI brand icon for the HeroSMS dr service', () => {
    const markup = renderToStaticMarkup(
      <SmsServiceIdentity
        service={{ code: 'dr', name: 'OpenAI', popularity: 0 }}
      />
    )

    assert.match(markup, /<title>OpenAI<\/title>/)
    assert.doesNotMatch(markup, />OP<\/span>/)
  })

  test('renders configured library icons without falling back to initials', () => {
    const markup = renderToStaticMarkup(
      <SmsServiceIdentity
        service={{ code: 'tg', name: 'Telegram', popularity: 0 }}
      />
    )

    assert.match(markup, /<svg/)
    assert.doesNotMatch(markup, />TE<\/span>/)
  })

  test('keeps initials as the fallback for an unknown service', () => {
    const markup = renderToStaticMarkup(
      <SmsServiceIdentity
        service={{ code: 'zz', name: 'Example', popularity: 0 }}
      />
    )

    assert.match(markup, />EX<\/span>/)
  })
})
