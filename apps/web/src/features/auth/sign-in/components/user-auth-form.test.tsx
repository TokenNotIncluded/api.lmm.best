/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, test } from 'node:test'

import { AxiosError } from 'axios'
import { Window } from 'happy-dom'
import type { ReactNode } from 'react'

const unexpectedRequests: string[] = []
function rejectNetwork({ request }: { request: { url: string } }): never {
  unexpectedRequests.push(request.url)
  throw new Error(
    `Unexpected network request in sign-in form test: ${request.url}`
  )
}

const dom = new Window({
  url: 'https://console.example.test/sign-in',
  settings: {
    fetch: {
      interceptor: {
        beforeAsyncRequest: async (context) => rejectNetwork(context),
        beforeSyncRequest: rejectNetwork,
      },
    },
  },
})
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
  'HTMLButtonElement',
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

const toastErrors: string[] = []
const toastSuccesses: string[] = []
const loginCalls: unknown[] = []
const wechatStartCalls: unknown[] = []
const oauthClicks: string[] = []
const successBundles: unknown[] = []
const pending2FATokens: string[] = []
let activeStatus: Record<string, unknown> | null = null
let loginImpl: (payload: unknown) => Promise<unknown> = async () => ({
  success: true,
  data: { token: 'test-token', user: { id: 1, username: 'alice' } },
})

mock.module('sonner', () => ({
  toast: {
    error: (message: string) => {
      toastErrors.push(message)
      return 'error-toast'
    },
    success: (message: string) => {
      toastSuccesses.push(message)
      return 'success-toast'
    },
    info: (message: string) => {
      toastErrors.push(message)
      return 'info-toast'
    },
  },
}))

mock.module('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, values?: Record<string, unknown>) => {
      if (!values) return key
      return Object.entries(values).reduce(
        (message, [name, value]) =>
          message.replaceAll(`{{${name}}}`, String(value)),
        key
      )
    },
  }),
}))

mock.module('@tanstack/react-router', () => ({
  Link: ({ to, children, ...props }: { to: string; children: ReactNode }) => (
    <a href={to} {...props}>
      {children}
    </a>
  ),
}))

mock.module('@/features/auth/api', () => ({
  login: async (payload: unknown) => {
    loginCalls.push(payload)
    return loginImpl(payload)
  },
  startWechatLogin: async (acceptedLegal: boolean) => {
    wechatStartCalls.push(acceptedLegal)
    return 'wechat-flow-token'
  },
  wechatLoginByCode: async () => ({ success: true, data: {} }),
}))

mock.module('@/features/auth/hooks/use-auth-redirect', () => ({
  useAuthRedirect: () => ({
    handleLoginSuccess: async (bundle: unknown) => {
      successBundles.push(bundle)
    },
    redirectTo2FA: () => {},
  }),
}))

mock.module('@/features/auth/hooks/use-turnstile', () => ({
  useTurnstile: () => ({
    isTurnstileEnabled: false,
    turnstileSiteKey: '',
    turnstileToken: 'turnstile-token',
    setTurnstileToken: () => {},
    validateTurnstile: () => true,
  }),
}))

mock.module('@/hooks/use-status', () => ({
  useStatus: () => ({ status: activeStatus }),
}))

mock.module('@/features/auth/passkey', () => ({
  beginPasskeyLogin: async () => ({ success: false, message: 'unused' }),
  finishPasskeyLogin: async () => ({ success: false, message: 'unused' }),
}))

mock.module('@/lib/passkey', () => ({
  buildAssertionResult: () => ({}),
  prepareCredentialRequestOptions: (options: unknown) => options,
  isPasskeySupported: async () => true,
}))

mock.module('@/lib/api', () => ({
  isAuthBundle: () => true,
}))

mock.module('@/stores/auth-store', () => ({
  useAuthStore: (selector: (state: unknown) => (token: string) => void) =>
    selector({
      auth: {
        setPending2FAFlowToken: (token: string) => pending2FATokens.push(token),
      },
    }),
}))

mock.module('@/features/auth/hooks/use-oauth-login', () => ({
  useOAuthLogin: () => ({
    isLoading: false,
    githubButtonText: 'Continue with GitHub',
    githubButtonDisabled: false,
    handleGitHubLogin: () => oauthClicks.push('github'),
    handleDiscordLogin: () => oauthClicks.push('discord'),
    handleOIDCLogin: () => oauthClicks.push('oidc'),
    handleLinuxDOLogin: () => oauthClicks.push('linuxdo'),
    handleTelegramLogin: () => oauthClicks.push('telegram'),
    handleCustomOAuthLogin: (provider: { slug: string }) =>
      oauthClicks.push(`custom:${provider.slug}`),
    isTelegramDialogOpen: false,
    isTelegramPending: false,
    handleTelegramAuthorization: () => {},
    handleTelegramDialogChange: () => {},
  }),
}))

