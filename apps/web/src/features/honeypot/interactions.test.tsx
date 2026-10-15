/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { AxiosError, type InternalAxiosRequestConfig } from 'axios'
import { Window } from 'happy-dom'

import type { HoneypotPolicies } from './contract'

const dom = new Window({
  url: 'https://console.example.test/system-settings/security/honeypot',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'HTMLSelectElement',
  'HTMLTextAreaElement',
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
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { HoneypotPolicyEditor } = await import('./policy-editor')
const { HoneypotBulkDialog, HoneypotBulkActionButton } =
  await import('./bulk-mark-dialog')
const { HoneypotUsersPanel } = await import('./users-panel')
const { DEFAULT_LIMITS } = await import('./contract')
const { hashHoneypotConfig } = await import('./contract')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const previousAuth = useAuthStore.getState().auth
const originalAdapter = api.defaults.adapter
const mounts: Array<() => void> = []
const scope = { userId: 901, sessionId: 'synthetic-interaction-session' }
const policies: HoneypotPolicies = {
  version: 1,
  revision: 4,
  config: {
    version: 1,
    policies: [
      {
        id: 'fixed',
        name: 'Fixed',
        mode: 'fixed_text',
        public_models: ['public'],
        fixed_text: 'Example',
        clients: {},
        limits: DEFAULT_LIMITS,
      },
    ],
  },
  capabilities: {
    client_protocols: ['openai_chat', 'openai_responses'],
    upstream_protocols: [
      'openai_chat',
      'openai_responses',
      'anthropic_messages',
      'gemini',
    ],
    modes: ['fixed_text', 'custom_upstream'],
    numeric_loopback_only: true,
    credential_references: ['host-alias'],
  },
}
const user = {
  user_id: '7',
  username: 'synthetic-user',
  email: 'synthetic@example.test',
  active: true,
  platform_role: 'user',
  revision: 0,
  marked: false,
  policy_id: null,
}
const response = (config: InternalAxiosRequestConfig, data: unknown) => ({
  status: 200,
  statusText: 'OK',
  headers: {},
  config,
  data,
})
function login(role = 100) {
  useAuthStore.getState().auth.setBundle({
    access_token: 'private-synthetic-unit-token',
    token_type: 'Bearer',
    access_expires_at: 2000000000,
    user: { id: 901, username: 'synthetic-root', role },
    session: {
      sid: scope.sessionId,
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'synthetic-unit',
      created_at: 1,
      last_active_at: 1,
      expires_at: 2000000000,
    },
  })
}
async function mount(element: React.ReactNode) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  })
  await act(async () =>
    root.render(
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={i18n}>{element}</I18nextProvider>
      </QueryClientProvider>
    )
  )
  mounts.push(() => {
    root.unmount()
    client.clear()
    container.remove()
  })
  return container
}
async function settle() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 20))
  })
}
function findButton(label: string) {
  const button = [...document.querySelectorAll('button')].find(
    (item) => item.textContent?.trim() === label
  )
  assert.ok(button, `button ${label}`)
  return button
}
async function click(label: string) {
  const button = [...document.querySelectorAll('button')].find(
    (item) => item.textContent?.trim() === label
  )
  assert.ok(button, `button ${label}`)
  assert.equal(button.disabled, false, `button ${label} enabled`)
  await act(async () => button.click())
  await settle()
}
async function edit(label: string, value: string) {
  const wrapper = [...document.querySelectorAll('label')].find((item) =>
    item.textContent?.startsWith(label)
  )
  const input = wrapper?.querySelector('input,textarea,select') as
    | HTMLInputElement
    | HTMLTextAreaElement
    | HTMLSelectElement
  assert.ok(input, `field ${label}`)
  await act(async () => {
    const prototype =
      input.tagName === 'TEXTAREA'
        ? HTMLTextAreaElement.prototype
        : input.tagName === 'SELECT'
          ? HTMLSelectElement.prototype
          : HTMLInputElement.prototype
    const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
    assert.ok(setter)
    setter.call(input, value)
    input.dispatchEvent(
      new Event(input.tagName === 'SELECT' ? 'change' : 'input', {
        bubbles: true,
      })
    )
  })
}
afterEach(async () => {
  await act(async () => {
    for (const cleanup of mounts.splice(0)) cleanup()
  })
  api.defaults.adapter = originalAdapter
  useAuthStore.setState({ auth: previousAuth })
})
after(() => dom.close())

