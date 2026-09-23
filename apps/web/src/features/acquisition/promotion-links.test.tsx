/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({
  url: 'https://console.example.test/operations/sources',
})
dom.document.write('<!doctype html><html><body></body></html>')
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'Element',
  'Node',
  'Event',
  'PointerEvent',
  'MutationObserver',
  'ResizeObserver',
  'localStorage',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.assign(globalThis, {
  IS_REACT_ACT_ENVIRONMENT: true,
  __LMM_PERSONA_DEBUG__: false,
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { api } = await import('@/lib/api')
const { PromotionLinks } = await import('./promotion-links')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
after(() => dom.close())

function button(scope: ParentNode, label: string) {
  const found = [...scope.querySelectorAll('button')].find(
    (entry) => entry.textContent?.trim() === label
  )
  assert.ok(found, `missing button: ${label}`)
  return found
}

test('promotion link can be copied, and deletion requires confirmation and preserves retry', async () => {
  const originalGet = api.get
  const originalDelete = api.delete
  const id = '0123456789abcdef0123456789abcdef'
  const record = {
    id,
    name: 'Community launch',
    source: 'community',
    medium: 'post',
    campaign: 'autumn',
    content: '',
    target: '/guide',
    archived: false,
    created_at: 1_700_000_000,
  }
  let deleted = false
  let attempts = 0
  let copied = ''
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: {
      writeText: async (value: string) => {
        copied = value
      },
    },
  })
  api.get = (async (path: string) => {
    assert.match(path, /^\/api\/admin\/acquisition\/links\?/)
    return {
      data: {
        success: true,
        data: {
          items: deleted ? [] : [record],
          total: deleted ? 0 : 1,
          page: 1,
        },
      },
    }
  }) as typeof api.get
  api.delete = (async (path: string) => {
    assert.equal(path, `/api/admin/acquisition/links/${id}`)
    attempts += 1
    if (attempts === 1) {
      return { data: { success: false, message: 'temporary failure' } }
    }
    deleted = true
    return { data: { success: true, data: { id, deleted: true } } }
  }) as typeof api.delete

  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  try {
    await act(async () =>
      root.render(
        <I18nextProvider i18n={i18n}>
          <QueryClientProvider client={client}>
            <PromotionLinks canWrite />
          </QueryClientProvider>
        </I18nextProvider>
      )
    )
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    const field = container.querySelector<HTMLInputElement>('input[readonly]')
    assert.ok(field)
    assert.equal(
      field.value,
      `https://console.example.test/guide?lmm_source=${id}&utm_source=community&utm_medium=post&utm_campaign=autumn`
    )
    await act(async () => button(container, 'Copy link').click())
    assert.equal(copied, field.value)
    assert.ok(button(container, 'Copied'))

    await act(async () => button(container, 'Delete').click())
    const dialog = document.querySelector('[data-slot="alert-dialog-content"]')
    assert.ok(dialog)
    assert.match(
      dialog.textContent || '',
      /Historical attribution and spend stay in reports/
    )
    await act(async () => button(dialog, 'Delete').click())
    assert.equal(attempts, 1)
    assert.ok(dialog.querySelector('[role="alert"]'))
    assert.ok(container.querySelector('input[readonly]'))
    await act(async () => button(dialog, 'Delete').click())
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    assert.equal(attempts, 2)
    assert.equal(container.querySelector('input[readonly]'), null)
    assert.match(container.textContent || '', /No promotion links yet/)
  } finally {
    await act(async () => root.unmount())
    client.clear()
    container.remove()
    api.get = originalGet
    api.delete = originalDelete
  }
})
