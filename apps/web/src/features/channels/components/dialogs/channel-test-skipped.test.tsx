/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

import type { ChannelTestResponse, GetChannelsResponse } from '../../types'

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

const { act, useEffect } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { toast } = await import('sonner')
const { api } = await import('@/lib/api')
const { TooltipProvider } = await import('@/components/ui/tooltip')
const { channelsQueryKeys } = await import('../../lib/channel-actions')
const { channelSchema } = await import('../../types')
const { ChannelsProvider, useChannels } = await import('../channels-provider')
const { ChannelTestDialog } = await import('./channel-test-dialog')

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

const skippedMessage = 'This model needs a realtime session client.'
const skippedResponse: ChannelTestResponse = {
  success: false,
  skipped: true,
  message: skippedMessage,
  data: { response_time: 999 },
}

async function setup(responses: Record<string, ChannelTestResponse>) {
  const original = {
    get: api.get,
    put: api.put,
    loading: toast.loading,
    dismiss: toast.dismiss,
    info: toast.info,
    error: toast.error,
    success: toast.success,
  }
  const notices: Array<{
    kind: string
    title: string
    description?: unknown
  }> = []
  const puts: unknown[] = []
  const tested: string[] = []
  for (const kind of ['loading', 'info', 'error', 'success'] as const) {
    toast[kind] = ((title, options) => {
      notices.push({
        kind,
        title: String(title),
        description: options?.description,
      })
      return 1
    }) as typeof toast.info
  }
  toast.dismiss = (() => 1) as typeof toast.dismiss
  api.get = (async (url, config) => {
    assert.equal(url, '/api/channel/test/7')
    const model = String(config?.params?.model)
    tested.push(model)
    return { data: required(responses[model]) }
  }) as typeof api.get
  api.put = (async (url, payload) => {
    assert.equal(url, '/api/channel/')
    puts.push(payload)
    return { data: { success: true } }
  }) as typeof api.put

  const channel = channelSchema.parse({
    id: 7,
    type: 1,
    key: 'synthetic',
    status: 1,
    name: 'synthetic channel',
    models: Object.keys(responses).join(','),
    created_time: 1,
    test_time: 123,
    response_time: 321,
    balance_updated_time: 1,
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const queryKey = channelsQueryKeys.list({ page: 1 })
  client.setQueryData<GetChannelsResponse>(queryKey, {
    success: true,
    data: { items: [channel], total: 1, page: 1, page_size: 30 },
  })
  function Harness() {
    const { setCurrentRow } = useChannels()
    useEffect(() => setCurrentRow(channel), [setCurrentRow])
    return <ChannelTestDialog open onOpenChange={() => {}} />
  }
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={i18n}>
          <TooltipProvider>
            <ChannelsProvider>
              <Harness />
            </ChannelsProvider>
          </TooltipProvider>
        </I18nextProvider>
      </QueryClientProvider>
    )
  })
  const buttons = () => [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
  ]
  return {
    notices,
    puts,
    tested,
    buttons,
    cache: () => required(client.getQueryData<GetChannelsResponse>(queryKey)),
    async click(label: string) {
      const button = required(
        buttons().find((element) => element.textContent === label)
      )
      await act(async () => {
        button.click()
        await new Promise((resolve) => setTimeout(resolve, 30))
      })
    },
    async cleanup() {
      await act(async () => root.unmount())
      client.clear()
      container.remove()
      api.get = original.get
      api.put = original.put
      toast.loading = original.loading
      toast.dismiss = original.dismiss
      toast.info = original.info
      toast.error = original.error
      toast.success = original.success
    },
  }
}

test('a skipped batch is neutral and preserves channel latency and models', async () => {
  const ctx = await setup({ live: skippedResponse })
  try {
    await ctx.click('Test all 1 models')
    assert.deepEqual(ctx.tested, ['live'])
    assert.ok(document.body.textContent?.includes(skippedMessage))
    const badge = required(
      [...document.querySelectorAll('span')].find(
        (element) => element.textContent === 'Skipped'
      )
    )
    assert.ok(badge.classList.contains('text-muted-foreground'))
    assert.ok(
      !ctx
        .buttons()
        .some((button) => button.textContent?.includes('Delete failed models'))
    )
    assert.equal(required(ctx.cache().data).items[0].response_time, 321)
    assert.equal(required(ctx.cache().data).items[0].test_time, 123)
    assert.ok(
      ctx.notices.some(
        (notice) => notice.kind === 'info' && notice.description === '1 skipped'
      )
    )
    assert.ok(
      !ctx.notices.some(
        (notice) => notice.kind === 'error' || notice.kind === 'success'
      )
    )
    assert.deepEqual(ctx.puts, [])
  } finally {
    await ctx.cleanup()
  }
})

test('mixed batch counts and deleting failures retain skipped models', async () => {
  const ctx = await setup({
    good: { success: true, data: { response_time: 10 } },
    bad: {
      success: false,
      message: 'upstream failed',
      data: { response_time: 20 },
    },
    live: skippedResponse,
  })
  try {
    await ctx.click('Test all 3 models')
    assert.ok(
      ctx.notices.some(
        (notice) =>
          notice.kind === 'error' &&
          notice.title === 'Batch test completed: 1 succeeded, 1 failed' &&
          notice.description === '1 skipped'
      )
    )
    assert.equal(required(ctx.cache().data).items[0].response_time, 20)
    await ctx.click('Delete failed models (1)')
    await ctx.click('Delete')
    assert.deepEqual(ctx.puts, [{ id: 7, models: 'good,live' }])
    assert.ok(document.body.textContent?.includes('live'))
    assert.ok(document.body.textContent?.includes('Skipped'))
  } finally {
    await ctx.cleanup()
  }
})
