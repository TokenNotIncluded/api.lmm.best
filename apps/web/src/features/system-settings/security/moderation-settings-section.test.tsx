/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'https://console.example.test/settings' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'HTMLTextAreaElement',
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
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { ModerationSettingsSection } =
  await import('./moderation-settings-section')
const { moderationSettingsSchema } =
  await import('./moderation-settings-schema')
;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
).IS_REACT_ACT_ENVIRONMENT = true
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
after(() => domWindow.close())
const defaults = {
  ModerationEnabled: false,
  ModerationPolicyScope: 'account_group' as const,
  ModerationSafetyIdentifierEnabled: false,
  ModerationGroup: 'default',
  ModerationModel: 'omni-moderation-latest' as const,
  ModerationGroupPolicies: '{}',
}

test('rejects unknown policy scopes and keeps the two new controls explicitly disabled by default', () => {
  assert.equal(
    moderationSettingsSchema.parse(defaults).ModerationPolicyScope,
    'account_group'
  )
  assert.equal(
    moderationSettingsSchema.parse(defaults).ModerationSafetyIdentifierEnabled,
    false
  )
  assert.equal(
    moderationSettingsSchema.safeParse({
      ...defaults,
      ModerationPolicyScope: 'channel',
    }).success,
    false
  )
})

test('saves scope and private identifier changes together without enabling reviews or rewriting other options', async () => {
  const originalGet = api.get
  const originalPost = api.post
  const requests: Array<{ url: string; values: Record<string, string> }> = []
  api.get = (async (url: string) => ({
    data: {
      success: true,
      data:
        url === '/api/group/'
          ? ['default', 'route-one']
          : url.endsWith('/review-runs')
            ? []
            : {
                items: [],
                rows: [],
                total: 0,
                page: 1,
                page_size: 20,
                settings: { enabled: false },
                rules: [],
                pending: 0,
                running: 0,
                completed: 0,
                failed: 0,
                cancelled: 0,
                flagged: 0,
                fined: 0,
                charged_quota: 0,
              },
    },
  })) as typeof api.get
  api.post = (async (url: string, body: { values: Record<string, string> }) => {
    requests.push({ url, values: body.values })
    return { data: { success: true } }
  }) as typeof api.post
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const settle = () => new Promise((resolve) => setTimeout(resolve, 30))
  try {
    await act(async () => {
      root.render(
        <QueryClientProvider client={queryClient}>
          <I18nextProvider i18n={i18n}>
            <ModerationSettingsSection defaultValues={defaults} />
          </I18nextProvider>
        </QueryClientProvider>
      )
      await settle()
    })
    assert.match(
      container.textContent ?? '',
      /Assistant reviews always use account groups/
    )
    const scope = [
      ...container.querySelectorAll<HTMLButtonElement>('[role="combobox"]'),
    ].find((control) => control.textContent?.includes('Account group'))
    assert.ok(scope)
    await act(async () => {
      scope.click()
      await settle()
    })
    const requestOption = [
      ...document.querySelectorAll<HTMLElement>('[role="option"]'),
    ].find((option) => option.textContent === 'Request group')
    assert.ok(requestOption)
    await act(async () => {
      requestOption.click()
      await settle()
    })
    const switches =
      container.querySelectorAll<HTMLButtonElement>('[role="switch"]')
    assert.equal(switches[0].getAttribute('aria-checked'), 'false')
    assert.equal(switches[1].getAttribute('aria-checked'), 'false')
    await act(async () => {
      switches[1].click()
      await settle()
    })
    await act(async () => {
      const form = container.querySelector('form')
      assert.ok(form)
      form.dispatchEvent(
        new Event('submit', { bubbles: true, cancelable: true })
      )
      await settle()
    })
    assert.deepEqual(requests, [
      {
        url: '/api/option/bulk',
        values: {
          ModerationPolicyScope: 'request_group',
          ModerationSafetyIdentifierEnabled: 'true',
        },
      },
    ])
    assert.equal(switches[0].getAttribute('aria-checked'), 'false')
  } finally {
    await act(async () => root.unmount())
    container.remove()
    queryClient.clear()
    api.get = originalGet
    api.post = originalPost
  }
})
