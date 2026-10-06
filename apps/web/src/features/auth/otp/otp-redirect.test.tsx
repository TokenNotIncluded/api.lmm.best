/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun's test module is only available in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type { ReactNode } from 'react'

import type { AuthBundle } from '@/stores/auth-store'

const origin = 'https://console.example.test'
const dom = new Window({ url: `${origin}/sign-in` })
for (const key of [
  'window',
  'document',
  'navigator',
  'localStorage',
  'sessionStorage',
  'HTMLElement',
  'HTMLInputElement',
  'Element',
  'SVGElement',
  'Node',
  'Event',
  'MouseEvent',
  'InputEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'matchMedia',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
Object.defineProperty(globalThis, 'scrollTo', {
  configurable: true,
  value: () => undefined,
})

mock.module('@/features/auth/auth-layout', () => ({
  AuthLayout: ({ children }: { children: ReactNode }) => <>{children}</>,
}))
mock.module('@/features/auth/components/oauth-providers', () => ({
  OAuthProviders: () => null,
}))
let registrationEnabled = true
mock.module('@/hooks/use-status', () => ({
  useStatus: () => ({
    status: {
      password_login_enabled: true,
      register_enabled: registrationEnabled,
      password_register_enabled: registrationEnabled,
      oauth_register_enabled: false,
    },
    capabilitiesReady: true,
  }),
}))

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} = await import('@tanstack/react-router')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { Route: signInFileRoute } = await import('@/routes/(auth)/sign-in')
const { Route: otpFileRoute } = await import('@/routes/(auth)/otp')
const { Route: signUpFileRoute } = await import('@/routes/(auth)/sign-up')
const { SignIn } = await import('../sign-in')
const { SignUp } = await import('../sign-up')
const { Otp } = await import('./index')

const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })

const target =
  '/store/products/product-fixture?variant_id=variant-fixture&quantity=3&promotion=SUMMER%2B10#purchase'
const bundle: AuthBundle = {
  access_token: 'otp-redirect-fixture-token',
  token_type: 'Bearer',
  access_expires_at: 2_000_000_000,
  user: { id: 37, username: 'otp-fixture', role: 1 },
  session: {
    sid: 'otp-redirect-fixture-session',
    current: true,
    login_method: 'password',
    ip: '',
    user_agent: '',
    created_at: 1,
    last_active_at: 1,
    expires_at: 2_000_000_000,
  },
}
const originalPost = api.post
const calls: Array<{ url: string; body: unknown }> = []
const roots: ReturnType<typeof createRoot>[] = []
const clients: InstanceType<typeof QueryClient>[] = []

afterEach(async () => {
  for (const root of roots.splice(0)) await act(async () => root.unmount())
  for (const client of clients.splice(0)) client.clear()
  api.post = originalPost
  useAuthStore.getState().auth.reset('complete')
  calls.length = 0
  registrationEnabled = true
  document.body.replaceChildren()
})
after(() => {
  mock.restore()
  dom.close()
})

async function mount(entry: string) {
  api.post = (async (url, body) => {
    calls.push({ url: String(url), body })
    if (String(url).startsWith('/api/user/login?')) {
      return {
        data: {
          success: true,
          data: { require_2fa: true, flow_token: 'otp-redirect-flow' },
        },
      }
    }
    if (url === '/api/user/login/2fa') {
      return { data: { success: true, data: bundle } }
    }
    if (url === '/api/user/register') {
      return { data: { success: true } }
    }
    throw new Error(`Unexpected authentication request: ${url}`)
  }) as typeof api.post
  const rootRoute = createRootRoute({ component: Outlet })
  const authRoute = createRoute({
    getParentRoute: () => rootRoute,
    id: '(auth)',
    component: Outlet,
  })
  const signInRoute = createRoute({
    getParentRoute: () => authRoute,
    path: 'sign-in',
    component: SignIn,
    validateSearch: signInFileRoute.options.validateSearch,
  })
  const otpRoute = createRoute({
    getParentRoute: () => authRoute,
    path: 'otp',
    component: Otp,
    validateSearch: otpFileRoute.options.validateSearch,
  })
  const signUpRoute = createRoute({
    getParentRoute: () => authRoute,
    path: 'sign-up',
    component: SignUp,
    validateSearch: signUpFileRoute.options.validateSearch,
  })
  const productRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/store/products/$productId',
    validateSearch: (search) => search,
    component: () => <p>Product fixture</p>,
  })
  const landingRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/getting-started',
    component: () => <p>Default landing fixture</p>,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([
      authRoute.addChildren([signInRoute, signUpRoute, otpRoute]),
      productRoute,
      landingRoute,
    ]),
    history: createMemoryHistory({ initialEntries: [entry] }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  roots.push(root)
  await router.load()
  await act(async () => {
    root.render(
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={i18n}>
          <RouterProvider router={router} />
        </I18nextProvider>
      </QueryClientProvider>
    )
  })
  return { host, router }
}

