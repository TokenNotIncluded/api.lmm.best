/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
// @ts-expect-error Bun's test module is only available in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type { ComponentType, ReactNode } from 'react'

import type { AuthBundle } from '@/stores/auth-store'

const dom = new Window({ url: 'https://console.example.test/oauth/github' })
for (const key of [
  'window',
  'document',
  'navigator',
  'localStorage',
  'HTMLElement',
  'Element',
  'SVGElement',
  'Node',
  'Event',
  'MouseEvent',
  'DOMException',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const { act, StrictMode } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')

let search: Record<string, string> = {}
const navigations: Array<{
  href?: string
  to?: string
  flowTokenDuringNavigation: string | null
}> = []
const navigate = (target: { href?: string; to?: string }) => {
  navigations.push({
    ...target,
    flowTokenDuringNavigation: useAuthStore.getState().auth.pending2FAFlowToken,
  })
}
mock.module('@tanstack/react-router', () => ({
  createFileRoute: () => (options: { component: ComponentType }) => ({
    options,
  }),
  useNavigate: () => navigate,
  useParams: () => ({ provider: 'github' }),
  useSearch: () => search,
}))
mock.module('@/features/auth/auth-layout', () => ({
  AuthLayout: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}))
const { Route } = await import('./$provider')
const Callback = Route.options.component as ComponentType
const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})
const originalGet = api.get
const originalPost = api.post
const roots: ReturnType<typeof createRoot>[] = []

function callbackTree() {
  return (
    <StrictMode>
      <I18nextProvider i18n={i18n}>
        <Callback />
      </I18nextProvider>
    </StrictMode>
  )
}

async function mount() {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  roots.push(root)
  await act(async () => {
    root.render(callbackTree())
  })
  return host
}

function credential(): PublicKeyCredential {
  return {
    id: 'bound-passkey',
    rawId: new Uint8Array([1, 2]).buffer,
    type: 'public-key',
    response: {
      authenticatorData: new Uint8Array([3]).buffer,
      clientDataJSON: new Uint8Array([4]).buffer,
      signature: new Uint8Array([5]).buffer,
      userHandle: null,
    },
    getClientExtensionResults: () => ({}),
  } as unknown as PublicKeyCredential
}

const bundle: AuthBundle = {
  access_token: 'test-access-token',
  token_type: 'Bearer',
  access_expires_at: 2_000_000_000,
  user: { id: 7, username: 'oauth-user', role: 1 },
  session: {
    sid: 'oauth-session',
    current: true,
    login_method: 'passkey',
    ip: '',
    user_agent: '',
    created_at: 1,
    last_active_at: 1,
    expires_at: 2_000_000_000,
  },
}

function respondWithChallenge(data: unknown) {
  search = { code: 'oauth-code', state: 'oauth-state', redirect: '/wallet' }
  api.get = (async () => ({ data: { success: true, data } })) as typeof api.get
}

afterEach(async () => {
  for (const root of roots.splice(0)) await act(async () => root.unmount())
  api.get = originalGet
  api.post = originalPost
  useAuthStore.getState().auth.reset('complete')
  navigations.length = 0
  document.body.replaceChildren()
})
after(() => {
  mock.restore()
  dom.close()
})

test('StrictMode exchanges once and stores the 2FA challenge before navigating', async () => {
  search = { code: 'oauth-code', state: 'oauth-state' }
  let exchanges = 0
  let resolve!: (response: unknown) => void
  const response = new Promise((next) => {
    resolve = next
  })
  api.get = (async () => {
    exchanges++
    return response
  }) as typeof api.get
  await mount()
  await act(async () => {
    resolve({
      data: {
        success: true,
        data: { require_2fa: true, flow_token: 'bound-2fa-flow' },
      },
    })
  })
  assert.equal(exchanges, 1)
  assert.deepEqual(navigations, [
    { to: '/otp', replace: true, flowTokenDuringNavigation: 'bound-2fa-flow' },
  ])
  assert.equal(useAuthStore.getState().auth.accessToken, null)
})

test('a missing secondary challenge token cannot establish a session', async () => {
  respondWithChallenge({ require_2fa: true, ...bundle })
  await mount()
  assert.equal(useAuthStore.getState().auth.accessToken, null)
  assert.equal(navigations.at(-1)?.href, '/sign-in')
})

test('Passkey waits for a click, uses the bound challenge, and preserves the redirect', async () => {
  respondWithChallenge({
    require_passkey: true,
    flow_token: 'bound-passkey-flow',
    options: { publicKey: { challenge: 'AQI', rpId: 'example.test' } },
  })
  let getCalls = 0
  let resolveCredential!: (value: PublicKeyCredential | null) => void
  const pendingCredential = new Promise<PublicKeyCredential | null>((next) => {
    resolveCredential = next
  })
  Object.defineProperty(navigator, 'credentials', {
    configurable: true,
    value: {
      get: async (options: CredentialRequestOptions) => {
        getCalls++
        assert.equal(options.publicKey?.rpId, 'example.test')
        assert.ok(options.publicKey)
        assert.deepEqual(
          Array.from(
            new Uint8Array(options.publicKey.challenge as ArrayBuffer)
          ),
          [1, 2]
        )
        return pendingCredential
      },
    },
  })
  const posts: Array<{ url: string; payload: unknown }> = []
  api.post = (async (url, payload) => {
    posts.push({ url, payload })
    return { data: { success: true, data: bundle } }
  }) as typeof api.post
  const host = await mount()
  const verify = host.querySelector<HTMLButtonElement>('button')
  assert.ok(verify)
  assert.equal(verify.textContent, 'Sign in with Passkey')
  assert.equal(getCalls, 0)
  assert.equal(posts.length, 0)
  assert.equal(navigations.length, 0)
  await act(async () => {
    verify.click()
    verify.click()
  })
  assert.equal(getCalls, 1)
  assert.equal(verify.disabled, true)
  await act(async () => resolveCredential(credential()))
  assert.equal(posts.length, 1)
  assert.equal(posts[0].url, '/api/user/passkey/login/finish')
  const payload = posts[0].payload as {
    flow_token: string
    credential: { response: { signature: string } }
  }
  assert.equal(payload.flow_token, 'bound-passkey-flow')
  assert.equal(payload.credential.response.signature, 'BQ')
  assert.equal(useAuthStore.getState().auth.accessToken, bundle.access_token)
  assert.equal(navigations.at(-1)?.href, '/wallet')
  assert.equal(navigations.length, 1)
  assert.equal(window.location.search, '')
})