test('configuration needs a visible review and reason before a CAS write', async () => {
  login()
  const calls: InternalAxiosRequestConfig[] = []
  api.defaults.adapter = async (config) => {
    calls.push(config)
    const body =
      typeof config.data === 'string' ? JSON.parse(config.data) : config.data
    return response(
      config,
      config.method === 'put'
        ? {
            version: 1,
            status: 'applied',
            operation_id: body.operation_id,
            kind: 'policies',
            policy_revision: 5,
            users: [],
            applied_config_hash: await hashHoneypotConfig(body.config),
          }
        : {
            ...policies,
            revision: calls.some((call) => call.method === 'put') ? 5 : 4,
            config: calls.some((call) => call.method === 'put')
              ? {
                  ...policies.config,
                  policies: [
                    { ...policies.config.policies[0], fixed_text: 'Changed' },
                  ],
                }
              : policies.config,
          }
    )
  }
  await mount(<HoneypotPolicyEditor initial={policies} scope={scope} />)
  await edit('Fixed text', 'Changed')
  await edit('Audit reason', 'Reviewed synthetic setting')
  assert.equal(calls.length, 0)
  await click('Review changes')
  assert.equal(calls.length, 0)
  assert.match(document.body.textContent ?? '', /Revision 4/)
  await click('Save changes')
  assert.equal(calls.filter((call) => call.method === 'put').length, 1)
  const write = calls.find((call) => call.method === 'put')
  assert.ok(write)
  const body = JSON.parse(write.data)
  assert.equal(body.expected_revision, 4)
  assert.equal(body.reason, 'Reviewed synthetic setting')
  assert.match(document.body.textContent ?? '', /Applied at revision 5/)
})
test('a settings conflict keeps the draft and never shows an applied receipt', async () => {
  login()
  let writes = 0
  api.defaults.adapter = async (config) => {
    writes++
    throw new AxiosError(
      'private database diagnostic',
      'ERR_BAD_REQUEST',
      config,
      undefined,
      {
        ...response(config, { error: { code: 'ADMIN_ABUSE_CONFLICT' } }),
        status: 409,
      }
    )
  }
  await mount(<HoneypotPolicyEditor initial={policies} scope={scope} />)
  await edit('Fixed text', 'Keep this draft')
  await edit('Audit reason', 'Reviewed')
  await click('Review changes')
  await click('Save changes')
  assert.equal(writes, 1)
  assert.match(document.body.textContent ?? '', /changed on the server/)
  assert.equal(
    (document.body.textContent ?? '').includes('Applied at revision'),
    false
  )
  assert.equal(
    (document.body.textContent ?? '').includes('private database diagnostic'),
    false
  )
  assert.equal(
    (document.querySelector('textarea') as HTMLTextAreaElement).value,
    'public'
  )
  assert.ok(
    [...document.querySelectorAll('textarea')].some(
      (input) => input.value === 'Keep this draft'
    )
  )
})
test('selected users are previewed exactly, and an unknown commit is recovered without replay', async () => {
  login()
  let commitCount = 0
  let previewCount = 0
  let operationID = ''
  let operationReads = 0
  api.defaults.adapter = async (config) => {
    const body =
      typeof config.data === 'string' ? JSON.parse(config.data) : config.data
    if (config.url?.endsWith('/policies')) return response(config, policies)
    if (config.url?.endsWith('/users/7')) {
      return response(config, { ...user, version: 2 })
    }
    if (config.url?.endsWith('/users/preview')) {
      previewCount++
      operationID = body.operation_id
      assert.deepEqual(body.targets, [{ user_id: '7', expected_revision: 0 }])
      return response(config, {
        version: 2,
        preview_id: 'synthetic-preview',
        preview_hash: 'a'.repeat(64),
        expires_at: 2000000000,
        operation_id: operationID,
        action: 'mark',
        policy_id: 'fixed',
        policy_revision: 4,
        targets: [
          {
            user_id: '7',
            username: user.username,
            active: true,
            expected_revision: 0,
            before: { marked: false, policy_id: null },
            after: { marked: true, policy_id: 'fixed', active: true },
          },
        ],
      })
    }
    if (config.url?.endsWith('/users/commit')) {
      commitCount++
      throw new AxiosError('timeout', 'ETIMEDOUT', config)
    }
    assert.equal(config.url, `/core/v2/admin/abuse/operations/${operationID}`)
    operationReads++
    return response(config, {
      version: 2,
      status: 'applied',
      operation_id: operationID,
      kind: 'users',
      policy_revision: 4,
      users: [{ ...user, revision: 1, marked: true, policy_id: 'fixed' }],
    })
  }
  await mount(
    <HoneypotBulkDialog ids={['7']} scope={scope} onClose={() => {}} />
  )
  await settle()
  await edit('Policy', 'fixed')
  await edit('Audit reason', 'Reviewed synthetic incident')
  await click('Review selected users')
  assert.equal(previewCount, 1)
  assert.equal(commitCount, 0)
  assert.match(document.body.textContent ?? '', /Preview only/)
  await click('Apply reviewed change')
  assert.equal(commitCount, 1)
  assert.match(document.body.textContent ?? '', /Save result is unknown/)
  await click('Check original operation')
  assert.equal(commitCount, 1)
  assert.equal(operationReads, 1)
  assert.match(document.body.textContent ?? '', /Applied at revision 4/)
})
test('disabled targets require the explicit conversion action before preview', async () => {
  login()
  let mutations = 0
  api.defaults.adapter = async (config) => {
    if (config.method !== 'get') mutations++
    return response(
      config,
      config.url?.endsWith('/policies')
        ? policies
        : { ...user, version: 2, active: false }
    )
  }
  await mount(
    <HoneypotBulkDialog ids={['7']} scope={scope} onClose={() => {}} />
  )
  await settle()
  await edit('Policy', 'fixed')
  await edit('Audit reason', 'Reviewed')
  let review = findButton('Review selected users')
  assert.equal(review.disabled, true)
  assert.equal(mutations, 0)
  await edit('Action', 'convert_disabled')
  review = findButton('Review selected users')
  assert.equal(review.disabled, false)
  assert.match(document.body.textContent ?? '', /reactivates disabled users/)
})
test('ordinary users and lower administrators never mount mark-management requests', async () => {
  let reads = 0
  api.defaults.adapter = async (config) => {
    reads++
    return response(config, policies)
  }
  login(10)
  await mount(<HoneypotBulkActionButton ids={['7']} />)
  await settle()
  assert.equal(document.body.textContent, '')
  assert.equal(reads, 0)
  await act(async () => login(1))
  await settle()
  assert.equal(document.body.textContent, '')
  assert.equal(reads, 0)
})

