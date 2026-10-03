/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'

import { AdvancedCustomBalanceFields } from './advanced-custom-balance-fields'

test('balance editor exposes GET by default, optional POST body and extraction fields with associated labels', async () => {
  const i18n = createInstance()
  await i18n.init({ lng: 'en', resources: {}, fallbackLng: false })
  const render = (method?: 'GET' | 'POST') =>
    renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <AdvancedCustomBalanceFields
          value={
            method
              ? { method, json_pointer: '/data/balance', scale: 0.01 }
              : undefined
          }
          onChange={() => {}}
        />
      </I18nextProvider>
    )
  const defaultHTML = render()
  assert.ok(defaultHTML.includes('GET'))
  assert.ok(
    defaultHTML.includes(
      'Rust does not support Advanced Custom balance queries.'
    )
  )
  assert.ok(!defaultHTML.includes('POST body template'))
  const scaleInput = defaultHTML.match(/<input\b[^>]*type="number"[^>]*>/)?.[0]
  assert.ok(scaleInput?.includes('disabled=""'))
  const postHTML = render('POST')
  assert.ok(postHTML.includes('POST body template'))
  assert.ok(postHTML.includes('/data/balance'))
  assert.ok(postHTML.includes('value="0.01"'))
  assert.match(postHTML, /<label[^>]*for="[^"]+-pointer"/)
})
