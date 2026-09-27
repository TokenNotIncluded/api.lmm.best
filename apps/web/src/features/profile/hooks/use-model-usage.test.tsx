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

const domWindow = new Window({
  url: 'https://console.example.test/profile/share',
})
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
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { useModelUsage } = await import('./use-model-usage')

after(() => domWindow.close())

test('switching accounts hides the previous model report while the next account loads', async () => {
  const previousAuth = useAuthStore.getState().auth
  const previousGet = api.get
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const root = createRoot(document.createElement('div'))
  let state: ReturnType<typeof useModelUsage> | undefined
  let finishSecond: (() => void) | undefined
  let calls = 0
  const setUser = (id: number) =>
    useAuthStore.setState({
      auth: { ...previousAuth, user: { id, role: 1 } } as never,
    })
  const response = (model: string) => ({
    data: {
      success: true,
      data: [
        {
          created_at: Math.floor(Date.now() / 1000),
          model_name: model,
          token_used: 100,
          count: 1,
          quota: 10,
        },
      ],
    },
  })
  api.get = (async () => {
    calls += 1
    if (calls === 1) return response('first-account-model')
    return new Promise<ReturnType<typeof response>>((resolve) => {
      finishSecond = () => resolve(response('second-account-model'))
    })
  }) as typeof api.get
  function Harness() {
    state = useModelUsage('7d')
    return null
  }
  try {
    setUser(1)
    await act(async () => {
      root.render(
        <QueryClientProvider client={client}>
          <Harness />
        </QueryClientProvider>
      )
    })
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
    assert.equal(state?.report.models[0]?.modelName, 'first-account-model')
    await act(async () => {
      setUser(2)
    })
    assert.equal(state?.report.models.length, 0)
    assert.equal(state?.isPending, true)
    assert.equal(calls, 2)
    await act(async () => {
      finishSecond?.()
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
    assert.equal(state?.report.models[0]?.modelName, 'second-account-model')
  } finally {
    await act(async () => root.unmount())
    client.clear()
    api.get = previousGet
    useAuthStore.setState({ auth: previousAuth })
  }
})
