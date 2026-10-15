/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
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
const { useStatus } = await import('./use-status')
const { bindAuthCache, clearAuthentication, createRefreshRunner } =
  await import('@/lib/auth-session')
const originalGet = api.get
after(() => {
  api.get = originalGet
  domWindow.close()
})

test('anonymous refresh 401 cannot cancel a mounted live capability read', async () => {
  for (const cached of ['none', 'query', 'storage']) {
    window.localStorage.clear()
    const client = new QueryClient()
    const payload = { register_enabled: true, password_register_enabled: true }
    if (cached === 'query') {
      client.setQueryData(['status', 'anonymous'], payload)
    }
    if (cached === 'storage') {
      window.localStorage.setItem('status', JSON.stringify(payload))
    }
    const unbind = bindAuthCache(client)
    let resolve!: (value: unknown) => void
    const response = new Promise((done) => {
      resolve = done
    })
    api.get = (async (url, config) => {
      assert.equal(url, '/api/status')
      assert.equal(config?.skipAuthRefresh, true)
      return response
    }) as typeof api.get
    let latest: ReturnType<typeof useStatus> | undefined
    function Probe() {
      latest = useStatus()
      return null
    }
    const root = createRoot(document.createElement('div'))
    try {
      await act(async () => {
        root.render(
          <QueryClientProvider client={client}>
            <Probe />
          </QueryClientProvider>
        )
        await new Promise((done) => setTimeout(done, 10))
      })
      assert.equal(latest?.capabilitiesReady, false)
      client.setQueryData(['status', 'user:7:docs:1'], {
        backend_capabilities: { bounty_public_read: true },
      })
      client.setQueryData(['account', 7], { balance: 100 })
      client.getMutationCache().build(client, {
        mutationKey: ['payment'],
        mutationFn: async () => undefined,
      })
      await act(async () => {
        const result = await createRefreshRunner({
          request: async () => ({ status: 401 }),
          getExpectedSID: () => undefined,
          parseBundle: () => null,
          acceptBundle: () => {
            throw new Error('unexpected login')
          },
          clear: () => clearAuthentication(false),
          markTransient: () => {
            throw new Error('unexpected transient error')
          },
          wait: async () => undefined,
        })()
        assert.equal(result.kind, 'anonymous')
      })
      assert.equal(client.getQueryData(['status', 'user:7:docs:1']), undefined)
      assert.equal(client.getQueryData(['account', 7]), undefined)
      assert.equal(client.getMutationCache().getAll().length, 0)
      assert.equal(latest?.capabilitiesReady, false)
      await act(async () => {
        resolve({ data: { success: true, data: payload } })
        await new Promise((done) => setTimeout(done, 30))
      })
      assert.equal(latest?.capabilitiesReady, true, `cached=${cached}`)
    } finally {
      await act(async () => root.unmount())
      unbind()
      client.clear()
    }
  }
})
