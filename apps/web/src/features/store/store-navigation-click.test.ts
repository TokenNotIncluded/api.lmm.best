/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type { MouseEvent as ReactMouseEvent } from 'react'

import { handleStoreNavigationClick } from './store-navigation-click'

const dom = new Window({
  url: 'https://shop.example.test/store',
  settings: {
    navigation: {
      disableMainFrameNavigation: true,
      disableChildPageNavigation: true,
      disableFallbackToSetURL: true,
    },
  },
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLAnchorElement',
  'Element',
  'Node',
  'scrollTo',
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

afterEach(() => {
  dom.document.body.innerHTML = ''
})
after(() => dom.happyDOM.abort())

type ClickOptions = Pick<
  MouseEventInit,
  'button' | 'ctrlKey' | 'metaKey' | 'shiftKey' | 'altKey'
>

function anchor(href?: string) {
  const link = document.createElement('a')
  if (href !== undefined) link.setAttribute('href', href)
  const label = document.createElement('span')
  label.textContent = 'Open product'
  link.append(label)
  return { link, label }
}

function dispatch(
  scope: HTMLElement,
  target: HTMLElement,
  options: ClickOptions = {},
  alreadyPrevented = false
) {
  const calls: { href: string }[] = []
  const navigate = (value: { href: string }) => {
    calls.push(value)
    return Promise.resolve()
  }
  let intercepted = false
  let callbackError: unknown
  scope.addEventListener(
    'click',
    (event) => {
      try {
        handleStoreNavigationClick(
          event as unknown as ReactMouseEvent<HTMLElement>,
          navigate
        )
        intercepted = event.defaultPrevented
      } catch (error) {
        callbackError = error
      }
      // Keep the test browser from navigating while preserving the handler's
      // own decision about whether this click belongs to the SPA router.
      event.preventDefault()
    },
    { once: true }
  )
  const click = new dom.MouseEvent('click', {
    bubbles: true,
    cancelable: true,
    button: 0,
    ...options,
  })
  if (alreadyPrevented) click.preventDefault()
  target.dispatchEvent(click as unknown as Event)
  if (callbackError) throw callbackError
  return { calls, intercepted }
}

function clickLink(
  href: string,
  options: ClickOptions = {},
  attributes: Record<string, string> = {},
  nested = true,
  alreadyPrevented = false
) {
  const scope = document.createElement('main')
  const { link, label } = anchor(href)
  for (const [name, value] of Object.entries(attributes)) {
    link.setAttribute(name, value)
  }
  scope.append(link)
  document.body.append(scope)
  return dispatch(scope, nested ? label : link, options, alreadyPrevented)
}

test('ordinary store clicks navigate inside the app from nested link content', () => {
  for (const href of [
    '/store',
    '/store/products/product-1?quantity=2#buy',
    '/store/cart',
    '/store/favorites',
    '/store/orders',
    '/store/manage',
    '/store/settings',
    '/store/review',
  ]) {
    const result = clickLink(href)
    assert.equal(result.intercepted, true, href)
    assert.deepEqual(result.calls, [{ href }], href)
  }
})

test('same-origin absolute and relative store links preserve search and fragment', () => {
  const absolute = clickLink(
    'https://shop.example.test/store/products/p?variant_id=v#checkout',
    {},
    { target: '_self' },
    false
  )
  assert.equal(absolute.intercepted, true)
  assert.deepEqual(absolute.calls, [
    { href: '/store/products/p?variant_id=v#checkout' },
  ])
  const relative = clickLink('store/cart?from=product#summary')
  assert.equal(relative.intercepted, true)
  assert.deepEqual(relative.calls, [
    { href: '/store/cart?from=product#summary' },
  ])
})

test('modified and non-left clicks retain normal browser behavior', () => {
  for (const options of [
    { ctrlKey: true },
    { metaKey: true },
    { shiftKey: true },
    { altKey: true },
    { button: 1 },
    { button: 2 },
  ]) {
    const result = clickLink('/store/products/p', options)
    assert.equal(result.intercepted, false, JSON.stringify(options))
    assert.deepEqual(result.calls, [], JSON.stringify(options))
  }
})

test('previously handled clicks do not navigate again', () => {
  const result = clickLink('/store/products/p', {}, {}, true, true)
  assert.equal(result.intercepted, true)
  assert.deepEqual(result.calls, [])
})

test('new-window, named-frame and download links remain browser-owned', () => {
  const cases: Record<string, string>[] = [
    { target: '_blank' },
    { target: '_parent' },
    { target: '_top' },
    { target: 'store-preview' },
    { download: '' },
    { download: 'product.txt' },
  ]
  for (const attributes of cases) {
    const result = clickLink('/store/products/p', {}, attributes)
    assert.equal(result.intercepted, false, JSON.stringify(attributes))
    assert.deepEqual(result.calls, [], JSON.stringify(attributes))
  }
})

test('external origins and routes outside the store are not intercepted', () => {
  for (const href of [
    'https://merchant.example.test/store/products/p',
    'https://shop.example.test:8443/store/products/p',
    'http://shop.example.test/store/products/p',
    '//merchant.example.test/store/products/p',
    'mailto:seller@example.test',
    '/profile',
    '/sign-in?redirect=%2Fstore',
    '/stores/products/p',
    '/storehouse/products/p',
    '/store/../profile',
  ]) {
    const result = clickLink(href)
    assert.equal(result.intercepted, false, href)
    assert.deepEqual(result.calls, [], href)
  }
})

test('private pickup links keep their isolated document navigation', () => {
  const token = 'a'.repeat(43)
  for (const href of [
    `/store/claim/${token}`,
    `/store/claim/${token}?source=email#items`,
    `https://shop.example.test/store/claim/${token}`,
    '/store/claim/',
  ]) {
    const result = clickLink(href)
    assert.equal(result.intercepted, false, href)
    assert.deepEqual(result.calls, [], href)
  }
})

test('non-link clicks and anchors enclosing the scope are not intercepted', () => {
  const scope = document.createElement('main')
  const button = document.createElement('button')
  scope.append(button)
  document.body.append(scope)
  assert.deepEqual(dispatch(scope, button), {
    calls: [],
    intercepted: false,
  })

  const outer = anchor('/store/products/outside')
  const innerScope = document.createElement('span')
  const label = document.createElement('span')
  innerScope.append(label)
  outer.link.replaceChildren(innerScope)
  document.body.append(outer.link)
  assert.deepEqual(dispatch(innerScope, label), {
    calls: [],
    intercepted: false,
  })
})

test('anchor elements without href do not create a store navigation', () => {
  const scope = document.createElement('main')
  const { link, label } = anchor()
  scope.append(link)
  document.body.append(scope)
  assert.deepEqual(dispatch(scope, label), {
    calls: [],
    intercepted: false,
  })
})

test('malformed links do not throw or take over browser navigation', () => {
  const result = clickLink('http://[')
  assert.equal(result.intercepted, false)
  assert.deepEqual(result.calls, [])
})

test('real router navigation keeps the authenticated bundle when opening a product', async () => {
  const { act, createElement } = await import('react')
  const { createRoot } = await import('react-dom/client')
  const {
    createMemoryHistory,
    createRootRoute,
    createRoute,
    createRouter,
    Outlet,
    RouterProvider,
    useNavigate,
  } = await import('@tanstack/react-router')
  const { useAuthStore } = await import('@/stores/auth-store')
  const now = Math.floor(Date.now() / 1000)
  useAuthStore.getState().auth.setBundle({
    access_token: 'synthetic-store-navigation-access-token',
    token_type: 'Bearer',
    access_expires_at: now + 3600,
    user: { id: 7, username: 'navigation-fixture', role: 1, status: 1 },
    session: {
      sid: 'synthetic-store-navigation-session',
      current: true,
      login_method: 'password',
      ip: '192.0.2.7',
      user_agent: 'Navigation test',
      created_at: now,
      last_active_at: now,
      expires_at: now + 86400,
    },
  })
  const authenticated = useAuthStore.getState().auth
  let navigation: Promise<unknown> | undefined
  function StoreTestShell() {
    const navigate = useNavigate()
    return createElement(
      'main',
      {
        onClick: (event: ReactMouseEvent<HTMLElement>) => {
          handleStoreNavigationClick(event, (options) => {
            navigation = navigate(options)
            return navigation
          })
        },
      },
      createElement(Outlet)
    )
  }
  const rootRoute = createRootRoute({ component: StoreTestShell })
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/store',
    component: () =>
      createElement(
        'a',
        { href: '/store/products/integration-item?quantity=2#buy' },
        createElement('span', null, 'Open product')
      ),
  })
  const productRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/store/products/$productId',
    validateSearch: (search) => search,
    component: () =>
      createElement(
        'p',
        { 'data-current-route': 'product' },
        'Product fixture'
      ),
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute, productRoute]),
    history: createMemoryHistory({ initialEntries: ['/store'] }),
  })
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await router.load()
    await act(async () => {
      root.render(createElement(RouterProvider, { router }))
    })
    const label = host.querySelector('a span')
    assert.ok(label)
    const click = new dom.MouseEvent('click', {
      bubbles: true,
      cancelable: true,
      button: 0,
    })
    await act(async () => {
      label.dispatchEvent(click as unknown as Event)
      assert.ok(navigation)
      await navigation
    })
    assert.equal(click.defaultPrevented, true)
    assert.equal(
      router.state.location.href,
      '/store/products/integration-item?quantity=2#buy'
    )
    assert.ok(host.querySelector('[data-current-route="product"]'))
    assert.equal(window.location.href, 'https://shop.example.test/store')
    assert.equal(useAuthStore.getState().auth, authenticated)
    assert.equal(authenticated.bootstrapState, 'complete')
  } finally {
    await act(async () => root.unmount())
    useAuthStore.getState().auth.reset()
  }
})