test('cancelling Passkey leaves a retry button without sending a finish request', async () => {
  respondWithChallenge({
    require_passkey: true,
    flow_token: 'bound-passkey-flow',
    options: { challenge: 'AQI' },
  })
  let getCalls = 0
  Object.defineProperty(navigator, 'credentials', {
    configurable: true,
    value: {
      get: async () => {
        getCalls++
        throw new DOMException('Cancelled', 'NotAllowedError')
      },
    },
  })
  let finishCalls = 0
  api.post = (async () => {
    finishCalls++
    throw new Error('must not finish a cancelled challenge')
  }) as typeof api.post
  const host = await mount()
  const verify = host.querySelector<HTMLButtonElement>('button')
  assert.ok(verify)
  await act(async () => verify.click())
  assert.equal(verify.disabled, false)
  await act(async () => verify.click())
  assert.equal(getCalls, 2)
  assert.equal(finishCalls, 0)
  assert.equal(navigations.length, 0)
  assert.equal(useAuthStore.getState().auth.accessToken, null)
})

test('an incomplete Passkey finish response cannot establish a session', async () => {
  respondWithChallenge({
    require_passkey: true,
    flow_token: 'bound-passkey-flow',
    options: { challenge: 'AQI' },
  })
  Object.defineProperty(navigator, 'credentials', {
    configurable: true,
    value: { get: async () => credential() },
  })
  api.post = (async () => ({
    data: { success: true, data: { user: bundle.user } },
  })) as typeof api.post
  const host = await mount()
  const verify = host.querySelector<HTMLButtonElement>('button')
  assert.ok(verify)
  await act(async () => verify.click())
  assert.equal(useAuthStore.getState().auth.accessToken, null)
  assert.equal(navigations.length, 0)
  assert.equal(verify.disabled, false)
})

test('a late Passkey finish cannot replace a newer callback challenge', async () => {
  respondWithChallenge({
    require_passkey: true,
    flow_token: 'old-passkey-flow',
    options: { challenge: 'AQI' },
  })
  Object.defineProperty(navigator, 'credentials', {
    configurable: true,
    value: { get: async () => credential() },
  })
  let resolveFinish!: (response: unknown) => void
  const pendingFinish = new Promise((next) => {
    resolveFinish = next
  })
  const tokens: string[] = []
  api.post = (async (_url, payload) => {
    tokens.push((payload as { flow_token: string }).flow_token)
    return tokens.length === 1
      ? pendingFinish
      : { data: { success: true, data: bundle } }
  }) as typeof api.post
  const host = await mount()
  const verify = host.querySelector<HTMLButtonElement>('button')
  assert.ok(verify)
  await act(async () => verify.click())
  assert.equal(verify.disabled, true)

  respondWithChallenge({
    require_passkey: true,
    flow_token: 'new-passkey-flow',
    options: { challenge: 'AwQ' },
  })
  search = { ...search, code: 'new-code', state: 'new-state' }
  await act(async () => roots.at(-1)?.render(callbackTree()))
  await act(async () => {
    resolveFinish({ data: { success: true, data: bundle } })
  })
  assert.equal(useAuthStore.getState().auth.accessToken, null)
  assert.equal(navigations.length, 0)
  const newVerify = host.querySelector<HTMLButtonElement>('button')
  assert.ok(newVerify)
  assert.equal(newVerify.disabled, false)
  await act(async () => newVerify.click())
  assert.deepEqual(tokens, ['old-passkey-flow', 'new-passkey-flow'])
  assert.equal(useAuthStore.getState().auth.accessToken, bundle.access_token)
})

test('returning to login invalidates an already pending Passkey finish', async () => {
  respondWithChallenge({
    require_passkey: true,
    flow_token: 'bound-passkey-flow',
    options: { challenge: 'AQI' },
  })
  Object.defineProperty(navigator, 'credentials', {
    configurable: true,
    value: { get: async () => credential() },
  })
  let resolveFinish!: (response: unknown) => void
  const pendingFinish = new Promise((next) => {
    resolveFinish = next
  })
  let finishCalls = 0
  api.post = (async () => {
    finishCalls++
    return pendingFinish
  }) as typeof api.post
  const host = await mount()
  const verify = host.querySelector<HTMLButtonElement>('button')
  const backToLogin = Array.from(host.querySelectorAll('button')).find(
    (button) => button.textContent === 'Back to login'
  )
  assert.ok(verify)
  assert.ok(backToLogin)
  await act(async () => verify.click())
  assert.equal(finishCalls, 1)
  await act(async () => backToLogin.click())
  assert.equal(navigations.at(-1)?.href, '/sign-in')
  await act(async () => {
    resolveFinish({ data: { success: true, data: bundle } })
  })
  assert.equal(useAuthStore.getState().auth.accessToken, null)
  assert.equal(navigations.length, 1)
  assert.equal(navigations[0].href, '/sign-in')
  assert.equal(
    host.querySelector('h2')?.textContent,
    'Signing you in with GitHub'
  )
})
