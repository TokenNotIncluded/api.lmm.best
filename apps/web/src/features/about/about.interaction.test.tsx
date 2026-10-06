/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type { ReactNode } from 'react'

const dom = new Window({ url: 'https://console.example.test/about' })
dom.document.write('<!doctype html><html><head></head><body></body></html>')
Object.defineProperty(dom.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
const originalGlobals = new Map<string, PropertyDescriptor | undefined>()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'matchMedia',
  'customElements',
  'localStorage',
  'sessionStorage',
  'scrollTo',
] as const) {
  originalGlobals.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
originalGlobals.set(
  'IS_REACT_ACT_ENVIRONMENT',
  Object.getOwnPropertyDescriptor(globalThis, 'IS_REACT_ACT_ENVIRONMENT')
)
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})
// The page owns its content and data; navigation chrome has separate tests.
mock.module('@/features/forge/forge-public-shell', () => ({
  ForgePublicShell: ({ children }: { children: ReactNode }) => <>{children}</>,
}))
// Happy DOM does not model DOMPurify's browser realm. Check the unchanged
// renderer boundary here; this test does not claim sanitizer coverage.
mock.module('@/components/rich-content', () => ({
  RichContent: ({
    content,
    mode,
    htmlVariant,
  }: {
    content: string
    mode: string
    htmlVariant?: string
  }) => (
    <div data-rich-mode={mode} data-html-variant={htmlVariant}>
      {content}
    </div>
  ),
}))

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createRootRoute, createRouter, createMemoryHistory, RouterProvider } =
  await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { getBuildVersion } = await import('@/lib/build-metadata')
const { useUpdateOption, useUpdateOptions } =
  await import('@/features/system-settings/hooks/use-update-option')
const { About } = await import('./index')
const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})
const originalGet = api.get
const originalPut = api.put
const originalPost = api.post
let root: ReturnType<typeof createRoot> | undefined
let client: InstanceType<typeof QueryClient> | undefined
let singleSave: ReturnType<typeof useUpdateOption> | undefined
let batchSave: ReturnType<typeof useUpdateOptions> | undefined

function SaveHarness() {
  singleSave = useUpdateOption()
  batchSave = useUpdateOptions()
  return null
}
const flush = () => new Promise((resolve) => setTimeout(resolve, 30))
async function waitFor(check: () => boolean) {
  for (let attempt = 0; attempt < 30 && !check(); attempt += 1) {
    await act(flush)
  }
  assert.ok(check(), 'page did not reach the expected state')
}