test('large-ID rows, selection and cursor pagination preserve every decimal digit', async () => {
  login()
  const first = '9007199254740992'
  const adjacent = '9007199254740993'
  const maximum = '9223372036854775807'
  const calls: InternalAxiosRequestConfig[] = []
  api.defaults.adapter = async (config) => {
    calls.push(config)
    assert.equal(config.url, '/core/v2/admin/abuse/users')
    const ids =
      config.params.after === undefined ? [first, adjacent] : [maximum]
    return response(config, {
      version: 2,
      users: ids.map((user_id) => ({ ...user, user_id })),
      next_after: ids.length === 2 ? adjacent : null,
    })
  }
  await mount(<HoneypotUsersPanel scope={scope} />)
  await settle()
  assert.match(document.body.textContent ?? '', new RegExp(`#${first}`))
  assert.match(document.body.textContent ?? '', new RegExp(`#${adjacent}`))
  const checkbox = document.querySelector(
    `[aria-label="Select user ${adjacent}"]`
  ) as HTMLElement
  assert.ok(checkbox)
  await act(async () => checkbox.click())
  await settle()
  assert.match(document.body.textContent ?? '', /Selected 1 users/)
  assert.equal(findButton('Manage honeypot marks').disabled, false)
  await click('Next page')
  assert.equal(calls.length, 2)
  assert.equal(Object.hasOwn(calls[0].params, 'after'), false)
  assert.equal(calls[1].params.after, adjacent)
  assert.match(document.body.textContent ?? '', new RegExp(`#${maximum}`))
  assert.match(document.body.textContent ?? '', /Selected 0 users/)
  await click('First page')
  assert.equal(Object.hasOwn(calls.at(-1)?.params ?? {}, 'after'), false)
})

