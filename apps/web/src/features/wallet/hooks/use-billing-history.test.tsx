/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import type { AxiosAdapter, AxiosResponse } from 'axios'
import { Window } from 'happy-dom'
import type { Root } from 'react-dom/client'

const domWindow = new Window({ url: 'https://console.example.test/wallet' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
  'Element',
  'Event',
  'MutationObserver',
  'localStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { api } = await import('@/lib/api')
const { useBillingHistory } = await import('./use-billing-history')

const originalAPIAdapter = api.defaults.adapter
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

type HookValue = ReturnType<typeof useBillingHistory>
let currentHook: HookValue | null = null

function response(
  config: Parameters<AxiosAdapter>[0],
  data: unknown
): AxiosResponse {
  return {
    config,
    data,
    headers: {},
    status: 200,
    statusText: 'OK',
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise
  })
  return { promise, resolve }
}

function billingResponse(id: number) {
  return {
    success: true,
    data: {
      items: [
        {
          id,
          user_id: 7,
          amount: id,
          money: id,
          trade_no: `order-${id}`,
          payment_method: 'stripe',
          create_time: id,
          status: 'success',
        },
      ],
      total: 1,
    },
  }
}

function Harness() {
  currentHook = useBillingHistory({ enabled: true })
  return null
}

async function flushEffects() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

afterEach(() => {
  api.defaults.adapter = originalAPIAdapter
  currentHook = null
  domWindow.localStorage.clear()
})

after(() => domWindow.close())

test('sort changes reset pagination and stale responses cannot replace the new global order', async () => {
  const older = deferred<unknown>()
  const newer = deferred<unknown>()
  const secondRequestStarted = deferred<void>()
  const urls: string[] = []

  api.defaults.adapter = async (config) => {
    urls.push(String(config.url))
    const pending = urls.length === 1 ? older : newer
    if (urls.length === 2) secondRequestStarted.resolve()
    return response(config, await pending.promise)
  }

  const container = document.createElement('div')
  const root: Root = createRoot(container)
  await act(async () => root.render(<Harness />))

  try {
    await flushEffects()
    assert.equal(currentHook?.sortBy, 'create_time')
    assert.equal(currentHook?.sortOrder, 'desc')
    assert.equal(
      urls[0],
      '/api/user/topup/self?p=1&page_size=10&sort_by=create_time&sort_order=desc'
    )

    await act(async () => {
      currentHook?.handlePageChange(4)
      currentHook?.handleSortByChange('amount')
    })
    await secondRequestStarted.promise

    assert.equal(currentHook?.page, 1)
    assert.equal(currentHook?.sortBy, 'amount')
    assert.equal(
      urls[1],
      '/api/user/topup/self?p=1&page_size=10&sort_by=amount&sort_order=desc'
    )

    newer.resolve(billingResponse(20))
    await flushEffects()
    assert.equal(currentHook?.records[0]?.id, 20)

    older.resolve(billingResponse(10))
    await flushEffects()
    assert.equal(currentHook?.records[0]?.id, 20)
  } finally {
    await act(async () => root.unmount())
  }
})
