/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'https://public.example.test/' })
domWindow.document.write(
  '<!doctype html><html><head></head><body></body></html>'
)
Object.defineProperty(domWindow.document, 'compatMode', { value: 'CSS1Compat' })
for (const key of [
  'window',
  'document',
  'navigator',
  'history',
  'location',
  'HTMLElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'KeyboardEvent',
  'PointerEvent',
  'FocusEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'scrollTo',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createRootRoute, createRouter, createMemoryHistory, RouterProvider } =
  await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { PublicHeader } = await import('./public-header')

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})
const originalGet = api.get

afterEach(() => {
  api.get = originalGet
  domWindow.happyDOM.setWindowSize({ width: 1024 })
  document.body.replaceChildren()
  document.body.removeAttribute('style')
  document.documentElement.removeAttribute('style')
})
after(() => domWindow.close())

async function renderHeader() {
  api.get = (async () => ({
    data: { success: true, data: '' },
  })) as typeof api.get
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const route = createRootRoute({
    component: () => (
      <PublicHeader
        navLinks={[{ title: 'Home', href: '/' }]}
        useDynamicNavLinks={false}
        showThemeSwitch={false}
        showLanguageSwitcher={false}
        showNotifications={false}
        showAuthButtons={false}
        className='forge-public-header'
      />
    ),
  })
  const router = createRouter({
    routeTree: route,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <RouterProvider router={router} />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await new Promise((resolve) => setTimeout(resolve, 30))
  })
  await act(async () => new Promise((resolve) => setTimeout(resolve, 30)))
  const toggle = container.querySelector<HTMLButtonElement>(
    '[aria-controls="public-mobile-navigation"]'
  )
  assert.ok(toggle)
  return {
    toggle,
    cleanup: async () => {
      await act(async () => root.unmount())
      queryClient.clear()
    },
  }
}

test('the public menu locks the root instead of body and restores inline overflow on repeated close', async () => {
  document.documentElement.style.setProperty('overflow', 'auto', 'important')
  document.body.style.overflow = 'visible'
  const rendered = await renderHeader()
  try {
    for (let cycle = 0; cycle < 2; cycle += 1) {
      await act(async () => rendered.toggle.click())
      assert.equal(document.documentElement.style.overflow, 'hidden')
      assert.equal(document.body.style.overflow, 'visible')
      assert.equal(rendered.toggle.getAttribute('aria-expanded'), 'true')
      await act(async () => rendered.toggle.click())
      assert.equal(document.documentElement.style.overflow, 'auto')
      assert.equal(
        document.documentElement.style.getPropertyPriority('overflow'),
        'important'
      )
      assert.equal(document.body.style.overflow, 'visible')
      assert.equal(rendered.toggle.getAttribute('aria-expanded'), 'false')
    }
  } finally {
    await rendered.cleanup()
  }
})

test('Escape restores focus without asking the browser to scroll', async () => {
  const rendered = await renderHeader()
  try {
    await act(async () => rendered.toggle.click())
    let focusOptions: FocusOptions | undefined
    const focus = rendered.toggle.focus.bind(rendered.toggle)
    rendered.toggle.focus = (options) => {
      focusOptions = options
      focus(options)
    }
    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    })
    assert.deepEqual(focusOptions, { preventScroll: true })
    assert.equal(document.activeElement, rendered.toggle)
    assert.equal(rendered.toggle.getAttribute('aria-expanded'), 'false')
    assert.equal(document.documentElement.style.overflow, '')
  } finally {
    await rendered.cleanup()
  }
})

test('unmounting an open public menu restores the original root overflow', async () => {
  document.documentElement.style.overflow = 'scroll'
  const rendered = await renderHeader()
  try {
    await act(async () => rendered.toggle.click())
    assert.equal(document.documentElement.style.overflow, 'hidden')
  } finally {
    await rendered.cleanup()
  }
  assert.equal(document.documentElement.style.overflow, 'scroll')
  assert.equal(document.body.style.overflow, '')
})

test('a closed public header does not restore an outdated overflow during unmount', async () => {
  const rendered = await renderHeader()
  document.documentElement.style.overflow = 'clip'
  await rendered.cleanup()
  assert.equal(document.documentElement.style.overflow, 'clip')
})

test('crossing the desktop breakpoint closes the menu and releases the root lock', async () => {
  const rendered = await renderHeader()
  try {
    await act(async () => rendered.toggle.click())
    assert.equal(document.documentElement.style.overflow, 'hidden')
    await act(async () => domWindow.happyDOM.setWindowSize({ width: 1440 }))
    assert.equal(rendered.toggle.getAttribute('aria-expanded'), 'false')
    assert.equal(document.documentElement.style.overflow, '')
    await act(async () => domWindow.happyDOM.setWindowSize({ width: 390 }))
    assert.equal(rendered.toggle.getAttribute('aria-expanded'), 'false')
  } finally {
    await rendered.cleanup()
  }
})
