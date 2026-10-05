/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'
import { useWalletCurrencyPreferenceStore } from '@/stores/wallet-currency-preference-store'

import { ModerationReviewRow } from './moderation-audit-panel'
import type { ModerationReview } from './security-audit-types'

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const base: ModerationReview = {
  id: 1,
  created_at: 1,
  user_id: 7,
  group: 'default',
  source_kind: 'openai_moderation',
  source: 'relay_input',
  mode: 'strict',
  status: 'completed',
  flagged: true,
  categories: ['harassment'],
  review_model: 'omni-moderation-latest',
  request_id: 'req-test',
  fee_status: 'charged',
  fee_category: 'harassment',
  requested_quota: 100,
  charged_quota: 100,
  fee_record_id: 3,
  input_truncated: false,
  attempts: 1,
  completed_at: 2,
}
const render = (review: ModerationReview) =>
  renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <ModerationReviewRow review={review} />
    </I18nextProvider>
  )

const originalConfig = useSystemConfigStore.getState().config
const originalPreference =
  useWalletCurrencyPreferenceStore.getState().preference
after(() => {
  useSystemConfigStore.getState().setConfig(originalConfig)
  useWalletCurrencyPreferenceStore.getState().setPreference(originalPreference)
})

test('review deductions always use ledger USD across wallet display preferences', async () => {
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      currencyUnit: 'credit',
      creditsPerUsd: 3359744,
      creditsPerUsdExact: '3359744',
      cnyPerUsd: 7,
      cnyPerUsdExact: '7',
    },
  })
  const { Window } = await import('happy-dom')
  const window = new Window()
  for (const key of [
    'window',
    'document',
    'navigator',
    'HTMLElement',
    'SVGElement',
    'Node',
    'Element',
    'Event',
    'MutationObserver',
  ] as const) {
    Object.defineProperty(globalThis, key, {
      configurable: true,
      value: window[key],
    })
  }
  const { act } = await import('react')
  const { createRoot } = await import('react-dom/client')
  const testGlobals = globalThis as typeof globalThis & {
    IS_REACT_ACT_ENVIRONMENT?: boolean
  }
  testGlobals.IS_REACT_ACT_ENVIRONMENT = true
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  try {
    await act(async () =>
      root.render(
        <I18nextProvider i18n={i18n}>
          <ModerationReviewRow
            review={{
              ...base,
              requested_quota: 3359744,
              charged_quota: 1679872,
            }}
          />
        </I18nextProvider>
      )
    )
    for (const preference of ['USD', 'CNY', 'CREDIT'] as const) {
      await act(async () =>
        useWalletCurrencyPreferenceStore.getState().setPreference(preference)
      )
      assert.match(container.innerHTML, />1 USD</)
      assert.match(container.innerHTML, />0\.5 USD</)
      assert.doesNotMatch(container.innerHTML, /CNY|Credits/)
    }
  } finally {
    await act(async () => root.unmount())
    container.remove()
    window.close()
  }
})

test('shows durable metadata and category effects without rendering request text', () => {
  const html = render({
    ...base,
    payload: 'PRIVATE_REQUEST_TEXT',
  } as ModerationReview)
  assert.match(html, /API input/)
  assert.match(html, /Flagged/)
  assert.match(html, /Strict mode/)
  assert.match(html, /Harassment/)
  assert.match(html, /req-test/)
  assert.match(html, /Wallet deduction/)
  assert.match(html, /Fee record ID/)
  assert.doesNotMatch(html, /PRIVATE_REQUEST_TEXT/)
})

test('does not label failed or pending reviews as clear or violating', () => {
  for (const status of ['pending', 'failed'] as const) {
    const html = render({ ...base, status, flagged: false })
    assert.match(html, new RegExp(status === 'pending' ? 'Pending' : 'Failed'))
    assert.doesNotMatch(html, />Clear<|>Flagged</)
  }
})

test('shows captured private and paired upstream IDs while empty historical or restricted rows stay empty', () => {
  const html = render({
    ...base,
    subject_identifier: 'a'.repeat(64),
    provider_calls: [
      {
        attempt: 1,
        batch_index: 1,
        response_id: 'modr-first',
        request_id: 'req_first',
      },
      { attempt: 2, batch_index: 1, response_id: 'modr-retry', request_id: '' },
    ],
  })
  assert.match(html, /Private user identifier/)
  assert.match(html, /Upstream moderation calls/)
  assert.match(html, /modr-first/)
  assert.match(html, /req_first/)
  assert.match(html, /Attempt 2, batch 1/)
  assert.match(html, /modr-retry/)
  const historical = render({
    ...base,
    subject_identifier: '',
    provider_calls: [],
  })
  assert.doesNotMatch(
    historical,
    /Private user identifier|Upstream moderation calls|modr-first|req_first/
  )
})

test('output warnings state that users are excluded from penalties and risk scoring', () => {
  const html = render({
    ...base,
    source: 'assistant_output',
    mode: 'tolerant',
    requested_quota: 0,
    charged_quota: 0,
    fee_record_id: 0,
    fee_status: 'none',
  })
  assert.match(html, /Assistant output/)
  assert.match(html, /excluded from user penalties and risk scoring/)
  assert.doesNotMatch(html, /Fee record ID/)
})
