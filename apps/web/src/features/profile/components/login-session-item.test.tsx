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
import { test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import dayjs from '@/lib/dayjs'
import type { LoginSession } from '@/stores/auth-store'

import { LoginSessionItem } from './login-session-item'

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Last active {{time}} · Expires {{expires}}': 'Expires {{expires}}',
      },
    },
  },
})

const session: LoginSession = {
  sid: 'session-expiry-test',
  current: true,
  login_method: 'password',
  ip: '127.0.0.1',
  user_agent: 'Mozilla/5.0 Chrome/100.0 Linux',
  created_at: 1_700_000_000,
  last_active_at: 1_700_500_000,
  expires_at: 1_702_592_000,
}

function renderExpiry(value: LoginSession, enabled?: boolean) {
  return renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <LoginSessionItem
        session={value}
        sessionAutoLogout={enabled}
        onRevoke={() => {}}
      />
    </I18nextProvider>
  )
}

function expiryText(timestamp: number) {
  return `Expires ${dayjs.unix(timestamp).format('YYYY-MM-DD HH:mm')}`
}

test('device row renders the weekly deadline by default', () => {
  const html = renderExpiry(session)
  assert.ok(html.includes(expiryText(session.created_at + 604_801)))
  assert.ok(!html.includes(expiryText(session.expires_at)))
  assert.ok(html.includes('Current'))
})

test('device row honors opt-out and explicit re-enable', () => {
  const absolute = expiryText(session.expires_at)
  const weekly = expiryText(session.created_at + 604_801)
  assert.ok(renderExpiry(session, false).includes(absolute))
  assert.ok(renderExpiry(session, true).includes(weekly))
})

test('other devices and earlier absolute deadlines use the same policy', () => {
  const other = {
    ...session,
    current: false,
    expires_at: session.created_at + 60,
  }
  assert.ok(renderExpiry(other, true).includes(expiryText(other.expires_at)))
  assert.ok(renderExpiry(other, false).includes(expiryText(other.expires_at)))
})
