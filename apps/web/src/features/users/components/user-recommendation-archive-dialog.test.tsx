/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({ url: 'https://console.example.test/admin/users' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
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
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.defineProperties(globalThis, {
  getComputedStyle: {
    configurable: true,
    value: dom.getComputedStyle.bind(dom),
  },
  requestAnimationFrame: {
    configurable: true,
    value: (fn: FrameRequestCallback) => setTimeout(() => fn(0), 0),
  },
  cancelAnimationFrame: {
    configurable: true,
    value: (id: number) => clearTimeout(id),
  },
})
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { UserRecommendationArchiveDialog } =
  await import('./user-recommendation-archive-dialog')
;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const originalGet = api.get
const originalAuth = useAuthStore.getState().auth
const target = {
  id: 41,
  username: 'applicant',
  display_name: 'Applicant',
  quota: 0,
  used_quota: 0,
  request_count: 0,
  group: 'default',
  status: 1,
  role: 1,
}
const flush = () => new Promise((resolve) => setTimeout(resolve, 25))
afterEach(() => {
  api.get = originalGet
  useAuthStore.setState({ auth: originalAuth })
  document.body.replaceChildren()
})
after(() => dom.close())

test('archive button fetches and displays the selected user recommendation', async () => {
  useAuthStore.setState({
    auth: { ...originalAuth, user: { id: 1, username: 'admin', role: 100 } },
  })
  const requests: string[] = []
  api.get = (async (url: string) => {
    requests.push(url)
    return {
      data: {
        success: true,
        data: [
          {
            id: 7,
            request_id: 9,
            approved_at: 1786400000,
            recommendation: 'Approved robotics integration proposal.',
            admin_note: 'Welcome to L1.',
          },
        ],
      },
    }
  }) as typeof api.get
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  try {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <UserRecommendationArchiveDialog user={target} />
        </I18nextProvider>
      )
      await flush()
    })
    assert.equal(requests.length, 0)
    const trigger = document.querySelector<HTMLButtonElement>(
      'button[aria-label="View L1 recommendation archive"]'
    )
    assert.ok(trigger)
    await act(async () => {
      trigger.click()
      await flush()
    })
    assert.equal(requests.length, 1)
    assert.ok(requests[0].includes('/41/'))
    assert.match(
      document.body.textContent ?? '',
      /Approved robotics integration proposal/
    )
    assert.match(document.body.textContent ?? '', /Welcome to L1/)
    assert.doesNotMatch(
      document.body.textContent ?? '',
      /No approved recommendation archive yet/
    )
  } finally {
    await act(async () => {
      root.unmount()
      await flush()
    })
  }
})

test('ordinary users cannot open another user archive', async () => {
  useAuthStore.setState({
    auth: { ...originalAuth, user: { id: 2, username: 'viewer', role: 1 } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  try {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <UserRecommendationArchiveDialog user={target} />
        </I18nextProvider>
      )
      await flush()
    })
    assert.equal(
      document.querySelector(
        'button[aria-label="View L1 recommendation archive"]'
      ),
      null
    )
  } finally {
    await act(async () => {
      root.unmount()
      await flush()
    })
  }
})
