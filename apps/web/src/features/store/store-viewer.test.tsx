/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import type { QueryClient as QueryClientType } from '@tanstack/react-query'
import { Window } from 'happy-dom'

import { useAuthStore } from '@/stores/auth-store'

const dom = new Window({ url: 'https://shop.example.test/store' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
  'Event',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})
const { QueryClient, QueryClientProvider, useQuery } =
  await import('@tanstack/react-query')
const { act, createElement } = await import('react')
const { createRoot } = await import('react-dom/client')
const {
  clearPreviousStoreViewer,
  currentStoreViewer,
  storeViewerId,
  useStoreViewer,
} = await import('./store-viewer')
let root: ReturnType<typeof createRoot> | undefined
let client: QueryClientType | undefined

afterEach(async () => {
  await act(async () => root?.unmount())
  root = undefined
  client?.clear()
  client = undefined
  useAuthStore.getState().auth.reset()
  dom.document.body.replaceChildren()
})

after(() => dom.happyDOM.abort())

test('viewer cache keys separate accounts and unknown or disabled status from anonymous', () => {
  assert.equal(storeViewerId(null), 'anonymous')
  assert.equal(storeViewerId({ id: 27, status: 1 }), 'account:27')
  assert.equal(storeViewerId({ id: 28, status: 1 }), 'account:28')
  assert.equal(storeViewerId({ id: 27 }), 'account:27:unverified')
  assert.equal(storeViewerId({ id: 27, status: 2 }), 'account:27:disabled')
  useAuthStore
    .getState()
    .auth.setUser({ id: 27, role: 1, status: 1, username: 'buyer' })
  assert.equal(currentStoreViewer(), 'account:27')
})

test('viewer cleanup removes legacy and previous private data and aborts pending old requests', async () => {
  const queryClient = new QueryClient()
  queryClient.setQueryData(
    ['store', 'products', 'account:27'],
    ['private-product']
  )
  queryClient.setQueryData(
    ['store', 'favorites', 'account:27'],
    ['private-favorite']
  )
  queryClient.setQueryData(
    ['store', 'product', 'private-id', 27],
    'legacy-private-detail'
  )
  queryClient.setQueryData(
    ['store', 'products', 'account:28'],
    ['current-product']
  )
  queryClient.setQueryData(['other-feature', 'public'], 'preserved')
  let aborted = false
  const pending = queryClient.fetchQuery({
    queryKey: ['store', 'cart', 'account:27'],
    queryFn: ({ signal }) =>
      new Promise<never>((_resolve, reject) => {
        signal.addEventListener('abort', () => {
          aborted = true
          reject(new Error('aborted'))
        })
      }),
  })
  const cancellation = pending.catch(() => undefined)
  clearPreviousStoreViewer(queryClient, 'account:28')
  await cancellation
  assert.equal(aborted, true)
  assert.equal(
    queryClient.getQueryData(['store', 'products', 'account:27']),
    undefined
  )
  assert.equal(
    queryClient.getQueryData(['store', 'favorites', 'account:27']),
    undefined
  )
  assert.equal(
    queryClient.getQueryData(['store', 'product', 'private-id', 27]),
    undefined
  )
  assert.deepEqual(
    queryClient.getQueryData(['store', 'products', 'account:28']),
    ['current-product']
  )
  assert.equal(
    queryClient.getQueryData(['other-feature', 'public']),
    'preserved'
  )
  queryClient.clear()
})

test('a mounted storefront cannot show the previous account while a new account loads', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 27, role: 1, status: 1, username: 'buyer-a' })
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client = queryClient
  queryClient.setQueryData(
    ['store', 'favorites', 'account:27'],
    'private-account-a'
  )
  let resolveAccountB!: (value: string) => void
  const accountB = new Promise<string>((resolve) => {
    resolveAccountB = resolve
  })
  function Surface() {
    const viewer = useStoreViewer()
    const query = useQuery({
      queryKey: ['store', 'favorites', viewer],
      queryFn: () => {
        if (viewer === 'account:27') return Promise.resolve('private-account-a')
        if (viewer === 'account:28') return accountB
        return Promise.resolve('anonymous-public-products')
      },
      staleTime: Infinity,
    })
    return createElement('div', null, query.data ?? 'loading')
  }
  const host = document.createElement('div')
  document.body.append(host)
  const mountedRoot = createRoot(host)
  root = mountedRoot
  await act(async () => {
    mountedRoot.render(
      createElement(
        QueryClientProvider,
        { client: queryClient },
        createElement(Surface)
      )
    )
    await new Promise((resolve) => setTimeout(resolve, 10))
  })
  assert.equal(host.textContent, 'private-account-a')
  await act(async () => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 28, role: 1, status: 1, username: 'buyer-b' })
  })
  assert.equal(host.textContent, 'loading')
  assert.equal(
    queryClient.getQueryData(['store', 'favorites', 'account:27']),
    undefined
  )
  await act(async () => {
    resolveAccountB('account-b-favorite')
    await new Promise((resolve) => setTimeout(resolve, 10))
  })
  assert.equal(host.textContent, 'account-b-favorite')
  await act(async () => {
    useAuthStore.getState().auth.reset()
    await new Promise((resolve) => setTimeout(resolve, 10))
  })
  assert.equal(
    queryClient.getQueryData(['store', 'favorites', 'account:28']),
    undefined
  )
  assert.notEqual(host.textContent, 'account-b-favorite')
})