test('a reviewed large-ID selection reconciles the same v2 operation without a second commit', async () => {
  login()
  const id = '9007199254740993'
  let operationID = ''
  let previewCount = 0
  let commitCount = 0
  let operationReads = 0
  api.defaults.adapter = async (config) => {
    const body =
      typeof config.data === 'string' ? JSON.parse(config.data) : config.data
    if (config.url?.endsWith('/policies')) return response(config, policies)
    if (config.url?.endsWith(`/users/${id}`)) {
      return response(config, { ...user, version: 2, user_id: id })
    }
    if (config.url?.endsWith('/users/preview')) {
      assert.equal(config.url, '/core/v2/admin/abuse/users/preview')
      assert.deepEqual(body.targets, [{ user_id: id, expected_revision: 0 }])
      operationID = body.operation_id
      previewCount++
      return response(config, {
        version: 2,
        preview_id: 'opaque-bigint-preview',
        preview_hash: 'b'.repeat(64),
        expires_at: 2000000000,
        operation_id: operationID,
        action: 'mark',
        policy_id: 'fixed',
        policy_revision: 4,
        targets: [
          {
            user_id: id,
            username: user.username,
            active: true,
            expected_revision: 0,
            before: { marked: false, policy_id: null },
            after: { marked: true, policy_id: 'fixed', active: true },
          },
        ],
      })
    }
    if (config.url?.endsWith('/users/commit')) {
      commitCount++
      assert.deepEqual(body, {
        operation_id: operationID,
        preview_id: 'opaque-bigint-preview',
        preview_hash: 'b'.repeat(64),
      })
      throw new AxiosError('synthetic timeout', 'ETIMEDOUT', config)
    }
    assert.equal(config.url, `/core/v2/admin/abuse/operations/${operationID}`)
    operationReads++
    return response(config, {
      version: 2,
      status: 'applied',
      operation_id: operationID,
      kind: 'users',
      policy_revision: 4,
      users: [
        { ...user, user_id: id, revision: 1, marked: true, policy_id: 'fixed' },
      ],
    })
  }
  await mount(
    <HoneypotBulkDialog ids={[id]} scope={scope} onClose={() => {}} />
  )
  await settle()
  await edit('Policy', 'fixed')
  await edit('Audit reason', 'Synthetic exact-ID review')
  await click('Review selected users')
  assert.match(document.body.textContent ?? '', new RegExp(`#${id}`))
  assert.equal(previewCount, 1)
  assert.equal(commitCount, 0)
  await click('Apply reviewed change')
  assert.match(document.body.textContent ?? '', /Save result is unknown/)
  await click('Check original operation')
  assert.equal(commitCount, 1)
  assert.equal(operationReads, 1)
  assert.match(document.body.textContent ?? '', /Applied at revision 4/)
})

test('a missing policy hash stays unknown through a not-yet-recorded operation and then resolves without another PUT', async () => {
  login()
  let writes = 0
  let operationReads = 0
  let operationID = ''
  let submitted = policies.config
  api.defaults.adapter = async (config) => {
    if (config.method === 'put') {
      writes++
      const body = JSON.parse(config.data)
      operationID = body.operation_id
      submitted = body.config
      return response(config, {
        version: 1,
        status: 'applied',
        operation_id: operationID,
        kind: 'policies',
        policy_revision: 5,
        users: [],
      })
    }
    if (config.url?.includes('/operations/')) {
      assert.equal(config.url, `/core/v1/admin/abuse/operations/${operationID}`)
      operationReads++
      if (operationReads === 1) {
        throw new AxiosError(
          'not recorded',
          'ERR_BAD_REQUEST',
          config,
          undefined,
          {
            ...response(config, {
              error: { code: 'ADMIN_ABUSE_OPERATION_NOT_FOUND' },
            }),
            status: 404,
          }
        )
      }
      return response(config, {
        version: 1,
        status: 'applied',
        operation_id: operationID,
        kind: 'policies',
        policy_revision: 5,
        users: [],
        applied_config_hash: await hashHoneypotConfig(submitted),
      })
    }
    return response(config, {
      ...policies,
      revision: writes ? 5 : 4,
      config: submitted,
    })
  }
  await mount(<HoneypotPolicyEditor initial={policies} scope={scope} />)
  await edit('Fixed text', 'Changed')
  await edit('Audit reason', 'Reviewed')
  await click('Review changes')
  await click('Save changes')
  assert.equal(writes, 1)
  assert.match(document.body.textContent ?? '', /Save result is unknown/)
  assert.equal(findButton('Save changes').disabled, true)
  await click('Check original operation')
  assert.match(document.body.textContent ?? '', /not recorded yet/)
  assert.equal(findButton('Save changes').disabled, true)
  await click('Check original operation')
  assert.equal(writes, 1)
  assert.equal(operationReads, 2)
  assert.match(document.body.textContent ?? '', /Applied at revision 5/)
})