async function fill(input: HTMLInputElement | null, value: string) {
  assert.ok(input)
  const setter = Object.getOwnPropertyDescriptor(
    dom.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setter)
  await act(async () => {
    setter.call(input, value)
    input.dispatchEvent(new dom.InputEvent('input', { bubbles: true }))
    input.dispatchEvent(new dom.Event('change', { bubbles: true }))
  })
}

async function submit(host: HTMLDivElement) {
  const form = host.querySelector('form')
  assert.ok(form)
  await act(async () => {
    form.dispatchEvent(
      new dom.Event('submit', { bubbles: true, cancelable: true })
    )
  })
}

async function passwordLogin(host: HTMLDivElement) {
  await fill(host.querySelector('input[name="username"]'), 'otp-fixture')
  await fill(host.querySelector('input[name="password"]'), 'fixture-password')
  await submit(host)
}

async function signUp(host: HTMLDivElement) {
  await fill(host.querySelector('input[name="username"]'), 'otp-fixture')
  await fill(host.querySelector('input[name="password"]'), 'fixture-password')
  await fill(
    host.querySelector('input[name="confirmPassword"]'),
    'fixture-password'
  )
  await submit(host)
}

async function clickLink(host: HTMLDivElement, label: string) {
  const link = [...host.querySelectorAll('a')].find(
    (item) => item.textContent === label
  )
  assert.ok(link)
  await act(async () =>
    link.dispatchEvent(
      new dom.MouseEvent('click', {
        bubbles: true,
        cancelable: true,
        button: 0,
      })
    )
  )
}

async function otpLogin(host: HTMLDivElement) {
  for (const [index, digit] of [...'123456'].entries()) {
    await fill(
      host.querySelectorAll<HTMLInputElement>(
        'input[data-slot="input-otp-slot"]'
      )[index] ?? null,
      digit
    )
  }
  await submit(host)
}

test('password and OTP retain the product SKU, quantity, encoded query and hash', async () => {
  const { host, router } = await mount(
    `/sign-in?redirect=${encodeURIComponent(target)}`
  )
  await passwordLogin(host)
  assert.equal(router.state.location.pathname, '/otp')
  assert.equal(router.state.location.search.redirect, target)
  assert.equal(useAuthStore.getState().auth.user, null)
  assert.equal(
    useAuthStore.getState().auth.pending2FAFlowToken,
    'otp-redirect-flow'
  )
  await otpLogin(host)
  assert.equal(router.state.location.href, target)
  assert.equal(useAuthStore.getState().auth.user?.id, bundle.user.id)
  assert.deepEqual(calls.at(-1), {
    url: '/api/user/login/2fa',
    body: { code: '123456', flow_token: 'otp-redirect-flow' },
  })
})

test('a same-origin absolute target returns as a normalized internal URL', async () => {
  const { host, router } = await mount(
    `/sign-in?redirect=${encodeURIComponent(origin + target)}`
  )
  await passwordLogin(host)
  assert.equal(router.state.location.search.redirect, target)
  await otpLogin(host)
  assert.equal(router.state.location.href, target)
})

test('password and OTP without a redirect keep the default landing route', async () => {
  const { host, router } = await mount('/sign-in')
  await passwordLogin(host)
  assert.equal(router.state.location.pathname, '/otp')
  assert.equal(router.state.location.search.redirect, undefined)
  await otpLogin(host)
  assert.equal(router.state.location.pathname, '/getting-started')
})

for (const unsafe of [
  '//attacker.example/path',
  'https://attacker.example/path',
  'javascript:alert(1)',
]) {
  test(`rejects an unsafe target through password and OTP: ${unsafe}`, async () => {
    const { host, router } = await mount(
      `/sign-in?redirect=${encodeURIComponent(unsafe)}`
    )
    await passwordLogin(host)
    assert.equal(router.state.location.pathname, '/otp')
    assert.equal(router.state.location.search.redirect, undefined)
    await otpLogin(host)
    assert.equal(router.state.location.pathname, '/getting-started')
    assert.equal(window.location.origin, origin)
  })
}