async function mount(
  options: {
    content?: () => { success: boolean; message: string; data: string }
    status?: () => Promise<unknown>
    saveHarness?: boolean
  } = {}
) {
  const host = document.createElement('div')
  document.body.append(host)
  const activeClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  client = activeClient
  api.get = (async (url) => {
    if (url === '/api/about') {
      return {
        data: options.content?.() ?? { success: true, message: '', data: '' },
      }
    }
    if (url === '/api/status') {
      return {
        data: {
          success: true,
          data: options.status
            ? await options.status()
            : { version: '0.2.86', backend_capabilities: {} },
        },
      }
    }
    throw new Error(`Unexpected request: ${url}`)
  }) as typeof api.get
  const route = createRootRoute({
    component: () => (
      <>
        <About />
        {options.saveHarness && <SaveHarness />}
      </>
    ),
  })
  const router = createRouter({
    routeTree: route,
    history: createMemoryHistory({ initialEntries: ['/about'] }),
  })
  root = createRoot(host)
  const activeRoot = root
  await act(async () => {
    activeRoot.render(
      <QueryClientProvider client={activeClient}>
        <I18nextProvider i18n={i18n}>
          <RouterProvider router={router} />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flush()
  })
  await act(flush)
  return host
}

afterEach(async () => {
  const activeRoot = root
  if (activeRoot) await act(() => activeRoot.unmount())
  root = undefined
  client?.clear()
  client = undefined
  singleSave = undefined
  batchSave = undefined
  api.get = originalGet
  api.put = originalPut
  api.post = originalPost
  document.body.innerHTML = ''
  localStorage.clear()
})
after(() => {
  dom.close()
  for (const [key, descriptor] of originalGlobals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
})

test('default About describes current services without a first-top-up promise', async () => {
  const host = await mount()
  await waitFor(() => host.textContent?.includes('API 0.2.86') === true)
  assert.match(host.textContent ?? '', /manage keys and usage/)
  assert.match(host.textContent ?? '', /One gateway for supported models/)
  assert.match(host.textContent ?? '', /API 0\.2\.86/)
  assert.ok(host.textContent?.includes(`Web ${getBuildVersion()}`))
  assert.doesNotMatch(
    host.textContent ?? '',
    /first top-up|every model|has not published/
  )
  assert.equal(host.querySelectorAll('dl > div').length, 3)
  assert.equal(
    host.querySelector('a[href="/guide"]')?.textContent,
    'Read the guide'
  )
})

test('URL override keeps the existing iframe sandbox and takes priority', async () => {
  const host = await mount({
    content: () => ({
      success: true,
      message: '',
      data: 'https://tenant.example.test/about',
    }),
  })
  const frame = host.querySelector('iframe')
  assert.equal(frame?.getAttribute('src'), 'https://tenant.example.test/about')
  assert.equal(
    frame?.getAttribute('sandbox'),
    'allow-forms allow-popups allow-popups-to-escape-sandbox allow-scripts'
  )
  assert.doesNotMatch(host.textContent ?? '', /One gateway/)
})

test('HTML override remains isolated and takes priority', async () => {
  const host = await mount({
    content: () => ({
      success: true,
      message: '',
      data: '<h1>Tenant About</h1>',
    }),
  })
  const isolated = host.querySelector('[data-rich-mode="html"]')
  assert.equal(isolated?.getAttribute('data-html-variant'), 'isolated')
  assert.equal(isolated?.textContent, '<h1>Tenant About</h1>')
  assert.doesNotMatch(host.textContent ?? '', /One gateway/)
})

test('Markdown override remains the administrator content', async () => {
  const host = await mount({
    content: () => ({
      success: true,
      message: '',
      data: '# Tenant introduction',
    }),
  })
  assert.match(host.textContent ?? '', /Tenant introduction/)
  assert.equal(
    host.querySelector('[data-rich-mode]')?.getAttribute('data-rich-mode'),
    'markdown'
  )
  assert.doesNotMatch(host.textContent ?? '', /One gateway/)
})

test('business failure shows a retry that fetches the page again', async () => {
  let failed = true
  const host = await mount({
    content: () => ({ success: !failed, message: 'unavailable', data: '' }),
  })
  assert.match(host.textContent ?? '', /Failed to load/)
  assert.doesNotMatch(host.textContent ?? '', /One gateway/)
  failed = false
  const retry = Array.from(host.querySelectorAll('button')).find(
    (button) => button.textContent === 'Retry'
  )
  assert.ok(retry)
  await act(async () => {
    retry.click()
    await flush()
  })
  await act(flush)
  assert.match(host.textContent ?? '', /One gateway/)
})

test('transport failure does not render the empty-content default', async () => {
  const host = await mount({
    content: () => {
      throw new Error('offline')
    },
  })
  assert.match(host.textContent ?? '', /Failed to load/)
  assert.doesNotMatch(host.textContent ?? '', /One gateway/)
})

test('single and batch About saves refresh an already mounted page', async () => {
  let content = '# Before save'
  const host = await mount({
    content: () => ({ success: true, message: '', data: content }),
    saveHarness: true,
  })
  api.put = (async (_url, body) => {
    content = (body as { value: string }).value
    return { data: { success: true, message: '' } }
  }) as typeof api.put
  api.post = (async (_url, body) => {
    content = (body as { values: { About: string } }).values.About
    return { data: { success: true, message: '' } }
  }) as typeof api.post
  assert.match(host.textContent ?? '', /Before save/)
  const activeSingle = singleSave
  assert.ok(activeSingle)
  await act(async () => {
    await activeSingle.mutateAsync({ key: 'About', value: '# Single save' })
    await flush()
  })
  await act(flush)
  assert.match(host.textContent ?? '', /Single save/)
  const activeBatch = batchSave
  assert.ok(activeBatch)
  await act(async () => {
    await activeBatch.mutateAsync({ About: '# Batch save' })
    await flush()
  })
  await act(flush)
  assert.match(host.textContent ?? '', /Batch save/)
})

test('cached status is not presented as the current API version', async () => {
  localStorage.setItem('status', JSON.stringify({ version: '9.9.9-stale' }))
  let resolveStatus: (value: unknown) => void = () => {}
  const pendingStatus = new Promise((resolve) => {
    resolveStatus = resolve
  })
  const host = await mount({ status: () => pendingStatus })
  assert.match(host.textContent ?? '', /API Unknown version/)
  assert.doesNotMatch(host.textContent ?? '', /9\.9\.9-stale/)
  await act(async () => {
    resolveStatus({ version: '0.2.86-live', backend_capabilities: {} })
    await flush()
  })
  await act(flush)
  assert.match(host.textContent ?? '', /API 0\.2\.86-live/)
})
