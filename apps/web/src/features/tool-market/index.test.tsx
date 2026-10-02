/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

import type {
  Grant,
  Installation,
  MarketConfig,
  MarketDetail,
  MarketService,
  MarketSummary,
  MarketTool,
} from './api'

const dom = new Window({ url: 'https://console.example.test/tool-market' })
Object.defineProperty(dom.document, 'compatMode', { value: 'CSS1Compat' })
const originalGlobals = new Map<string, PropertyDescriptor | undefined>()
for (const key of [
  'window',
  'document',
  'navigator',
  'localStorage',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'HTMLTextAreaElement',
  'HTMLSelectElement',
  'SVGElement',
  'customElements',
  'Node',
  'Element',
  'DocumentFragment',
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
  originalGlobals.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.defineProperty(dom.HTMLElement.prototype, 'scrollIntoView', {
  configurable: true,
  value() {},
})
Object.defineProperty(dom, 'matchMedia', {
  configurable: true,
  value: globalThis.matchMedia,
})
originalGlobals.set(
  'IS_REACT_ACT_ENVIRONMENT',
  Object.getOwnPropertyDescriptor(globalThis, 'IS_REACT_ACT_ENVIRONMENT')
)
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { useAuthStore } = await import('@/stores/auth-store')
const { api } = await import('@/lib/api')
const { marketAPI } = await import('./api')
const { ToolMarket } = await import('./index')
const originalAPI = { ...marketAPI }
const originalAdapter = api.defaults.adapter
const originalAuth = useAuthStore.getState().auth
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const views: {
  root: ReturnType<typeof createRoot>
  client: InstanceType<typeof QueryClient>
  container: HTMLElement
}[] = []
const flush = () => new Promise<void>((resolve) => setTimeout(resolve, 15))

const pausedConfig: MarketConfig = {
  enabled: false,
  builtin_enabled: true,
  fee_bps: 0,
  recipient_id: 0,
  quota_per_unit: 500000,
  web_client_id: 'web-market',
  mcp_path: '/mcp/market',
}
function detail(
  id: string,
  executionType: string,
  price = 0,
  visibility = 'private'
): MarketDetail {
  const versionID = `${id}-version-exact`
  return {
    service: {
      id,
      name: id,
      owner_id: 2,
      live_version_id: versionID,
      draft_version_id: versionID,
      status: 'published',
      created_at: 1,
    },
    version: {
      id: versionID,
      name: id,
      description: `${id} tools`,
      execution_type: executionType,
      endpoint:
        executionType === 'builtin' ? '' : 'https://provider.example.test/mcp',
      visibility,
      status: 'published',
      review_note: '',
    },
    tools: [
      {
        tool_id: `${id}-tool`,
        version_id: versionID,
        name: `${id} search`,
        description: 'Search records',
        input_schema: '{"type":"object","properties":{}}',
        output_schema: '',
        permissions: '["read"]',
        price_quota: price,
      },
    ],
    pricing: '',
    validated: false,
  }
}
const builtin = detail('Platform catalog', 'builtin')
const remote = detail('Remote catalog', 'remote', 125000)
function summary(item: MarketDetail): MarketSummary {
  return {
    id: item.service.id,
    owner_id: item.service.owner_id,
    version_id: item.version.id,
    name: item.version.name,
    description: item.version.description,
    execution_type: item.version.execution_type,
    tool_count: item.tools.length,
    min_price_quota: item.tools[0].price_quota,
    max_price_quota: item.tools[0].price_quota,
  }
}
function grant(tool: MarketTool): Grant {
  return {
    id: `${tool.tool_id}-grant`,
    client_id: 'web-market',
    tool_id: tool.tool_id,
    version_id: tool.version_id,
    max_price_quota: tool.price_quota,
    max_total_quota: tool.price_quota * 4,
    max_calls: 4,
    reserved_quota: 0,
    spent_quota: 0,
    successful_calls: 0,
    reserved_calls: 0,
    expires_at: Math.floor(Date.now() / 1000) + 3600,
    revoked_at: 0,
  }
}

function findButton(text: string, root: ParentNode = document.body) {
  return [...root.querySelectorAll<HTMLButtonElement>('button')].find(
    (button) => button.textContent === text
  )
}
function button(text: string, root: ParentNode = document.body) {
  const result = findButton(text, root)
  assert.ok(result, `Missing button: ${text}`)
  return result
}
async function waitFor(predicate: () => boolean) {
  for (let attempt = 0; attempt < 80; attempt++) {
    if (predicate()) return
    await act(flush)
  }
  assert.fail('Expected tool market UI did not become ready')
}
async function click(element: HTMLElement) {
  await act(async () => {
    element.click()
    await flush()
  })
}
async function mount(role = 1, id = 2) {
  useAuthStore.getState().auth.setUser({ id, username: 'market-test', role })
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Infinity },
      mutations: { retry: false },
    },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  views.push({ root, client, container })
  await act(async () => {
    root.render(
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={i18n}>
          <ToolMarket />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flush()
  })
  return { container, client }
}
function stubNavigation(items: MarketDetail[], services: MarketService[] = []) {
  marketAPI.config = async () => pausedConfig
  marketAPI.list = async () => items.map(summary)
  marketAPI.mine = (async (kind: string) => {
    if (kind === 'services') return services
    if (kind === 'installations') {
      return items.map((item) => ({
        client_id: 'web-market',
        tool_id: item.tools[0].tool_id,
        version_id: item.version.id,
      }))
    }
    if (kind === 'grants') return items.map((item) => grant(item.tools[0]))
    return []
  }) as typeof marketAPI.mine
  marketAPI.detail = async (id) => {
    const item = items.find((row) => row.service.id === id)
    assert.ok(item)
    return item
  }
  marketAPI.reviews = async () => []
  marketAPI.calls = async () => []
  marketAPI.income = async () => []
  marketAPI.grant = async () =>
    assert.fail('Navigation must not authorize payment')
  marketAPI.install = async () =>
    assert.fail('Navigation must not change loaded tools')
  marketAPI.submit = async () =>
    assert.fail('Navigation must not submit a review')
  marketAPI.review = async () =>
    assert.fail('Navigation must not approve a tool')
}

afterEach(async () => {
  for (const view of views.splice(0)) {
    await act(async () => view.root.unmount())
    view.client.clear()
    view.container.remove()
  }
  Object.assign(marketAPI, originalAPI)
  api.defaults.adapter = originalAdapter
  useAuthStore.setState({ auth: originalAuth })
})
after(() => {
  dom.close()
  for (const [key, descriptor] of originalGlobals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
})

test('a paused remote market keeps builtin tools runnable and invokes only after explicit run', async () => {
  stubNavigation([builtin, remote])
  const calls: Parameters<typeof marketAPI.invoke>[0][] = []
  let settingsWrites = 0
  marketAPI.configure = async () => {
    settingsWrites++
    return null
  }
  marketAPI.invoke = async (input) => {
    calls.push(input)
    return {
      call: {
        id: 'builtin-call',
        service_id: builtin.service.id,
        tool_id: input.tool_id,
        version_id: input.version_id,
        client_id: 'web-market',
        execution_status: 'succeeded',
        settlement_status: 'settled',
        price_quota: 0,
        created_at: 1,
        resolve_by: 1,
      },
      result: { content: [{ type: 'text', text: 'Found one record.' }] },
      result_expired: false,
    }
  }
  const { container } = await mount()
  await waitFor(
    () => container.textContent?.includes(remote.version.name) === true
  )
  assert.equal(calls.length, 0)
  assert.equal(settingsWrites, 0)
  assert.equal(findButton('Configure market', container), undefined)
  const remoteCard = [...container.querySelectorAll('button')].find(
    (row) => row.querySelector('strong')?.textContent === remote.version.name
  )
  assert.ok(remoteCard)
  await click(remoteCard)
  await waitFor(() => Boolean(findButton('Run tool', container)))
  assert.equal(button('Run tool', container).disabled, true)
  await click(button('Back to list', container))
  const builtinCard = [...container.querySelectorAll('button')].find(
    (row) => row.querySelector('strong')?.textContent === builtin.version.name
  )
  assert.ok(builtinCard)
  await click(builtinCard)
  await waitFor(() => Boolean(findButton('Run free tool', container)))
  assert.equal(button('Run free tool', container).disabled, false)
  assert.equal(calls.length, 0)
  await click(button('Run free tool', container))
  await waitFor(() => document.body.querySelector('[role="dialog"]') !== null)
  assert.equal(calls.length, 0, 'opening the call form does not execute a tool')
  const dialog = document.body.querySelector('[role="dialog"]')
  assert.ok(dialog)
  await click(button('Run free tool', dialog))
  await waitFor(() => calls.length === 1)
  assert.equal(calls[0].tool_id, builtin.tools[0].tool_id)
  assert.equal(calls[0].version_id, builtin.version.id)
  assert.equal(calls[0].grant_id, grant(builtin.tools[0]).id)
  assert.deepEqual(calls[0].arguments, {})
  assert.equal(settingsWrites, 0)
})

test('the root pause CTA opens settings with a recipient default without enabling or saving', async () => {
  stubNavigation([])
  const settings: Parameters<typeof marketAPI.configure>[0][] = []
  marketAPI.configure = async (input) => {
    settings.push(input)
    return null
  }
  marketAPI.invoke = async () =>
    assert.fail('Opening settings must not execute a tool')
  const { container } = await mount(100, 42)
  await waitFor(() => Boolean(findButton('Configure market', container)))
  assert.equal(settings.length, 0)
  await click(button('Configure market', container))
  await waitFor(() => container.querySelector('#market-enabled') !== null)
  assert.equal(
    container.querySelector<HTMLSelectElement>('#market-enabled')?.value,
    'paused'
  )
  assert.equal(
    container.querySelector<HTMLInputElement>('#market-recipient')?.value,
    '42'
  )
  assert.equal(
    settings.length,
    0,
    'opening the settings panel must not enable the market'
  )
  await click(button('Confirm market settings', container))
  await waitFor(() => settings.length === 1)
  assert.deepEqual(settings, [{ enabled: false, fee_bps: 0, recipient_id: 42 }])
})

test('private activation uses the exact draft API and stays unavailable for public or paid drafts', async () => {
  const free = detail('Free private', 'remote')
  free.version.status = 'draft'
  free.service.status = 'draft'
  free.service.live_version_id = ''
  const publicFree = detail('Public free', 'remote', 0, 'public')
  publicFree.version.status = 'draft'
  const paidPrivate = detail('Paid private', 'remote', 125000)
  paidPrivate.version.status = 'draft'
  stubNavigation([free, publicFree, paidPrivate], [free.service])
  const mutations: {
    method: string | undefined
    url: string
    input: unknown
  }[] = []
  api.defaults.adapter = async (config) => {
    const url = config.url ?? ''
    assert.ok(
      url.endsWith('/validate') || url.endsWith('/activate'),
      'only explicit draft validation/activation API is allowed'
    )
    mutations.push({
      method: config.method,
      url,
      input:
        typeof config.data === 'string' ? JSON.parse(config.data) : config.data,
    })
    if (url.endsWith('/activate')) free.version.status = 'published'
    return {
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: { success: true, data: null },
    }
  }
  const { container, client } = await mount()
  await click(button('My publications', container))
  await waitFor(() => Boolean(findButton('Open draft', container)))
  await click(button('Open draft', container))
  await waitFor(() =>
    Boolean(findButton('Validate and enable only for me', container))
  )
  assert.equal(mutations.length, 0)
  await click(button('Validate connection', container))
  await waitFor(() => mutations.length === 1)
  await click(button('Validate and enable only for me', container))
  await waitFor(() => mutations.length === 2)
  assert.deepEqual(
    mutations.map(({ method, url, input }) => ({
      method,
      url,
      ...(input === undefined ? {} : { input }),
    })),
    [
      {
        method: 'post',
        url: `/api/tool-market/services/${free.service.id}/validate`,
      },
      {
        method: 'post',
        url: `/api/tool-market/services/${free.service.id}/activate`,
        input: { version_id: free.version.id },
      },
    ]
  )
  assert.equal(findButton('Approve and publish', container), undefined)
  await click(button('Back to list', container))
  for (const candidate of [publicFree, paidPrivate]) {
    client.setQueryData(['tool-market', 2, 'services'], [candidate.service])
    await act(flush)
    await click(button('Open draft', container))
    await waitFor(
      () =>
        container.querySelector('h3')?.textContent === candidate.version.name
    )
    assert.equal(
      findButton('Validate and enable only for me', container),
      undefined
    )
    assert.equal(
      mutations.length,
      2,
      'browsing an ineligible draft does not publish it'
    )
    await click(button('Back to list', container))
  }
})

test('editing a published service opens the existing authored draft without replacing it with live settings', async () => {
  const published = detail('Authored service', 'remote', 125000, 'public')
  published.version.name = 'Published configuration'
  published.service.draft_version_id = 'unsubmitted-version-exact'
  const draft = structuredClone(published)
  draft.version = {
    ...draft.version,
    id: 'unsubmitted-version-exact',
    name: 'Unsubmitted draft name',
    description: 'Owner changes that must be kept.',
    endpoint: 'https://draft-provider.example.test/mcp',
    visibility: 'shared',
    status: 'draft',
  }
  draft.tools = [
    {
      ...draft.tools[0],
      version_id: draft.version.id,
      permissions: '["read","files"]',
      price_quota: 375000,
    },
  ]
  draft.allowed_users = [7, 11]
  stubNavigation([published])
  const reads: { id: string; mode: string }[] = []
  const credentialReads: { id: string; versionID: string }[] = []
  marketAPI.detail = async (id, mode = 'published') => {
    reads.push({ id, mode })
    return mode === 'draft' ? draft : published
  }
  marketAPI.credentials = async (id, versionID) => {
    credentialReads.push({ id, versionID })
    return { mode: 'none', configured: false, updated_at: 0 }
  }
  marketAPI.save = async () =>
    assert.fail('Opening the editor must not save a draft')
  marketAPI.setCredentials = async () =>
    assert.fail('Opening the editor must not change credentials')
  marketAPI.inspect = async () =>
    assert.fail('Opening the editor must not contact a provider')
  const { container } = await mount()
  await waitFor(
    () => container.textContent?.includes(published.version.name) === true
  )
  const card = [...container.querySelectorAll('button')].find(
    (row) => row.querySelector('strong')?.textContent === published.version.name
  )
  assert.ok(card)
  await click(card)
  await waitFor(() => Boolean(findButton('Edit draft', container)))
  await click(button('Edit draft', container))
  await waitFor(
    () =>
      container.querySelector<HTMLInputElement>('#market-name')?.value ===
        draft.version.name &&
      credentialReads.length >= 1 &&
      container.querySelector<HTMLSelectElement>('#market-authentication')
        ?.disabled === false
  )
  await act(flush)
  assert.deepEqual(reads.slice(0, 2), [
    { id: published.service.id, mode: 'published' },
    { id: published.service.id, mode: 'draft' },
  ])
  assert.equal(reads.filter((row) => row.mode === 'draft').length, 1)
  assert.deepEqual(credentialReads, [
    { id: published.service.id, versionID: draft.version.id },
  ])
  assert.equal(
    container.querySelector<HTMLTextAreaElement>('#market-description')?.value,
    draft.version.description
  )
  assert.equal(
    container.querySelector<HTMLInputElement>('#market-endpoint')?.value,
    draft.version.endpoint
  )
  const price = [...container.querySelectorAll<HTMLInputElement>('input')].find(
    (input) => input.id === `price-${draft.tools[0].name}`
  )
  assert.equal(price?.value, '0.75')
  assert.equal(
    container.querySelector<HTMLSelectElement>('#market-visibility')?.value,
    'shared'
  )
  assert.equal(
    container.querySelector<HTMLInputElement>('#market-shared')?.value,
    '7, 11'
  )
  const files = [...container.querySelectorAll('label')].find(
    (label) => label.textContent?.trim() === 'Files'
  )
  assert.ok(files)
  assert.equal(
    files.querySelector<HTMLInputElement>('input[type="checkbox"]')?.checked,
    true
  )
  assert.equal(
    button('Save draft', container).disabled,
    true,
    'saving still requires an explicit definition read'
  )
})

for (const clientID of ['oauth:lmm-pi', 'oauth:lmm-dsh']) {
  test(`first-time ${clientID} can authorize and load its first exact tool without a personal token`, async () => {
    stubNavigation([builtin])
    const authorizations: Parameters<typeof marketAPI.grant>[0][] = []
    const installations: Installation[] = []
    const savedGrants: Grant[] = []
    marketAPI.mine = (async (kind: string) => {
      if (kind === 'oauth-clients') return [{ client_id: clientID }]
      if (kind === 'installations') return installations
      if (kind === 'grants') return savedGrants
      return []
    }) as typeof marketAPI.mine
    marketAPI.token = async () =>
      assert.fail('OAuth authorization must not issue a personal token')
    marketAPI.invoke = async () =>
      assert.fail('Selecting or authorizing a client must not invoke a tool')
    marketAPI.grant = async (input) => {
      authorizations.push(input)
      const row = { ...grant(builtin.tools[0]), ...input }
      savedGrants.push(row)
      return row
    }
    marketAPI.install = async (input, loaded) => {
      assert.equal(loaded, true)
      installations.push(input)
      return null
    }
    const { container } = await mount()
    await waitFor(() =>
      Boolean(container.querySelector(`option[value="${clientID}"]`))
    )
    const card = [...container.querySelectorAll('button')].find(
      (row) => row.querySelector('strong')?.textContent === builtin.version.name
    )
    assert.ok(card)
    await click(card)
    await waitFor(() =>
      Boolean(container.querySelector('#market-active-client'))
    )
    const picker = container.querySelector<HTMLSelectElement>(
      '#market-active-client'
    )
    assert.ok(picker)
    await act(async () => {
      picker.value = clientID
      picker.dispatchEvent(new Event('change', { bubbles: true }))
      await flush()
    })
    assert.equal(button('Load', container).disabled, false)
    assert.equal(authorizations.length, 0)
    assert.equal(installations.length, 0)
    assert.equal(findButton('Run free tool', container), undefined)
    await click(button('Add and authorize tool', container))
    await waitFor(() => document.body.querySelector('[role="dialog"]') !== null)
    const dialog = document.body.querySelector('[role="dialog"]')
    assert.ok(dialog)
    assert.ok(dialog.textContent?.includes(clientID))
    assert.equal(
      authorizations.length,
      0,
      'opening limits does not grant access'
    )
    await click(button('Add and authorize tool', dialog))
    await waitFor(() => installations.length === 1 && savedGrants.length === 1)
    assert.equal(authorizations[0].client_id, clientID)
    assert.equal(authorizations[0].tool_id, builtin.tools[0].tool_id)
    assert.equal(authorizations[0].version_id, builtin.version.id)
    assert.deepEqual(installations[0], {
      client_id: clientID,
      tool_id: builtin.tools[0].tool_id,
      version_id: builtin.version.id,
    })
    await waitFor(
      () => container.textContent?.includes('Ready in this client.') === true
    )
  })
}

test('historical OAuth rows cannot impersonate eligible clients and revocation resets the selected target', async () => {
  stubNavigation([builtin])
  let eligible = [{ client_id: 'oauth:lmm-pi' }]
  marketAPI.mine = (async (kind: string) => {
    if (kind === 'oauth-clients') return eligible
    if (kind === 'grants') {
      return ['oauth:pretend-pi', 'oauth:lmm-dsh'].map((client_id) => ({
        ...grant(builtin.tools[0]),
        client_id,
      }))
    }
    if (kind === 'tokens') return [{ client_id: 'oauth:pretend-token' }]
    if (kind === 'installations') {
      return [{ client_id: 'oauth:pretend-install' }]
    }
    return []
  }) as typeof marketAPI.mine
  const { container, client: cache } = await mount()
  await waitFor(() =>
    Boolean(container.querySelector('option[value="oauth:lmm-pi"]'))
  )
  assert.deepEqual(
    [...container.querySelectorAll<HTMLOptionElement>('option')].map(
      (row) => row.value
    ),
    ['web-market', 'oauth:lmm-pi']
  )
  const picker = container.querySelector<HTMLSelectElement>(
    '#market-catalog-client'
  )
  assert.ok(picker)
  await act(async () => {
    picker.value = 'oauth:lmm-pi'
    picker.dispatchEvent(new Event('change', { bubbles: true }))
    await flush()
  })
  eligible = []
  await act(async () => {
    await cache.invalidateQueries({
      queryKey: ['tool-market', 2, 'oauth-clients'],
    })
    await flush()
  })
  await waitFor(() => picker.value === 'web-market')
  assert.equal(container.querySelector('option[value="oauth:lmm-pi"]'), null)
})

test('OAuth authorization targets are discarded when the signed-in account changes', async () => {
  stubNavigation([builtin])
  marketAPI.mine = (async (kind: string) => {
    if (kind === 'oauth-clients') {
      return useAuthStore.getState().auth.user?.id === 2
        ? [{ client_id: 'oauth:lmm-pi' }]
        : [{ client_id: 'oauth:lmm-dsh' }]
    }
    return []
  }) as typeof marketAPI.mine
  const { container } = await mount()
  await waitFor(() =>
    Boolean(container.querySelector('option[value="oauth:lmm-pi"]'))
  )
  await act(async () => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 3, username: 'other-user', role: 1 })
    await flush()
  })
  await waitFor(() =>
    Boolean(container.querySelector('option[value="oauth:lmm-dsh"]'))
  )
  assert.equal(container.querySelector('option[value="oauth:lmm-pi"]'), null)
  assert.equal(
    container.querySelector<HTMLSelectElement>('#market-catalog-client')?.value,
    'web-market'
  )
})
