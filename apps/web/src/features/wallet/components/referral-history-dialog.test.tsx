/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'https://console.example.test/' })
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
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'localStorage',
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
const { api } = await import('@/lib/api')
const { ReferralHistoryDialog } = await import('./referral-history-dialog')
;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
after(() => domWindow.close())

function required<T>(value: T | null | undefined): T {
  assert.ok(value)
  return value
}
function page(id: number, cursor = 0) {
  return {
    success: true,
    data: {
      entries: [
        {
          id,
          reward_id: id,
          kind: 'reward',
          quota: 500000,
          reason: 'first_top_up',
          created_at: 1700000000,
        },
      ],
      next_cursor: cursor,
      available_quota: 500000,
      debt_quota: 0,
    },
  }
}
type Result = ReturnType<typeof page> | { success: false; message: string }
type RequestConfig = {
  params?: { before: number }
  signal?: AbortSignal
  disableDuplicate?: boolean
  skipBusinessError?: boolean
  skipErrorHandler?: boolean
}
type Pending = {
  before: number
  signal: AbortSignal | undefined
  resolve: (data: Result) => void
  reject: (error: Error) => void
}
function button(text: string) {
  return required(
    [...document.querySelectorAll('button')].find(
      (entry) => entry.textContent?.trim() === text
    )
  )
}
async function setup() {
  const original = api.get
  const pending: Pending[] = []
  api.get = ((url: string, config?: RequestConfig) => {
    assert.equal(url, '/api/user/self/aff/rewards')
    assert.equal(config?.disableDuplicate, true)
    assert.equal(config?.skipBusinessError, true)
    assert.equal(config?.skipErrorHandler, true)
    return new Promise<{ data: Result }>((resolve, reject) => {
      pending.push({
        before: config?.params?.before ?? 0,
        signal: config?.signal,
        resolve: (data) => resolve({ data }),
        reject,
      })
    })
  }) as typeof api.get
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  let mounted = true
  await act(async () => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <ReferralHistoryDialog />
      </I18nextProvider>
    )
  })
  const unmount = async () => {
    if (!mounted) return
    mounted = false
    await act(async () => root.unmount())
  }
  return {
    pending,
    unmount,
    async open() {
      await act(async () => button('Reward history').click())
    },
    async close() {
      await act(async () => {
        required(
          document.querySelector<HTMLButtonElement>(
            '[data-slot="dialog-close"]'
          )
        ).click()
      })
    },
    async cleanup() {
      await unmount()
      await act(async () => {
        pending.forEach((request) => request.reject(new Error('test cleanup')))
      })
      api.get = original
      container.remove()
    },
  }
}

test('close aborts the read and late success cannot replace reopened history', async () => {
  const ctx = await setup()
  try {
    await ctx.open()
    const obsolete = required(ctx.pending[0])
    assert.equal(required(obsolete.signal).aborted, false)
    await ctx.close()
    assert.equal(required(obsolete.signal).aborted, true)
    await ctx.open()
    assert.equal(ctx.pending.length, 2)
    await act(async () => obsolete.resolve(page(9999)))
    assert.doesNotMatch(document.body.textContent ?? '', /#9999/)
    assert.ok(document.querySelector('[role="status"]'))
    await act(async () => ctx.pending[1].resolve(page(50)))
    assert.match(document.body.textContent ?? '', /#50/)
    assert.equal(document.querySelector('[role="status"]'), null)
  } finally {
    await ctx.cleanup()
  }
})

test('unmount aborts without displaying a cancellation error', async () => {
  const ctx = await setup()
  try {
    await ctx.open()
    const request = required(ctx.pending[0])
    await ctx.unmount()
    assert.equal(required(request.signal).aborted, true)
    await act(async () => request.reject(new Error('cancelled')))
    assert.equal(document.querySelector('[role="alert"]'), null)
  } finally {
    await ctx.cleanup()
  }
})

test('double load-more shares one page and retry preserves its cursor and history', async () => {
  const ctx = await setup()
  try {
    await ctx.open()
    await act(async () => ctx.pending[0].resolve(page(100, 100)))
    await act(async () => {
      const more = button('Load more')
      more.click()
      more.click()
    })
    assert.equal(ctx.pending.length, 2)
    assert.equal(ctx.pending[1].before, 100)
    await act(async () => ctx.pending[1].reject(new Error('temporary failure')))
    assert.match(document.body.textContent ?? '', /#100/)
    assert.match(
      document.querySelector('[role="alert"]')?.textContent ?? '',
      /temporary failure/
    )
    await act(async () => button('Retry').click())
    assert.equal(ctx.pending[2].before, 100)
    await act(async () => ctx.pending[2].resolve(page(90)))
    const text = document.body.textContent ?? ''
    assert.equal(text.match(/#100/g)?.length, 1)
    assert.equal(text.match(/#90/g)?.length, 1)
    assert.equal(document.querySelector('[role="alert"]'), null)
    assert.equal(
      [...document.querySelectorAll('button')].some(
        (entry) => entry.textContent?.trim() === 'Load more'
      ),
      false
    )
  } finally {
    await ctx.cleanup()
  }
})

test('old request rejection cannot hide the loading state or show an error after reopen', async () => {
  const ctx = await setup()
  try {
    await ctx.open()
    await ctx.close()
    await ctx.open()
    await act(async () => ctx.pending[0].reject(new Error('obsolete error')))
    assert.equal(document.querySelector('[role="alert"]'), null)
    assert.ok(document.querySelector('[role="status"]'))
    await act(async () => ctx.pending[1].resolve(page(12)))
    assert.match(document.body.textContent ?? '', /#12/)
  } finally {
    await ctx.cleanup()
  }
})

test('application-level failure retries the first page rather than a stale cursor', async () => {
  const ctx = await setup()
  try {
    await ctx.open()
    await act(async () =>
      ctx.pending[0].resolve({ success: false, message: 'try again' })
    )
    assert.match(
      document.querySelector('[role="alert"]')?.textContent ?? '',
      /try again/
    )
    await act(async () => button('Retry').click())
    assert.equal(ctx.pending[1].before, 0)
    await act(async () => ctx.pending[1].resolve(page(7)))
    assert.match(document.body.textContent ?? '', /#7/)
  } finally {
    await ctx.cleanup()
  }
})
