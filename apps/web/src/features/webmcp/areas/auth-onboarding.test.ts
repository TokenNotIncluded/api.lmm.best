/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

import { consumeQueuedAssistantRequest } from '@/features/assistant/assistant-events'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import type { ModelContextTool, WebMcpRouter } from '../tool-kit'
import { authOnboardingTools } from './auth-onboarding'

const domWindow = new Window({ url: 'https://console.example.test/sign-in' })
Object.defineProperty(globalThis, 'window', {
  configurable: true,
  value: domWindow,
})
Object.defineProperty(globalThis, 'CustomEvent', {
  configurable: true,
  value: domWindow.CustomEvent,
})

const originalGet = api.get
const signal = () => new AbortController().signal

afterEach(() => {
  api.get = originalGet
  consumeQueuedAssistantRequest()
  window.sessionStorage.clear()
  useAuthStore.setState({
    auth: { ...useAuthStore.getState().auth, user: null },
  })
})

after(() => domWindow.close())

function buildTools() {
  const calls: Array<{ to: string; search?: Record<string, unknown> }> = []
  const router: WebMcpRouter = {
    navigate: async (options: {
      to: string
      search?: Record<string, unknown>
    }) => {
      calls.push(options)
      return undefined
    },
    subscribe: () => () => undefined,
  }
  return { tools: authOnboardingTools({ router }), calls }
}

function byName(tools: readonly ModelContextTool[], name: string) {
  const tool = tools.find((entry) => entry.name === name)
  assert.ok(tool, `${name} is registered`)
  return tool
}

function mockStatus(data: Record<string, unknown>) {
  api.get = (async () => ({ data: { success: true, data } })) as typeof api.get
}

const TOOL_NAMES = [
  'lmm_auth_methods',
  'lmm_auth_open',
  'lmm_onboarding_status',
  'lmm_onboarding_open_assistant',
  'lmm_setup_status',
] as const

test('every auth-onboarding tool is uniquely named and read-only', () => {
  const { tools } = buildTools()
  assert.equal(tools.length, TOOL_NAMES.length)
  const names = tools.map((tool) => tool.name)
  assert.equal(new Set(names).size, names.length)
  for (const name of TOOL_NAMES) {
    assert.ok(names.includes(name), `${name} is registered`)
  }
  for (const tool of tools) {
    assert.match(tool.name, /^lmm_(auth|onboarding|setup)_[a-z0-9_]+$/)
    // No tool here performs a write, a payment, or a sign-in.
    assert.equal(tool.annotations?.consequentialHint, undefined, tool.name)
    assert.ok((tool.title ?? '').length > 0, `${tool.name} has a title`)
    assert.ok(tool.description.length > 0, `${tool.name} has a description`)
  }
})

test('sign-in methods never leak a client ID, secret, or credential', async () => {
  mockStatus({
    github_oauth: true,
    github_client_id: 'Iv1.public-but-still-withheld',
    github_client_secret: 'super-secret',
    discord_oauth: true,
    discord_client_id: '',
    register_enabled: true,
    password_register_enabled: false,
    oauth_register_enabled: true,
    oauth_registration_disabled_methods: ['github'],
    custom_oauth_providers: [
      {
        slug: 'acme',
        client_id: 'acme-public',
        authorization_endpoint: 'https://a',
      },
    ],
  })
  const { tools } = buildTools()
  const result = (await byName(tools, 'lmm_auth_methods').execute(
    {},
    { signal: signal() }
  )) as Record<string, unknown>
  const serialized = JSON.stringify(result)
  assert.doesNotMatch(serialized, /super-secret/)
  assert.doesNotMatch(serialized, /Iv1\.public-but-still-withheld/)
  assert.equal(result.registration_open, true)
  assert.equal(result.password_registration, false)
  assert.deepEqual(
    result.providers as unknown[],
    [
      // GitHub can sign in, but registration for it is disabled.
      { method: 'github', label: 'GitHub', sign_in: true, register: false },
      // A flag without a client ID is not a real entry point.
      { method: 'discord', label: 'Discord', sign_in: false, register: false },
      { method: 'custom:acme', label: 'acme', sign_in: true, register: true },
    ].filter((row) => row.sign_in || row.register)
  )
})

test('a status without password login defaults to offering it', async () => {
  mockStatus({})
  const { tools } = buildTools()
  const result = (await byName(tools, 'lmm_auth_methods').execute(
    {},
    { signal: signal() }
  )) as Record<string, unknown>
  assert.equal(result.password_login, true)
  assert.equal(result.can_create_account, true)
  assert.equal((result.providers as unknown[]).length, 0)
})

