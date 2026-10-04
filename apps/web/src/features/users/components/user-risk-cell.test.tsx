/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

import type { User } from '../types'

const domWindow = new Window({ url: 'https://console.example.test/users' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'PointerEvent',
  'FocusEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { userSchema } = await import('../types')
const { UserRiskCell } = await import('./user-risk-cell')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })

function user(): User {
  return {
    id: 7,
    username: 'customer',
    display_name: 'Customer',
    quota: 0,
    used_quota: 0,
    request_count: 0,
    group: 'default',
    status: 1,
    role: 1,
    wallet_risk: {
      score: 0.5,
      high_risk: false,
      version: 2,
      reasons: ['moderation_violations'],
      checkin_quota: 0,
      checkin_count: 0,
      transferred_quota: 0,
      pending_quota: 0,
      received_quota: 0,
      high_risk_senders: 0,
      moderation_reviewed_count: 12,
      moderation_flagged_count: 3,
    },
  }
}

async function renderRisk(value: User) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <UserRiskCell user={value} />
      </I18nextProvider>
    )
  })
  const trigger = container.querySelector<HTMLButtonElement>('button')
  assert.ok(trigger)
  await act(async () => {
    trigger.click()
  })
  for (let attempt = 0; attempt < 30; attempt += 1) {
    const popup = document.querySelector<HTMLElement>(
      '[data-slot="popover-content"]'
    )
    if (popup) return { root, popup }
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 5))
    })
  }
  await act(async () => root.unmount())
  throw new Error('Risk details did not open')
}

afterEach(() => document.body.replaceChildren())
after(() => domWindow.close())

describe('Moderation risk details', () => {
  test('keeps the backend review and flagged counts when parsing users', () => {
    const parsed = userSchema.parse(user())
    assert.equal(parsed.wallet_risk?.moderation_reviewed_count, 12)
    assert.equal(parsed.wallet_risk?.moderation_flagged_count, 3)
    assert.deepEqual(parsed.wallet_risk?.reasons, ['moderation_violations'])
  })

  test('shows counts and the user-input reason in the actual risk popover', async () => {
    const rendered = await renderRisk(user())
    try {
      const labels = [...rendered.popup.querySelectorAll('dt')]
      const reviews = labels.find(
        (label) => label.textContent === 'Moderation reviews'
      )
      const flagged = labels.find(
        (label) => label.textContent === 'Flagged user inputs'
      )
      assert.equal(reviews?.nextElementSibling?.textContent, '12')
      assert.equal(flagged?.nextElementSibling?.textContent, '3')
      assert.match(
        rendered.popup.textContent ?? '',
        /Moderation violations in user input/
      )
      assert.match(rendered.popup.textContent ?? '', /Model output is excluded/)
    } finally {
      await act(async () => rendered.root.unmount())
    }
  })

  test('does not fabricate review counts for an older server response', async () => {
    const value = user()
    assert.ok(value.wallet_risk)
    delete value.wallet_risk.moderation_reviewed_count
    delete value.wallet_risk.moderation_flagged_count
    const rendered = await renderRisk(value)
    try {
      assert.doesNotMatch(
        rendered.popup.textContent ?? '',
        /Moderation reviews|Flagged user inputs/
      )
    } finally {
      await act(async () => rendered.root.unmount())
    }
  })
})
