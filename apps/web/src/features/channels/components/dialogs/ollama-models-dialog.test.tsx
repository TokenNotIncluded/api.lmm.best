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
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const { act, useEffect } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { ChannelsProvider, useChannels } = await import('../channels-provider')
const { channelSchema } = await import('../../types')
const { OllamaModelsDialog } = await import('./ollama-models-dialog')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
after(() => domWindow.close())

test('model-pull fetch declares canonical credits while preserving auth, body and abort signal', async () => {
  const originalAdapter = api.defaults.adapter
  const originalFetch = globalThis.fetch
  const originalAuth = useAuthStore.getState().auth
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const expiresAt = Math.floor(Date.now() / 1000) + 600
  useAuthStore.getState().auth.setBundle({
    access_token: 'ollama-test-token',
    token_type: 'Bearer',
    access_expires_at: expiresAt,
    user: { id: 42, username: 'admin-test', role: 100 },
    session: {
      sid: 'ollama-test-session',
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'test',
      created_at: 1,
      last_active_at: 1,
      expires_at: expiresAt + 600,
    },
  })
  api.defaults.adapter = async (config) => ({
    config,
    status: 200,
    statusText: 'OK',
    headers: {},
    data: { success: true, data: [] },
  })
  const calls: Array<{ url: string; init: RequestInit | undefined }> = []
  globalThis.fetch = (async (url, init) => {
    calls.push({ url: String(url), init })
    return new Response('data: [DONE]\n\n', {
      headers: { 'Content-Type': 'text/event-stream' },
    })
  }) as typeof fetch
  const channel = channelSchema.parse({
    id: 7,
    type: 4,
    key: '',
    status: 1,
    name: 'Ollama test',
    created_time: 0,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
    base_url: 'http://127.0.0.1:11434',
  })
  function Harness() {
    const { setCurrentRow } = useChannels()
    useEffect(() => setCurrentRow(channel), [setCurrentRow])
    return <OllamaModelsDialog open onOpenChange={() => {}} />
  }
  try {
    await act(async () => {
      root.render(
        <QueryClientProvider client={client}>
          <I18nextProvider i18n={i18n}>
            <ChannelsProvider>
              <Harness />
            </ChannelsProvider>
          </I18nextProvider>
        </QueryClientProvider>
      )
    })
    const input = document.querySelector<HTMLInputElement>('#ollama-pull')
    assert.ok(input)
    const setter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(setter)
    await act(async () => {
      setter.call(input, 'tiny-model')
      input.dispatchEvent(new Event('input', { bubbles: true }))
      input.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const button = [
      ...document.querySelectorAll<HTMLButtonElement>('button'),
    ].find((candidate) => candidate.textContent?.trim() === 'Pull')
    assert.ok(button)
    await act(async () => {
      button.click()
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
    assert.equal(calls.length, 1)
    assert.equal(calls[0].url, '/api/channel/ollama/pull/stream')
    const init = calls[0].init
    assert.ok(init)
    const headers = new Headers(init.headers)
    assert.equal(headers.get('X-LMM-Credit-Unit'), '500000')
    assert.equal(headers.get('Authorization'), 'Bearer ollama-test-token')
    assert.equal(headers.get('Accept'), 'text/event-stream')
    assert.equal(init.method, 'POST')
    assert.equal(init.credentials, 'include')
    assert.deepEqual(JSON.parse(String(init.body)), {
      channel_id: 7,
      model_name: 'tiny-model',
    })
    assert.ok(init.signal)
    assert.equal(init.signal.aborted, false)
  } finally {
    await act(async () => root.unmount())
    container.remove()
    client.clear()
    api.defaults.adapter = originalAdapter
    globalThis.fetch = originalFetch
    useAuthStore.setState({ auth: originalAuth })
  }
})
