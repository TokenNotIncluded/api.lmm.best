/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import type { TodoItem } from './api'
import { ModerationNoticeDetails } from './moderation-notice-details'
import { todoItemTitleKey } from './todo-labels'

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

function render(
  details: Record<string, unknown>,
  category: TodoItem['category'] = 'moderation'
) {
  const item: TodoItem = {
    id: 'moderation:7',
    source_id: 7,
    category,
    type: 'moderation_warning',
    title: 'moderation.warning',
    summary: 'A safety review found flagged user input.',
    read: false,
    created_at: 1,
    updated_at: 1,
    details,
  }
  return renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <ModerationNoticeDetails item={item} />
    </I18nextProvider>
  )
}

describe('Moderation notice metadata', () => {
  test('uses a readable translated warning title', () => {
    assert.equal(
      todoItemTitleKey('moderation.warning'),
      'Safety review warning'
    )
  })

  test('renders whitelisted identifiers and fees without input or arbitrary metadata', () => {
    const html = render({
      request_id: 'reviewed-request',
      mode: 'strict',
      source: 'relay_input',
      categories: ['hate', 'violence', null, 7],
      fee_record_id: 13,
      requested_quota: 100000,
      charged_quota: 0,
      input: 'private-input-must-not-render',
      payload: 'private-payload-must-not-render',
      error: 'private-error-must-not-render',
    })
    assert.match(html, /reviewed-request/)
    assert.match(html, /Strict mode/)
    assert.match(html, /API user input/)
    assert.match(html, /hate, violence/)
    assert.match(html, /Wallet deduction/)
    assert.match(html, /Requested category fee/)
    assert.match(html, /Fee record ID/)
    assert.match(html, /13/)
    assert.doesNotMatch(html, /private-|null, 7/)
  })

  test('distinguishes assistant output warnings from a user violation', () => {
    const html = render({
      source: 'assistant_output',
      mode: 'tolerant',
      categories: ['violence'],
      charged_quota: 0,
      requested_quota: 0,
    })
    assert.match(html, /Assistant output/)
    assert.match(html, /your wallet and risk score are unchanged/)
    assert.doesNotMatch(html, /Requested category fee|Fee record ID/)
  })

  test('omits invalid metadata and does not affect unrelated notifications', () => {
    const html = render({
      request_id: 42,
      mode: 'unknown',
      categories: 'not-an-array',
      charged_quota: -1,
      requested_quota: Number.NaN,
      fee_record_id: 0.5,
    })
    assert.doesNotMatch(html, /Request ID|Wallet deduction|Fee record ID|NaN/)
    assert.equal(render({ request_id: 'any' }, 'account_action'), '')
  })
})
