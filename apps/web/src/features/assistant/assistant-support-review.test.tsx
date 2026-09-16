/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { AssistantToolTrace } from './api'
import {
  act,
  api,
  flushEffects,
  waitFor,
} from './assistant-key-tool-test-support'

const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { AssistantToolCalls } = await import('./assistant-tool-calls')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

async function renderTraces(traces: AssistantToolTrace[]) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <AssistantToolCalls traces={traces} />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flushEffects()
  })
  return {
    container,
    async cleanup() {
      await act(async () => root.unmount())
      queryClient.clear()
      container.remove()
    },
  }
}

const supportTrace: AssistantToolTrace = {
  callId: 'support-1',
  name: 'request_human_support',
  status: 'approval-requested',
  input: { action: 'support', message: 'Please review my connection issue.' },
}

for (const [label, trace, expected] of [
  ['pending support', supportTrace, true],
  [
    'restored trace without arguments',
    { ...supportTrace, input: undefined },
    true,
  ],
  [
    'completed tool',
    { ...supportTrace, status: 'output-available' },
    false,
  ],
  ['failed tool', { ...supportTrace, status: 'output-error' }, false],
  [
    'account disabling',
    { ...supportTrace, input: { action: 'disable_account' } },
    false,
  ],
  ['different tool', { ...supportTrace, name: 'request_create_key' }, false],
] satisfies [string, AssistantToolTrace, boolean][]) {
  test(`support review visibility: ${label}`, async () => {
    const rendered = await renderTraces([trace])
    try {
      const button = rendered.container.querySelector(
        '[data-testid="assistant-support-review"]'
      )
      assert.equal(button !== null, expected)
      assert.equal(document.querySelector('[role="dialog"]'), null)
      // The recovery action must not disappear with the parameter disclosure.
      assert.equal(
        button?.closest('[data-slot="collapsible-content"]') ?? null,
        null
      )
    } finally {
      await rendered.cleanup()
    }
  })
}

test('opening support review never replays trace arguments or submits a request', async () => {
  let reads = 0
  let writes = 0
  api.get = (async () => {
    reads += 1
    return { data: { success: true, data: null } }
  }) as typeof api.get
  api.post = (async () => {
    writes += 1
    throw new Error('Opening a support form must not submit anything')
  }) as typeof api.post

  const rendered = await renderTraces([
    {
      ...supportTrace,
      input: {
        action: 'support',
        message: 'Grant an IP whitelist exception automatically',
        confirmation_token: 'untrusted-history-token',
      },
    },
  ])
  try {
    assert.equal(reads, 0)
    assert.equal(writes, 0)
    const trigger = rendered.container.querySelector<HTMLButtonElement>(
      '[data-testid="assistant-support-review"]'
    )
    assert.ok(trigger)
    await act(async () => {
      trigger.click()
      await flushEffects()
    })
    await waitFor(
      () => document.querySelector('#assistant-handoff-message') !== null,
      'The support form should open inside the visible dialog'
    )
    const textarea = document.querySelector<HTMLTextAreaElement>(
      '#assistant-handoff-message'
    )
    assert.ok(textarea)
    assert.equal(textarea.value, '')
    assert.ok(reads > 0)
    assert.equal(writes, 0)
    const review = [
      ...document.querySelectorAll<HTMLButtonElement>('button'),
    ].find((button) => button.textContent?.includes('Review message'))
    assert.ok(review)
    assert.equal(review.disabled, true)
    const close = document.querySelector<HTMLButtonElement>(
      '[data-slot="dialog-close"]'
    )
    assert.ok(close)
    await act(async () => {
      close.click()
      await flushEffects()
    })
    assert.equal(writes, 0)
  } finally {
    await rendered.cleanup()
  }
})