test('complete HTTPS capabilities show alias origins and refuse a draft crossing the selected origin', async () => {
  login()
  let calls = 0
  api.defaults.adapter = async () => {
    calls++
    throw new Error('No mutation is allowed in this preview fixture')
  }
  const upstream = {
    base_url: 'https://example.test/v1',
    upstream_protocol: 'openai_chat' as const,
    model: 'public',
    credential_reference: 'sink',
  }
  await mount(
    <HoneypotPolicyEditor
      scope={scope}
      initial={{
        ...policies,
        config: {
          version: 1,
          policies: [
            {
              id: 'https',
              name: 'HTTPS',
              mode: 'custom_upstream',
              public_models: ['public'],
              clients: { openai_chat: upstream, openai_responses: upstream },
              limits: DEFAULT_LIMITS,
            },
          ],
        },
        capabilities: {
          ...policies.capabilities,
          numeric_loopback_only: false,
          external_https: true,
          local_http: false,
          credential_references: ['other', 'sink'],
          allowed_origins: {
            other: ['https://other.test'],
            sink: ['https://example.test'],
          },
        },
      }}
    />
  )
  await edit('Public models', 'public-new')
  await edit('Audit reason', 'Reviewed HTTPS')
  assert.equal(findButton('Review changes').disabled, false)
  await edit('Upstream URL', 'https://example.test.attacker.test/v1')
  assert.equal(findButton('Review changes').disabled, true)
  await edit('Upstream URL', 'https://EXAMPLE.TEST:443/custom/v1')
  assert.equal(findButton('Review changes').disabled, false)
  await edit('Credential alias', 'other')
  assert.equal(findButton('Review changes').disabled, true)
  assert.match(document.body.textContent ?? '', /https:\/\/other.test/)
  assert.equal(calls, 0)
})
test('an existing withdrawn upstream stays visible and can be repaired to fixed text', async () => {
  login()
  let calls = 0
  api.defaults.adapter = async () => {
    calls++
    throw new Error('No write should occur')
  }
  const upstream = {
    base_url: 'https://旧.example.test/中文@path',
    upstream_protocol: 'openai_chat' as const,
    model: 'public',
    credential_reference: 'removed',
  }
  await mount(
    <HoneypotPolicyEditor
      scope={scope}
      initial={{
        ...policies,
        config: {
          version: 1,
          policies: [
            {
              id: 'repair',
              name: 'Repair',
              mode: 'custom_upstream',
              public_models: ['public'],
              clients: { openai_chat: upstream, openai_responses: upstream },
              limits: DEFAULT_LIMITS,
            },
          ],
        },
        capabilities: {
          ...policies.capabilities,
          numeric_loopback_only: false,
          external_https: true,
          local_http: false,
          credential_references: ['sink'],
          allowed_origins: { sink: ['https://example.test'] },
        },
      }}
    />
  )
  assert.equal(
    (
      document.querySelector(
        'input[value="https://旧.example.test/中文@path"]'
      ) as HTMLInputElement
    )?.value,
    upstream.base_url
  )
  await edit('Audit reason', 'Repair withdrawn route')
  assert.equal(findButton('Review changes').disabled, true)
  await edit('Mode', 'fixed_text')
  assert.equal(findButton('Review changes').disabled, false)
  assert.equal(calls, 0)
})

