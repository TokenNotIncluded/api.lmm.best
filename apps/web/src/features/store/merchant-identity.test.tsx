/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import { StoreMerchantIdentity } from './merchant-identity'

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })

test('merchant identity links actual seller and explicit contact with escaped names', () => {
  const html = renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <StoreMerchantIdentity
        seller={{
          id: 27,
          username: 'merchant-name',
          display_name: '<img src=x> Shop',
          contact_email: 'public+sales@example.test',
        }}
        sellerId={99}
      />
    </I18nextProvider>
  )
  assert.ok(html.includes('/store?seller_id=27'))
  assert.ok(!html.includes('seller_id=99'))
  assert.ok(html.includes('@merchant-name'))
  assert.ok(html.includes('&lt;img src=x&gt; Shop'))
  assert.ok(!html.includes('<img'))
  assert.ok(html.includes('mailto:public%2Bsales%40example.test'))
})

test('merchant identity keeps missing contact absent and rejects injected mail headers', () => {
  for (const contact_email of [
    undefined,
    'sales@example.test\r\nBcc: other@example.test',
  ]) {
    const html = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <StoreMerchantIdentity
          seller={{
            id: 27,
            username: 'seller',
            display_name: '',
            contact_email,
          }}
        />
      </I18nextProvider>
    )
    assert.ok(!html.includes('mailto:'))
    assert.ok(!html.includes('Bcc:'))
    assert.ok(html.includes('User ID'))
  }
})