mock.module('@/assets/brand-icons', () => ({
  IconDiscord: () => <span aria-hidden='true' />,
  IconGithub: () => <span aria-hidden='true' />,
  IconGoogle: () => <span aria-hidden='true' />,
  IconLinuxDo: () => <span aria-hidden='true' />,
  IconTelegram: () => <span aria-hidden='true' />,
  IconWeChat: () => <span aria-hidden='true' />,
}))

mock.module('@/features/auth/components/telegram-login-dialog', () => ({
  TelegramLoginDialog: () => null,
}))

mock.module('./account-appeal-form', () => ({
  AccountAppealForm: () => <div data-testid='account-appeal-form' />,
}))

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { UserAuthForm } = await import('./user-auth-form')

type Root = ReturnType<typeof createRoot>
let root: Root | undefined

const flush = () => new Promise((resolve) => setTimeout(resolve, 20))

async function waitFor(check: () => boolean, message: string) {
  for (let attempt = 0; attempt < 30 && !check(); attempt += 1) {
    await act(flush)
  }
  assert.ok(check(), message)
}

async function render(status: Record<string, unknown> | null) {
  activeStatus = status
  const host = document.createElement('div')
  document.body.append(host)
  const activeRoot = createRoot(host)
  root = activeRoot
  await act(async () => {
    activeRoot.render(<UserAuthForm />)
    await flush()
  })
  await act(flush)
  return host
}

function buttonByName(host: HTMLElement, name: string) {
  const button = [...host.querySelectorAll('button')].find((candidate) =>
    candidate.textContent?.includes(name)
  )
  assert.ok(button, `missing button: ${name}`)
  return button as HTMLButtonElement
}

function buttonByAccessibleName(host: HTMLElement, name: string) {
  const button = [...host.querySelectorAll('button')].find(
    (candidate) =>
      candidate.getAttribute('aria-label') === name ||
      candidate.textContent?.includes(name)
  )
  assert.ok(button, `missing button: ${name}`)
  return button as HTMLButtonElement
}

function inputByName(host: HTMLElement, name: string) {
  const input = host.querySelector(`input[name="${name}"]`)
  assert.ok(input, `missing input: ${name}`)
  return input as HTMLInputElement
}

function passwordSubmitButton(host: HTMLElement) {
  const button = host.querySelector('button[type="submit"]')
  assert.ok(button, 'missing password submit button')
  return button as HTMLButtonElement
}

const setNativeInputValue = Object.getOwnPropertyDescriptor(
  dom.HTMLInputElement.prototype,
  'value'
)?.set
assert.ok(setNativeInputValue)

async function setInputValue(input: HTMLInputElement, value: string) {
  await act(async () => {
    assert.ok(setNativeInputValue, 'native input setter is available')
    setNativeInputValue.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await flush()
  })
}

async function click(element: HTMLElement) {
  await act(async () => {
    element.click()
    await flush()
  })
}

async function fillCredentials(host: HTMLElement) {
  await setInputValue(inputByName(host, 'username'), 'alice@example.test')
  await setInputValue(inputByName(host, 'password'), 'correct horse')
}

async function submitPassword(host: HTMLElement) {
  await click(passwordSubmitButton(host))
}

beforeEach(() => {
  activeStatus = null
  loginImpl = async () => ({
    success: true,
    data: { token: 'test-token', user: { id: 1, username: 'alice' } },
  })
})

afterEach(async () => {
  const activeRoot = root
  if (activeRoot) await act(() => activeRoot.unmount())
  root = undefined
  document.body.innerHTML = ''
  toastErrors.length = 0
  toastSuccesses.length = 0
  loginCalls.length = 0
  wechatStartCalls.length = 0
  oauthClicks.length = 0
  successBundles.length = 0
  pending2FATokens.length = 0
  localStorage.clear()
  sessionStorage.clear()
  assert.deepEqual(unexpectedRequests.splice(0), [])
})