for (const status of [403, 404]) {
  test(`generic post-dispatch ${status} retains the user preview and only reconciles the original operation`, async () => {
    login()
    const id = '9007199254740993'
    let operationID = ''
    let previews = 0
    let commits = 0
    let operationReads = 0
    api.defaults.adapter = async (config) => {
      const body =
        typeof config.data === 'string' ? JSON.parse(config.data) : config.data
      if (config.url?.endsWith('/policies')) return response(config, policies)
      if (config.url?.endsWith(`/users/${id}`)) {
        return response(config, { ...user, version: 2, user_id: id })
      }
      if (config.url?.endsWith('/users/preview')) {
        operationID = body.operation_id
        previews++
        return response(config, {
          version: 2,
          preview_id: 'unknown-preview',
          preview_hash: 'c'.repeat(64),
          expires_at: 2000000000,
          operation_id: operationID,
          action: 'mark',
          policy_id: 'fixed',
          policy_revision: 4,
          targets: [
            {
              user_id: id,
              username: user.username,
              active: true,
              expected_revision: 0,
              before: { marked: false, policy_id: null },
              after: { marked: true, policy_id: 'fixed', active: true },
            },
          ],
        })
      }
      if (config.url?.endsWith('/users/commit')) {
        commits++
        throw new AxiosError(
          'synthetic proxy response',
          'ERR_BAD_RESPONSE',
          config,
          undefined,
          {
            ...response(config, {
              error: { code: 'SYNTHETIC_PROXY_RESPONSE' },
            }),
            status,
          }
        )
      }
      assert.equal(config.url, `/core/v2/admin/abuse/operations/${operationID}`)
      operationReads++
      if (operationReads <= 3) {
        throw new AxiosError(
          'synthetic lookup unavailable',
          'ERR_BAD_RESPONSE',
          config,
          undefined,
          {
            ...response(config, {}),
            status: [403, 404, 503][operationReads - 1],
          }
        )
      }
      return response(config, {
        version: 2,
        status: 'applied',
        operation_id: operationID,
        kind: 'users',
        policy_revision: 4,
        users: [
          {
            ...user,
            user_id: id,
            revision: 1,
            marked: true,
            policy_id: 'fixed',
          },
        ],
      })
    }
    await mount(
      <HoneypotBulkDialog ids={[id]} scope={scope} onClose={() => {}} />
    )
    await settle()
    await edit('Policy', 'fixed')
    await edit('Audit reason', 'Synthetic ambiguous response')
    await click('Review selected users')
    await click('Apply reviewed change')
    assert.equal(
      [...document.querySelectorAll('button')].some(
        (button) => button.textContent?.trim() === 'Review selected users'
      ),
      false,
      'unknown result must not discard preview and offer a replacement'
    )
    assert.match(document.body.textContent ?? '', /Save result is unknown/)
    assert.match(document.body.textContent ?? '', new RegExp(operationID))
    assert.equal(
      (document.body.textContent ?? '').includes(
        'Preview only; no account has changed.'
      ),
      false
    )
    assert.equal(findButton('Apply reviewed change').disabled, true)
    for (let i = 0; i < 3; i++) {
      await click('Check original operation')
      assert.match(document.body.textContent ?? '', /Save result is unknown/)
      assert.equal(findButton('Apply reviewed change').disabled, true)
      assert.equal(previews, 1)
      assert.equal(commits, 1)
      assert.match(document.body.textContent ?? '', new RegExp(operationID))
    }
    await click('Check original operation')
    assert.equal(operationReads, 4)
    assert.equal(previews, 1)
    assert.equal(commits, 1)
    assert.match(document.body.textContent ?? '', /Applied at revision 4/)
  })

  test(`generic post-dispatch ${status} retains the policy draft operation and never offers another PUT`, async () => {
    login()
    let operationID = ''
    let writes = 0
    let reads = 0
    let committedHash = ''
    api.defaults.adapter = async (config) => {
      const body =
        typeof config.data === 'string' ? JSON.parse(config.data) : config.data
      if (config.method === 'put') {
        writes++
        operationID = body.operation_id
        committedHash = await hashHoneypotConfig(body.config)
        throw new AxiosError(
          'synthetic proxy response',
          'ERR_BAD_RESPONSE',
          config,
          undefined,
          { ...response(config, {}), status }
        )
      }
      if (config.url?.includes('/operations/')) {
        assert.equal(
          config.url,
          `/core/v1/admin/abuse/operations/${operationID}`
        )
        reads++
        if (reads === 1) {
          throw new AxiosError(
            'synthetic lookup unavailable',
            'ERR_BAD_RESPONSE',
            config,
            undefined,
            { ...response(config, {}), status: 404 }
          )
        }
        return response(config, {
          version: 1,
          status: 'applied',
          operation_id: operationID,
          kind: 'policies',
          policy_revision: 5,
          users: [],
          applied_config_hash: committedHash,
        })
      }
      return response(config, policies)
    }
    await mount(<HoneypotPolicyEditor initial={policies} scope={scope} />)
    await edit('Fixed text', 'Synthetic reviewed change')
    await edit('Audit reason', 'Synthetic ambiguous response')
    await click('Review changes')
    await click('Save changes')
    assert.match(document.body.textContent ?? '', /Save result is unknown/)
    assert.match(document.body.textContent ?? '', new RegExp(operationID))
    assert.equal(findButton('Save changes').disabled, true)
    await click('Check original operation')
    assert.equal(findButton('Save changes').disabled, true)
    assert.equal(writes, 1)
    await click('Check original operation')
    assert.equal(writes, 1)
    assert.equal(reads, 2)
    assert.match(document.body.textContent ?? '', /Applied at revision 5/)
  })
}
