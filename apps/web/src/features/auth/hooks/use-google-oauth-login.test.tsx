/*
Copyright (C) 2026 LIghtJUNction

SPDX-License-Identifier: AGPL-3.0-or-later
*/
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

import { GOOGLE_BUILT_IN_PRESET } from '../lib/google-oauth'
import type { SystemStatus } from '../types'

const dom = new Window({ url: 'https://site.example.test/sign-in' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
  'Element',
  'Event',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const calls: { path: string; payload: unknown; options: unknown }[] = []
const navigations: string[] = []
const errors: string[] = []
Object.defineProperty(dom.location, 'assign', {
  configurable: true,
  value: (url: string) => navigations.push(url),
})
let response: unknown
let forbiddenCalls = 0
const t = (key: string) => key
mock.module('react-i18next', () => ({ useTranslation: () => ({ t }) }))
mock.module('sonner', () => ({
  toast: { error: (value: string) => errors.push(value), success: () => {} },
}))
mock.module('@/lib/api', () => ({
  api: {
    post: async (path: string, payload: unknown, options: unknown) => {
      calls.push({ path, payload, options })
      return { data: response }
    },
  },
  clearAuthentication: () => {
    forbiddenCalls++
  },
  isAuthBundle: () => false,
}))
mock.module('../api', () => ({
  createOAuthFlow: async () => {
    forbiddenCalls++
    throw new Error('legacy flow not supported')
  },
  logout: async () => {
    forbiddenCalls++
    throw new Error('not needed for Google bridge')
  },
  startTelegramLogin: async () => '',
  telegramLogin: async () => ({ success: false }),
}))
mock.module('./use-auth-redirect', () => ({
  useAuthRedirect: () => ({
    handleLoginSuccess: async () => {
      forbiddenCalls++
    },
  }),
}))
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { useOAuthLogin } = await import('./use-oauth-login')
let root: ReturnType<typeof createRoot> | undefined
let login: () => Promise<void>
function Harness({
  status,
  acceptedLegal,
}: {
  status: SystemStatus
  acceptedLegal: boolean
}) {
  login = useOAuthLogin(status, '/console', acceptedLegal).handleGoogleLogin
  return null
}
const supported = { google_oauth: true, google_client_id: 'test-client' }
function validResponse() {
  const url = new URL(GOOGLE_BUILT_IN_PRESET.authorization_endpoint)
  for (const [key, value] of Object.entries({
    client_id: 'test-client',
    response_type: 'code',
    scope: 'openid email profile',
    state: 'synthetic-state',
    nonce: 'synthetic-nonce',
    code_challenge: 'synthetic-proof',
    code_challenge_method: 'S256',
    redirect_uri: 'https://site.example.test/api/oauth/google/callback',
  })) {
    url.searchParams.set(key, value)
  }
  return { success: true, data: { authorization_url: url.toString() } }
}
async function mount(status: SystemStatus = supported, acceptedLegal = false) {
  const host = document.createElement('div')
  document.body.append(host)
  const currentRoot = createRoot(host)
  root = currentRoot
  await act(async () =>
    currentRoot.render(
      <Harness status={status} acceptedLegal={acceptedLegal} />
    )
  )
}
afterEach(async () => {
  const currentRoot = root
  if (currentRoot) await act(async () => currentRoot.unmount())
  root = undefined
  document.body.replaceChildren()
  calls.length = navigations.length = errors.length = forbiddenCalls = 0
})
after(() => dom.close())

describe('built-in Google hook contract', () => {
  test('POSTs actual consent to browser bridge, validates authorization redirect, does not claim login success', async () => {
    response = validResponse()
    await mount(supported, true)
    await act(async () => login())
    assert.deepEqual(calls, [
      {
        path: '/api/oauth/google',
        payload: { accepted_legal: true },
        options: { skipAuthRefresh: true },
      },
    ])
    assert.deepEqual(navigations, [validResponse().data.authorization_url])
    assert.equal(forbiddenCalls, 0)
    assert.deepEqual(errors, [])
  })

  test('legal consent failure makes no start request or navigation', async () => {
    response = validResponse()
    await mount({ ...supported, user_agreement_enabled: true }, false)
    await act(async () => login())
    assert.deepEqual(calls, [])
    assert.deepEqual(navigations, [])
    assert.equal(errors.length, 1)
  })

  test('missing native browser capability makes no request', async () => {
    response = validResponse()
    await mount({ ...supported, google_oauth: false })
    await act(async () => login())
    assert.deepEqual(calls, [])
    assert.deepEqual(navigations, [])
  })

  test('never adapts native-token JSON or follows foreign redirect as browser acceptance', async () => {
    await mount()
    for (const bad of [
      { success: true, data: { access_token: 'synthetic-native-token' } },
      { success: false, data: validResponse().data },
      {
        success: true,
        data: { authorization_url: 'https://attacker.example.test' },
      },
    ]) {
      response = bad
      await act(async () => login())
    }
    assert.equal(calls.length, 3)
    assert.deepEqual(navigations, [])
    assert.equal(errors.length, 3)
    assert.equal(forbiddenCalls, 0)
  })
})