test('opening an auth page navigates and rejects an unknown page', async () => {
  const { tools, calls } = buildTools()
  const open = byName(tools, 'lmm_auth_open')
  assert.deepEqual(
    await open.execute({ page: '/sign-up' }, { signal: signal() }),
    {
      path: '/sign-up',
      opened: true,
    }
  )
  assert.deepEqual(calls, [{ to: '/sign-up' }])
  await assert.rejects(
    open.execute({ page: '/admin' }, { signal: signal() }),
    /page must be one of/
  )
  assert.equal(calls.length, 1)
})

test('onboarding status reports L0 progress and the next step', async () => {
  useAuthStore.setState({
    auth: {
      ...useAuthStore.getState().auth,
      user: {
        id: 42,
        username: 'l0-progress',
        role: 1,
        developer_access_granted: false,
        onboarding: {
          activation_complete: false,
          credential_complete: false,
          first_request_complete: false,
          stage: 'activate',
          paid_activation_enabled: true,
          paid_activation_min_amount: 5,
        },
        trust_level_info: { level: 0, paid_amount: 2 },
      },
    } as never,
  })
  const { tools } = buildTools()
  const result = (await byName(tools, 'lmm_onboarding_status').execute(
    {},
    { signal: signal() }
  )) as Record<string, unknown>
  assert.equal(result.authenticated, true)
  assert.equal(result.developer_access_granted, false)
  assert.equal(result.stage, 'activate')
  assert.deepEqual(result.paid_access, {
    mode: 'topup',
    paid_amount_usd: 2,
    threshold_usd: 5,
    remaining_usd: 3,
    note: 'Displayed progress only. Topping up does not itself grant developer access; the server decides.',
  })
  assert.deepEqual(result.next_step, {
    path: '/getting-started',
    label: 'Check API access status',
  })
})

test('onboarding status stays readable while signed out', async () => {
  const { tools } = buildTools()
  const result = (await byName(tools, 'lmm_onboarding_status').execute(
    {},
    { signal: signal() }
  )) as Record<string, unknown>
  assert.equal(result.authenticated, false)
  assert.deepEqual(result.next_step, {
    path: '/sign-in',
    label: 'Sign in to get started',
  })
})

test('opening the assistant queues only a fixed preset', async () => {
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'activated',
    role: 1,
    developer_access_granted: true,
  })
  const { tools } = buildTools()
  const open = byName(tools, 'lmm_onboarding_open_assistant')
  await open.execute({ preset: 'human' }, { signal: signal() })
  assert.equal(consumeQueuedAssistantRequest()?.preset, 'human')
  await assert.rejects(
    open.execute({ preset: 'whatever' }, { signal: signal() }),
    /preset must be one of/
  )
  await assert.rejects(
    open.execute({}, { signal: signal() }),
    /preset is required/
  )
})

test('L0 onboarding help navigates to the inline page without opening a second assistant', async () => {
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'l0',
    role: 1,
    developer_access_granted: false,
  })
  const { tools, calls } = buildTools()
  await byName(tools, 'lmm_onboarding_open_assistant').execute(
    { preset: 'onboarding' },
    { signal: signal() }
  )
  assert.deepEqual(calls, [{ to: '/getting-started' }])
  assert.equal(consumeQueuedAssistantRequest(), undefined)
})

test('setup status reads without exposing setup credentials', async () => {
  mockStatus({
    setup: false,
    version: 'v1.2.3',
    system_name: 'LMM',
    root_password: 'must-not-leak',
  })
  const { tools } = buildTools()
  const result = (await byName(tools, 'lmm_setup_status').execute(
    {},
    { signal: signal() }
  )) as Record<string, unknown>
  assert.deepEqual(result, {
    setup_required: true,
    setup_path: '/setup',
    version: 'v1.2.3',
    system_name: 'LMM',
  })
  assert.doesNotMatch(JSON.stringify(result), /must-not-leak/)
})

test('an aborted signal stops a status read before it starts', async () => {
  let reads = 0
  api.get = (async () => {
    reads += 1
    return { data: { success: true, data: {} } }
  }) as typeof api.get
  const { tools } = buildTools()
  const controller = new AbortController()
  controller.abort()
  await assert.rejects(
    byName(tools, 'lmm_auth_methods').execute({}, { signal: controller.signal })
  )
  assert.equal(reads, 0)
})
