/*
Copyright (C) 2026 LIghtJUNction

SPDX-License-Identifier: AGPL-3.0-or-later
*/
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

import type { SystemStatus } from '../types'

const dom = new Window()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const clicks: string[] = []
const consent: boolean[] = []
mock.module('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, options?: { name?: string }) =>
      key.replace('{{name}}', options?.name ?? ''),
  }),
}))
mock.module('../hooks/use-oauth-login', () => ({
  useOAuthLogin: (_: unknown, __: unknown, acceptedLegal: boolean) => {
    consent.push(acceptedLegal)
    return {
      isLoading: false,
      githubButtonText: '',
      githubButtonDisabled: false,
      handleGoogleLogin: () => clicks.push('builtin:google'),
      handleGitHubLogin: () => clicks.push('github'),
      handleDiscordLogin: () => {},
      handleOIDCLogin: () => {},
      handleLinuxDOLogin: () => {},
      handleTelegramLogin: () => {},
      handleCustomOAuthLogin: (provider: { id: number }) =>
        clicks.push(`custom:${provider.id}`),
      isTelegramDialogOpen: false,
      isTelegramPending: false,
      handleTelegramAuthorization: () => {},
      handleTelegramDialogChange: () => {},
    }
  },
}))
mock.module('@/assets/brand-icons', () => {
  const icon = () => <span aria-hidden='true' />
  return {
    IconDiscord: icon,
    IconGithub: icon,
    IconGoogle: icon,
    IconLinuxDo: icon,
    IconTelegram: icon,
    IconWeChat: icon,
  }
})
mock.module('./telegram-login-dialog', () => ({
  TelegramLoginDialog: () => null,
}))
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { OAuthProviders } = await import('./oauth-providers')
let root: ReturnType<typeof createRoot> | undefined

const legacy = {
  id: 73,
  name: 'Google',
  slug: 'legacy-google',
  icon: 'google',
  client_id: 'legacy-client',
  authorization_endpoint: 'https://accounts.google.com/o/oauth2/v2/auth',
  scopes: 'openid profile email',
}
const supported = {
  google_oauth: true,
  google_client_id: 'builtin-client',
  custom_oauth_providers: [legacy],
}
async function render(
  status: SystemStatus | null,
  props: {
    registrationOnly?: boolean
    disabled?: boolean
    acceptedLegal?: boolean
  } = {}
) {
  const host = document.createElement('div')
  document.body.append(host)
  const currentRoot = createRoot(host)
  root = currentRoot
  await act(async () =>
    currentRoot.render(
      <OAuthProviders status={status} featureGoogle {...props} />
    )
  )
  return host
}
async function unmount() {
  const currentRoot = root
  if (currentRoot) await act(async () => currentRoot.unmount())
  root = undefined
}
function getButton(host: HTMLElement) {
  const button = host.querySelector<HTMLButtonElement>('button')
  assert.ok(button)
  return button
}
afterEach(async () => {
  await unmount()
  document.body.replaceChildren()
  clicks.length = 0
  consent.length = 0
})
after(() => dom.close())

describe('Google provider buttons', () => {
  test('native supported browser Google wins, custom duplicate is not invoked', async () => {
    const host = await render(supported, { acceptedLegal: true })
    const buttons = host.querySelectorAll<HTMLButtonElement>('button')
    assert.equal(buttons.length, 1)
    assert.equal(buttons[0].getAttribute('aria-label'), 'Continue with Google')
    await act(async () => buttons[0].click())
    assert.deepEqual(clicks, ['builtin:google'])
    assert.equal(consent.at(-1), true)
  })

  test('unsupported/disabled/unconfigured/missing-flag native preserves old custom button', async () => {
    for (const status of [
      { custom_oauth_providers: [legacy] },
      { ...supported, google_oauth: false },
      { ...supported, google_client_id: '' },
      { ...supported, google_oauth: undefined },
    ]) {
      const host = await render(status)
      const button = getButton(host)
      await act(async () => button.click())
      assert.equal(clicks.at(-1), 'custom:73')
      await unmount()
    }
  })

  test('multiple old custom Google records remain visible, only one featured', async () => {
    const host = await render({
      custom_oauth_providers: [legacy, { ...legacy, id: 74, slug: 'google' }],
    })
    const buttons = host.querySelectorAll<HTMLButtonElement>('button')
    assert.equal(buttons.length, 2)
    await act(async () => buttons[1].click())
    assert.deepEqual(clicks, ['custom:74'])
  })

  test('builtin registration disable preserves separately allowed legacy registration', async () => {
    const host = await render(
      { ...supported, oauth_registration_disabled_methods: ['google'] },
      { registrationOnly: true }
    )
    const button = getButton(host)
    await act(async () => button.click())
    assert.deepEqual(clicks, ['custom:73'])
  })

  test('registration method restrictions do not hide existing sign-in', async () => {
    const host = await render({
      ...supported,
      oauth_registration_disabled_methods: ['google', 'custom:legacy-google'],
    })
    await act(async () => getButton(host).click())
    assert.deepEqual(clicks, ['builtin:google'])
  })

  test('disabled consent gate remains disabled and does not invoke Google', async () => {
    const host = await render(supported, {
      disabled: true,
      acceptedLegal: false,
    })
    const button = getButton(host)
    assert.equal(button.disabled, true)
    await act(async () => button.click())
    assert.deepEqual(clicks, [])
    assert.equal(consent.at(-1), false)
  })

  test('missing flags with no fallback render no misleading native button', async () => {
    const host = await render({ google_client_id: 'native-only-client' })
    assert.equal(host.querySelector('button'), null)
  })
})