after(() => {
  dom.close()
  for (const [key, descriptor] of originalGlobals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
})

test('missing legal consent leaves password submit clickable and uses existing toast gate', async () => {
  const host = await render({
    user_agreement_enabled: true,
    privacy_policy_enabled: true,
  })
  await fillCredentials(host)

  const submit = passwordSubmitButton(host)
  assert.equal(submit.disabled, false)

  await submitPassword(host)

  assert.deepEqual(toastErrors, ['Please agree to the legal terms first'])
  assert.equal(loginCalls.length, 0)
  assert.equal(successBundles.length, 0)
})

test('accepted legal consent keeps the existing password login path', async () => {
  const host = await render({
    user_agreement_enabled: true,
    privacy_policy_enabled: true,
  })
  await fillCredentials(host)

  const consent = host.querySelector('#legal-consent')
  assert.ok(consent, 'legal consent control is rendered')
  await click(consent as HTMLElement)

  await submitPassword(host)

  assert.deepEqual(toastErrors, [])
  assert.equal(loginCalls.length, 1)
  assert.deepEqual(loginCalls[0], {
    username: 'alice@example.test',
    password: 'correct horse',
    turnstile: 'turnstile-token',
  })
  assert.equal(successBundles.length, 1)
  assert.deepEqual(toastSuccesses, ['Welcome back!'])
})

test('without a legal requirement the password login path is unchanged', async () => {
  const host = await render({
    user_agreement_enabled: false,
    privacy_policy_enabled: false,
  })
  await fillCredentials(host)

  assert.equal(
    host.textContent?.includes('I have read and agree to the'),
    false
  )
  await submitPassword(host)

  assert.deepEqual(toastErrors, [])
  assert.equal(loginCalls.length, 1)
  assert.deepEqual(loginCalls[0], {
    username: 'alice@example.test',
    password: 'correct horse',
    turnstile: 'turnstile-token',
  })
  assert.equal(successBundles.length, 1)
})

test('password submit is disabled while login is loading', async () => {
  let resolveLogin: (value: unknown) => void = () => {}
  loginImpl = async () =>
    new Promise((resolve) => {
      resolveLogin = resolve
    })
  const host = await render({
    user_agreement_enabled: false,
    privacy_policy_enabled: false,
  })
  await fillCredentials(host)

  const submit = passwordSubmitButton(host)
  assert.equal(submit.disabled, false)
  await submitPassword(host)

  await waitFor(
    () => submit.disabled,
    'password submit did not enter loading state'
  )
  assert.equal(loginCalls.length, 1)

  await act(async () => {
    resolveLogin({
      success: true,
      data: { token: 'test-token', user: { id: 1, username: 'alice' } },
    })
    await flush()
  })
  await waitFor(
    () => !submit.disabled,
    'password submit did not leave loading state'
  )
})

test('legal-consent policies for Passkey OAuth and WeChat remain disabled before consent', async () => {
  const host = await render({
    user_agreement_enabled: true,
    privacy_policy_enabled: true,
    passkey_login: true,
    wechat_login: true,
    custom_oauth_providers: [
      {
        name: 'Google',
        slug: 'google',
        icon: 'google',
        authorization_endpoint: 'https://accounts.google.example.test/oauth',
        client_id: 'google-client',
      },
    ],
  })

  await waitFor(
    () => host.textContent?.includes('Sign in with Passkey') === true,
    'passkey login did not render'
  )

  assert.equal(passwordSubmitButton(host).disabled, false)
  assert.equal(buttonByName(host, 'Sign in with Passkey').disabled, true)
  assert.equal(
    buttonByAccessibleName(host, 'Continue with WeChat').disabled,
    true
  )
  assert.equal(
    buttonByAccessibleName(host, 'Continue with Google').disabled,
    true
  )

  await fillCredentials(host)
  await submitPassword(host)
  assert.deepEqual(toastErrors, ['Please agree to the legal terms first'])
  assert.equal(loginCalls.length, 0)
  assert.deepEqual(wechatStartCalls, [])
  assert.deepEqual(oauthClicks, [])

  const consent = host.querySelector('#legal-consent')
  assert.ok(consent)
  await click(consent as HTMLElement)
  assert.equal(buttonByName(host, 'Sign in with Passkey').disabled, false)
  assert.equal(
    buttonByAccessibleName(host, 'Continue with WeChat').disabled,
    false
  )
  assert.equal(
    buttonByAccessibleName(host, 'Continue with Google').disabled,
    false
  )

  await click(buttonByAccessibleName(host, 'Continue with Google'))
  assert.deepEqual(oauthClicks, ['custom:google'])
  await click(consent as HTMLElement)
  assert.equal(buttonByName(host, 'Sign in with Passkey').disabled, true)
  assert.equal(
    buttonByAccessibleName(host, 'Continue with WeChat').disabled,
    true
  )
  assert.equal(
    buttonByAccessibleName(host, 'Continue with Google').disabled,
    true
  )
  assert.deepEqual(wechatStartCalls, [])
})

for (const [terms, privacy] of [
  [true, false],
  [false, true],
  [true, true],
] as const) {
  test(`terms=${terms}, privacy=${privacy} requires explicit consent and blocks again after withdrawal`, async () => {
    const host = await render({
      user_agreement_enabled: terms,
      privacy_policy_enabled: privacy,
    })
    await fillCredentials(host)

    const consent = host.querySelector<HTMLInputElement>('#legal-consent')
    assert.ok(consent, 'consent control is rendered')
    assert.equal(consent.checked, false)
    assert.equal(
      Boolean(host.querySelector('a[href="/user-agreement"]')),
      terms
    )
    assert.equal(
      Boolean(host.querySelector('a[href="/privacy-policy"]')),
      privacy
    )
    assert.equal(passwordSubmitButton(host).disabled, false)

    await submitPassword(host)
    assert.deepEqual(toastErrors, ['Please agree to the legal terms first'])
    assert.equal(loginCalls.length, 0)
    assert.equal(successBundles.length, 0)

    await click(consent as HTMLElement)
    assert.equal(consent.checked, true)
    await submitPassword(host)
    assert.equal(loginCalls.length, 1)
    assert.equal(successBundles.length, 1)
    assert.deepEqual(toastSuccesses, ['Welcome back!'])

    await click(consent as HTMLElement)
    assert.equal(consent.checked, false)
    await submitPassword(host)
    assert.equal(
      loginCalls.length,
      1,
      'withdrawn consent must not send a request'
    )
    assert.equal(successBundles.length, 1)
    assert.deepEqual(toastErrors, [
      'Please agree to the legal terms first',
      'Please agree to the legal terms first',
    ])
  })
}

for (const status of [
  { password_login_enabled: false },
  { data: { password_login_enabled: false } },
]) {
  test(`password login disabled by ${JSON.stringify(status)} stays unavailable`, async () => {
    const host = await render({
      ...status,
      user_agreement_enabled: true,
      privacy_policy_enabled: true,
    })
    assert.equal(host.querySelector('button[type="submit"]'), null)
    assert.equal(host.querySelector('input[name="username"]'), null)
    assert.equal(host.querySelector('input[name="password"]'), null)
    const consent = host.querySelector('#legal-consent')
    assert.ok(consent)
    await click(consent as HTMLElement)
    assert.equal(host.querySelector('button[type="submit"]'), null)
    assert.equal(loginCalls.length, 0)
  })
}

for (const [name, error, expectedErrors] of [
  ['client failure', new Error('Login flow expired'), ['Login flow expired']],
  ['unknown failure', 'unknown failure', ['Login failed']],
  ['Axios failure handled centrally', new AxiosError('Network error'), []],
] as const) {
  test(`${name} clears loading without bypassing consent on retry`, async () => {
    let rejectLogin: (error: unknown) => void = () => {}
    loginImpl = async () =>
      new Promise((_resolve, reject) => {
        rejectLogin = reject
      })
    const host = await render({
      user_agreement_enabled: true,
      privacy_policy_enabled: true,
    })
    await fillCredentials(host)
    const consent = host.querySelector('#legal-consent')
    assert.ok(consent)
    await click(consent as HTMLElement)
    const submit = passwordSubmitButton(host)
    await submitPassword(host)
    await waitFor(() => submit.disabled, 'submit did not enter loading state')
    await submitPassword(host)
    assert.equal(loginCalls.length, 1, 'disabled submit must not repeat login')

    await act(async () => {
      rejectLogin(error)
      await flush()
    })
    await waitFor(
      () => !submit.disabled,
      'submit stayed disabled after failure'
    )
    assert.deepEqual(toastErrors, [...expectedErrors])
    assert.deepEqual(toastSuccesses, [])
    assert.equal(successBundles.length, 0)

    await click(consent as HTMLElement)
    await submitPassword(host)
    assert.equal(loginCalls.length, 1)
    assert.deepEqual(toastErrors, [
      ...expectedErrors,
      'Please agree to the legal terms first',
    ])

    loginImpl = async () => ({
      success: true,
      data: { token: 'test-token', user: { id: 1, username: 'alice' } },
    })
    await click(consent as HTMLElement)
    await submitPassword(host)
    assert.equal(loginCalls.length, 2)
    assert.equal(successBundles.length, 1)
    assert.deepEqual(toastSuccesses, ['Welcome back!'])
    assert.equal(submit.disabled, false)
  })
}