test('a directly opened OTP URL also rejects a malicious return target', async () => {
  useAuthStore.getState().auth.setPending2FAFlowToken('otp-redirect-flow')
  const { host, router } = await mount(
    `/otp?redirect=${encodeURIComponent('https://attacker.example/path')}`
  )
  const relogin = [...host.querySelectorAll('a')].find(
    (link) => link.textContent === 'Re-login'
  )
  assert.ok(relogin)
  assert.equal(relogin.getAttribute('href'), '/sign-in')
  await otpLogin(host)
  assert.equal(router.state.location.pathname, '/getting-started')
})

for (const action of ['Back to login', 'Re-login']) {
  test(`${action} preserves the safe product return target`, async () => {
    const { host, router } = await mount(
      `/otp?redirect=${encodeURIComponent(target)}`
    )
    const control = [...host.querySelectorAll('button,a')].find(
      (item) => item.textContent === action
    )
    assert.ok(control)
    await act(async () =>
      control.dispatchEvent(
        new dom.MouseEvent('click', {
          bubbles: true,
          cancelable: true,
          button: 0,
        })
      )
    )
    assert.equal(router.state.location.pathname, '/sign-in')
    assert.equal(router.state.location.search.redirect, target)
  })
}

test('an expired OTP flow keeps its safe target when restarting sign-in', async () => {
  const { host, router } = await mount(
    `/otp?redirect=${encodeURIComponent(target)}`
  )
  await otpLogin(host)
  assert.equal(router.state.location.pathname, '/sign-in')
  assert.equal(router.state.location.search.redirect, target)
  assert.equal(calls.length, 0)
})

test('sign-up links and registration success keep the original product through sign-in and OTP', async () => {
  const { host, router } = await mount(
    `/sign-in?redirect=${encodeURIComponent(target)}`
  )
  await clickLink(host, 'Sign up')
  assert.equal(router.state.location.pathname, '/sign-up')
  assert.equal(router.state.location.search.redirect, target)
  await clickLink(host, 'Sign in')
  assert.equal(router.state.location.pathname, '/sign-in')
  assert.equal(router.state.location.search.redirect, target)
  await clickLink(host, 'Sign up')
  await signUp(host)
  assert.equal(calls[0]?.url, '/api/user/register')
  assert.equal(router.state.location.pathname, '/sign-in')
  assert.equal(router.state.location.search.redirect, target)
  await passwordLogin(host)
  await otpLogin(host)
  assert.equal(router.state.location.href, target)
})

test('registration without a return target keeps the normal sign-in and default landing', async () => {
  const { host, router } = await mount('/sign-up')
  await signUp(host)
  assert.equal(router.state.location.pathname, '/sign-in')
  assert.equal(router.state.location.search.redirect, undefined)
  await passwordLogin(host)
  await otpLogin(host)
  assert.equal(router.state.location.pathname, '/getting-started')
})

for (const unsafe of [
  '//attacker.example/path',
  'https://attacker.example/path',
  'javascript:alert(1)',
]) {
  test(`registration does not retain an unsafe return target: ${unsafe}`, async () => {
    const { host, router } = await mount(
      `/sign-up?redirect=${encodeURIComponent(unsafe)}`
    )
    const link = [...host.querySelectorAll('a')].find(
      (item) => item.textContent === 'Sign in'
    )
    assert.ok(link)
    assert.equal(link.getAttribute('href'), '/sign-in')
    await signUp(host)
    assert.equal(router.state.location.pathname, '/sign-in')
    assert.equal(router.state.location.search.redirect, undefined)
    await passwordLogin(host)
    await otpLogin(host)
    assert.equal(router.state.location.pathname, '/getting-started')
  })
}

test('disabled registration stays disabled while its sign-in link preserves a safe target', async () => {
  registrationEnabled = false
  const { host, router } = await mount(
    `/sign-up?redirect=${encodeURIComponent(target)}`
  )
  assert.equal(host.querySelector('form'), null)
  await clickLink(host, 'Sign in')
  assert.equal(router.state.location.pathname, '/sign-in')
  assert.equal(router.state.location.search.redirect, target)
  assert.equal(calls.length, 0)
})
