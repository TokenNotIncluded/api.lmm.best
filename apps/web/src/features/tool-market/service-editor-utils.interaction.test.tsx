/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({
  url: 'https://console.example.test/tool-market',
})
domWindow.document.write(
  '<!doctype html><html><head></head><body></body></html>'
)
const originalGlobals = new Map<string, PropertyDescriptor | undefined>()
for (const key of [
  'window',
  'document',
  'navigator',
  'localStorage',
  'HTMLElement',
  'HTMLInputElement',
  'SVGElement',
  'customElements',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'PointerEvent',
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
    value: domWindow[key],
  })
}
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
const { api } = await import('@/lib/api')
const { resetMarketCurrencyTest, useWalletCurrencyPreferenceStore } =
  await import('./currency-test-support')
const { ServiceEditor } = await import('./service-editor')
type MarketDetail = import('./api').MarketDetail
type DraftInput = import('./api').DraftInput
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })

after(() => {
  domWindow.close()
  for (const [key, descriptor] of originalGlobals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
})

function changeNativeInput(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setter)
  setter.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function submitForm(container: HTMLElement) {
  const form = container.querySelector('form')
  assert.ok(form)
  form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
}

async function renderEditor(initial?: MarketDetail, feeBps = 1000) {
  resetMarketCurrencyTest()
  useWalletCurrencyPreferenceStore.getState().setPreference('CNY')
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const saved: string[] = []
  let displayedInitial = initial
  let displayedFeeBps = feeBps
  const render = async () => {
    await act(async () =>
      root.render(
        <QueryClientProvider client={client}>
          <I18nextProvider i18n={i18n}>
            <ServiceEditor
              initial={displayedInitial}
              units={500000}
              feeBps={displayedFeeBps}
              onSaved={(id) => saved.push(id)}
              onCancel={() => {}}
            />
          </I18nextProvider>
        </QueryClientProvider>
      )
    )
  }
  await render()
  const settle = async () => {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
  }
  await settle()
  const button = (name: string) => {
    const result = [...container.querySelectorAll('button')].find(
      (item) => item.textContent === name
    )
    assert.ok(result, name)
    return result
  }
  return {
    container,
    client,
    saved,
    button,
    settle,
    input: async (id: string, value: string) => {
      const input = container.querySelector<HTMLInputElement>(id)
      assert.ok(input, id)
      await act(async () => changeNativeInput(input, value))
    },
    select: async (id: string, value: string) => {
      const select = container.querySelector<HTMLSelectElement>(id)
      assert.ok(select, id)
      await act(async () => {
        select.value = value
        select.dispatchEvent(new Event('change', { bubbles: true }))
      })
    },
    submit: async () => {
      await act(async () => submitForm(container))
      await settle()
    },
    rerenderInitial: async (next: MarketDetail) => {
      displayedInitial = next
      await render()
      await settle()
    },
    rerenderFee: async (next: number) => {
      displayedFeeBps = next
      await render()
      await settle()
    },
    click: async (name: string) => {
      await act(async () => {
        if (name === 'Save draft') {
          assert.equal(button(name).disabled, false)
          // Happy DOM's floating-point step validation rejects valid decimal
          // prices. Exercise the submit handler after checking the UI gate.
          submitForm(container)
        } else button(name).click()
      })
      await settle()
    },
    dispose: async () => {
      await act(async () => root.unmount())
      client.clear()
      container.remove()
    },
  }
}

const initial: MarketDetail = {
  service: {
    id: 'service-existing',
    owner_id: 1,
    live_version_id: 'version-old',
    draft_version_id: 'version-old',
    status: 'approved',
    created_at: 1,
  },
  version: {
    id: 'version-old',
    name: 'Search service',
    description: 'Search',
    endpoint: 'https://tools.example.test/mcp',
    execution_type: 'remote',
    visibility: 'private',
    status: 'draft',
    review_note: '',
  },
  tools: [
    {
      tool_id: 'search-id',
      version_id: 'version-old',
      name: 'search',
      description: 'Search',
      input_schema: '{"type":"object"}',
      output_schema: '',
      permissions: '["read","network"]',
      price_quota: 25000,
    },
  ],
  pricing: '',
  validated: false,
}
const discovered = [
  {
    name: 'search',
    description: 'Search',
    input_schema: { type: 'object', required: ['query'] },
    permissions: ['read'],
    price_quota: 0,
  },
  {
    name: 'new_tool',
    description: 'New tool',
    input_schema: { type: 'object' },
    permissions: ['send'],
    price_quota: 0,
  },
]

function pricingRequests(definitions = discovered) {
  const originalAdapter = api.defaults.adapter
  const drafts: DraftInput[] = []
  api.defaults.adapter = async (config) => {
    const url = config.url ?? ''
    const input =
      typeof config.data === 'string' ? JSON.parse(config.data) : config.data
    let data: unknown
    if (url.endsWith('/inspect')) {
      data = definitions
    } else if (
      (config.method === 'post' && url.endsWith('/services')) ||
      (config.method === 'put' && url.endsWith('/draft'))
    ) {
      drafts.push(input)
      data = {
        ...initial.service,
        id: 'service-pricing',
        draft_version_id: 'version-priced',
      }
    } else if (url.endsWith('/credentials')) {
      data = { mode: 'none', configured: false, updated_at: 0 }
    } else assert.fail(`Unexpected request: ${config.method} ${url}`)
    return {
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: { success: true, data },
    }
  }
  return {
    drafts,
    restore: () => {
      api.defaults.adapter = originalAdapter
    },
  }
}

async function readNewService(view: Awaited<ReturnType<typeof renderEditor>>) {
  await view.input('#market-name', 'Priced tool service')
  await view.input('#market-endpoint', 'https://tools.example.test/mcp')
  await view.click('Read tool definitions')
}

test('a new service shows pricing guidance before discovery and cannot save undiscovered tools', async () => {
  const originalAdapter = api.defaults.adapter
  const requests: string[] = []
  api.defaults.adapter = async (config) => {
    requests.push(config.url ?? '')
    assert.fail('A service without inspected tools must not write a draft')
  }
  const view = await renderEditor()
  try {
    assert.ok(
      [...view.container.querySelectorAll('legend')].some(
        (legend) => legend.textContent === 'Tools and prices'
      ),
      'pricing is visible as soon as the publisher opens the form'
    )
    assert.match(
      view.container.textContent ?? '',
      /Read the MCP tool definitions first to choose free or paid pricing for each tool/
    )
    assert.equal(
      view.container.querySelectorAll('input[id^="select-"]').length,
      0
    )
    await view.input('#market-name', 'Uninspected service')
    await view.input('#market-endpoint', 'https://tools.example.test/mcp')
    assert.equal(view.button('Save draft').disabled, true)
    await view.submit()
    assert.deepEqual(requests, [])
    assert.deepEqual(view.saved, [])
  } finally {
    await view.dispose()
    api.defaults.adapter = originalAdapter
  }
})

test('each discovered tool can be priced independently and switching back to free saves zero', async () => {
  const requests = pricingRequests()
  const view = await renderEditor()
  try {
    await readNewService(view)
    for (const name of ['search', 'new_tool']) {
      const mode = view.container.querySelector<HTMLSelectElement>(
        `#billing-mode-${name}`
      )
      assert.ok(mode)
      assert.equal(mode.value, 'free')
      assert.deepEqual(
        [...mode.options].map((option) => option.textContent),
        [
          'Free tool',
          'Paid tool',
          'Input token usage',
          'Combined usage pricing',
        ]
      )
    }
    await view.select('#billing-mode-search', 'paid')
    assert.equal(view.button('Save draft').disabled, true)
    await view.input('#price-search', '1')
    await view.select('#billing-mode-new_tool', 'paid')
    await view.input('#price-new_tool', '0.000002')
    await view.click('Save draft')
    assert.equal(requests.drafts.length, 1)
    assert.deepEqual(
      requests.drafts[0].tools.map(({ name, price_quota }) => ({
        name,
        price_quota,
      })),
      [
        { name: 'search', price_quota: 500000 },
        { name: 'new_tool', price_quota: 1 },
      ]
    )
    await view.select('#billing-mode-search', 'free')
    await view.click('Save draft')
    assert.equal(requests.drafts.length, 2)
    assert.deepEqual(
      requests.drafts[1].tools.map(({ name, price_quota }) => ({
        name,
        price_quota,
      })),
      [
        { name: 'search', price_quota: 0 },
        { name: 'new_tool', price_quota: 1 },
      ],
      'making one tool free must not reset the other tool price'
    )
    await view.select('#billing-mode-new_tool', 'free')
    await view.click('Save draft')
    assert.equal(requests.drafts.length, 3)
    assert.deepEqual(
      requests.drafts[2].tools.map((tool) => tool.price_quota),
      [0, 0]
    )
  } finally {
    await view.dispose()
    requests.restore()
  }
})

test('paid tools with an empty or zero price cannot submit, while explicit free pricing saves zero', async () => {
  const requests = pricingRequests([discovered[0]])
  const view = await renderEditor()
  try {
    await readNewService(view)
    await view.select('#billing-mode-search', 'paid')
    for (const invalidPrice of ['', '0']) {
      await view.input('#price-search', invalidPrice)
      assert.equal(view.button('Save draft').disabled, true, invalidPrice)
      await view.submit()
      assert.deepEqual(requests.drafts, [])
      assert.deepEqual(view.saved, [])
    }
    await view.select('#billing-mode-search', 'free')
    await view.click('Save draft')
    assert.equal(requests.drafts.length, 1)
    assert.equal(requests.drafts[0].tools[0].price_quota, 0)
    assert.deepEqual(view.saved, ['service-pricing'])
  } finally {
    await view.dispose()
    requests.restore()
  }
})

test('net earnings use the current platform fee and refreshing definitions preserves the entered price', async () => {
  const requests = pricingRequests([discovered[0]])
  const view = await renderEditor(undefined, 1000)
  try {
    await readNewService(view)
    await view.select('#billing-mode-search', 'paid')
    await view.input('#price-search', '1')
    assert.match(
      view.container.textContent ?? '',
      /You receive 0\.9 CNY per successful call after the 10% platform fee\./
    )
    await view.rerenderFee(2500)
    assert.match(
      view.container.textContent ?? '',
      /You receive 0\.75 CNY per successful call after the 25% platform fee\./
    )
    await view.click('Read tool definitions')
    assert.equal(
      view.container.querySelector<HTMLSelectElement>('#billing-mode-search')
        ?.value,
      'paid'
    )
    assert.equal(
      view.container.querySelector<HTMLInputElement>('#price-search')?.value,
      '1'
    )
    assert.match(
      view.container.textContent ?? '',
      /You receive 0\.75 CNY per successful call after the 25% platform fee\./
    )
    await view.click('Save draft')
    assert.equal(requests.drafts.length, 1)
    assert.equal(
      requests.drafts[0].tools[0].price_quota,
      500000,
      'the platform fee changes author earnings, not the buyer price'
    )
  } finally {
    await view.dispose()
    requests.restore()
  }
})

test('editor preserves owner policy, requires review, and retries credential writes without a duplicate draft', async () => {
  const originalAdapter = api.defaults.adapter
  const inspections: Record<string, unknown>[] = []
  const drafts: Record<string, unknown>[] = []
  const credentialWrites: Record<string, unknown>[] = []
  api.defaults.adapter = async (config) => {
    const url = config.url ?? ''
    const input =
      typeof config.data === 'string' ? JSON.parse(config.data) : config.data
    let data: unknown
    let success = true
    if (config.method === 'get' && url.endsWith('/credentials')) {
      data = { mode: 'bearer', configured: true, updated_at: 1 }
    } else if (url.endsWith('/inspect')) {
      inspections.push(input)
      data = discovered
    } else if (url.endsWith('/draft')) {
      drafts.push(input)
      data = { ...initial.service, draft_version_id: 'version-new' }
    } else if (config.method === 'put' && url.endsWith('/credentials')) {
      credentialWrites.push(input)
      success = credentialWrites.length > 1
      data = { mode: 'bearer', configured: true, updated_at: 2 }
    } else {
      assert.fail(`Unexpected request: ${config.method} ${url}`)
    }
    return {
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: {
        success,
        code: success ? undefined : 'TOOL_MARKET_UNAVAILABLE',
        data,
      },
    }
  }
  const view = await renderEditor(initial)
  try {
    await view.click('Read tool definitions')
    assert.deepEqual(inspections[0], {
      endpoint: initial.version.endpoint,
      service_id: initial.service.id,
      version_id: initial.version.id,
    })
    assert.equal(
      view.container.querySelector<HTMLInputElement>('#price-search')?.value,
      '0.05'
    )
    assert.equal(
      view.container.querySelector<HTMLInputElement>('input#select-search')
        ?.checked,
      true
    )
    assert.equal(
      view.container.querySelector<HTMLInputElement>('input#select-new_tool')
        ?.checked,
      false
    )
    assert.equal(view.button('Save draft').disabled, true)
    await view.click('Read tool definitions')
    assert.equal(
      view.button('Save draft').disabled,
      true,
      'rereading does not skip unreviewed schema changes'
    )
    const review = [...view.container.querySelectorAll('label')].find((label) =>
      label.textContent?.includes('I reviewed the endpoint')
    )
    assert.ok(review)
    const reviewCheckbox = review.querySelector<HTMLInputElement>(
      'input[type="checkbox"]'
    )
    assert.ok(reviewCheckbox)
    await act(async () => reviewCheckbox.click())
    assert.equal(
      view.button('Save draft').disabled,
      false,
      'review allows saving'
    )
    await view.click('Save draft')
    assert.equal(view.saved.length, 0)
    assert.match(
      view.container.querySelector('[role="alert"]')?.textContent ?? '',
      /draft was saved/
    )
    await view.rerenderInitial({
      ...initial,
      service: { ...initial.service, draft_version_id: 'version-new' },
      version: { ...initial.version, id: 'version-new' },
    })
    await view.click('Save draft')
    assert.deepEqual(view.saved, ['service-existing'])
    assert.equal(drafts.length, 1)
    assert.deepEqual(drafts[0].tools, [
      {
        ...discovered[0],
        permissions: ['read', 'network'],
        price_quota: 25000,
        billing_mode: '',
        input_token_price_quota: 0,
        max_input_tokens: 0,
      },
    ])
    assert.equal('authentication' in drafts[0], false)
    assert.deepEqual(credentialWrites, [
      {
        version_id: 'version-new',
        mode: 'bearer',
        copy_from_version_id: 'version-old',
      },
      {
        version_id: 'version-new',
        mode: 'bearer',
        copy_from_version_id: 'version-old',
      },
    ])
    assert.equal(view.client.getMutationCache().getAll().length, 0)
  } finally {
    await view.dispose()
    api.defaults.adapter = originalAdapter
  }
})

test('replacement credentials stay outside query caches and draft data', async () => {
  const originalAdapter = api.defaults.adapter
  const inputs: { kind: string; data: Record<string, unknown> }[] = []
  api.defaults.adapter = async (config) => {
    const url = config.url ?? ''
    const input =
      typeof config.data === 'string' ? JSON.parse(config.data) : config.data
    let data: unknown
    if (config.method === 'get' && url.endsWith('/credentials')) {
      data = { mode: 'bearer', configured: true, updated_at: 1 }
    } else if (url.endsWith('/inspect')) {
      inputs.push({ kind: 'inspect', data: input })
      data = [discovered[0]]
    } else if (url.endsWith('/draft')) {
      inputs.push({ kind: 'draft', data: input })
      data = { ...initial.service, draft_version_id: 'version-new' }
    } else if (url.endsWith('/credentials')) {
      inputs.push({ kind: 'credential', data: input })
      data = { mode: 'bearer', configured: true, updated_at: 2 }
    } else assert.fail(`Unexpected request: ${url}`)
    return {
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: { success: true, data },
    }
  }
  const view = await renderEditor(initial)
  const fixtureSecret = 'fixture-secret-local-only'
  try {
    const input =
      view.container.querySelector<HTMLInputElement>('#market-secret')
    assert.ok(input)
    assert.equal(input.type, 'password')
    await act(async () => {
      changeNativeInput(input, fixtureSecret)
    })
    await view.click('Read tool definitions')
    assert.deepEqual(inputs[0].data.authentication, {
      mode: 'bearer',
      secret: fixtureSecret,
    })
    assert.equal(
      JSON.stringify(view.client.getQueryCache().getAll()).includes(
        fixtureSecret
      ),
      false
    )
    assert.equal(view.client.getMutationCache().getAll().length, 0)
    const review = [...view.container.querySelectorAll('label')].find((label) =>
      label.textContent?.includes('I reviewed the endpoint')
    )
    assert.ok(review)
    const reviewCheckbox = review.querySelector<HTMLInputElement>(
      'input[type="checkbox"]'
    )
    assert.ok(reviewCheckbox)
    await act(async () => reviewCheckbox.click())
    assert.equal(
      view.button('Save draft').disabled,
      false,
      'review allows saving'
    )
    await view.click('Save draft')
    const draft = inputs.find((item) => item.kind === 'draft')
    assert.ok(draft)
    assert.equal(JSON.stringify(draft.data).includes(fixtureSecret), false)
    assert.equal('authentication' in draft.data, false)
    assert.equal(
      view.container.querySelector<HTMLInputElement>('#market-secret')?.value,
      ''
    )
    assert.deepEqual(view.saved, ['service-existing'])
  } finally {
    await view.dispose()
    api.defaults.adapter = originalAdapter
  }
})

test('changing the endpoint retains edited tool policy but never forwards the old credential', async () => {
  const originalAdapter = api.defaults.adapter
  const inspections: Record<string, unknown>[] = []
  api.defaults.adapter = async (config) => {
    const url = config.url ?? ''
    const input =
      typeof config.data === 'string' ? JSON.parse(config.data) : config.data
    let data: unknown
    if (config.method === 'get' && url.endsWith('/credentials')) {
      data = { mode: 'bearer', configured: true, updated_at: 1 }
    } else if (url.endsWith('/inspect')) {
      inspections.push(input)
      data = discovered
    } else assert.fail(`Unexpected request: ${url}`)
    return {
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: { success: true, data },
    }
  }
  const view = await renderEditor(initial)
  const changeInput = async (id: string, value: string) => {
    const input = view.container.querySelector<HTMLInputElement>(id)
    assert.ok(input)
    await act(async () => {
      changeNativeInput(input, value)
    })
  }
  try {
    await changeInput('#price-search', '0.25')
    await changeInput('#market-secret', 'fixture-for-old-endpoint')
    await changeInput(
      '#market-endpoint',
      'https://replacement.example.test/mcp'
    )
    assert.equal(
      view.container.querySelector<HTMLInputElement>('#market-secret')?.value,
      ''
    )
    assert.equal(
      view.container.querySelector<HTMLInputElement>('#price-search')?.value,
      '0.25'
    )
    assert.equal(
      view.container.querySelector<HTMLInputElement>('input#select-search')
        ?.checked,
      true
    )
    await view.click('Read tool definitions')
    assert.equal(
      inspections.length,
      0,
      'stored credentials must not be copied across endpoints'
    )
    assert.match(
      view.container.querySelector('[role="alert"]')?.textContent ?? '',
      /Enter a credential/
    )
    await changeInput('#market-secret', 'fixture-for-new-endpoint')
    await view.click('Read tool definitions')
    assert.deepEqual(inspections[0], {
      endpoint: 'https://replacement.example.test/mcp',
      authentication: { mode: 'bearer', secret: 'fixture-for-new-endpoint' },
    })
    assert.equal(view.button('Save draft').disabled, true)
    assert.match(view.container.textContent ?? '', /endpoint changed/)
    assert.equal(
      view.container.querySelector<HTMLInputElement>('#price-search')?.value,
      '0.25'
    )
    assert.equal(
      view.container.querySelector<HTMLInputElement>('input#select-new_tool')
        ?.checked,
      false
    )
  } finally {
    await view.dispose()
    api.defaults.adapter = originalAdapter
  }
})

const preciseInputSchema =
  '{"type":"object","properties":{"id":{"type":"integer","minimum":9007199254740993,"maximum":9007199254740997,"default":9007199254740995}}}'
const preciseOutputSchema =
  '{"type":"object","properties":{"id":{"type":"integer","const":9007199254740999}}}'
function preciseDetail(): MarketDetail {
  const item = structuredClone(initial)
  item.tools[0].input_schema = preciseInputSchema
  item.tools[0].output_schema = preciseOutputSchema
  return item
}
function preciseInspectionEnvelope(): string {
  return `{"success":true,"data":[{"name":"search","description":"Search","input_schema":${preciseInputSchema},"input_schema_json":${JSON.stringify(preciseInputSchema)},"output_schema":${preciseOutputSchema},"output_schema_json":${JSON.stringify(preciseOutputSchema)},"permissions":["read"],"price_quota":0}]}`
}

test('stored and rediscovered precise schemas reach the draft HTTP body as exact JSON object values', async () => {
  const originalAdapter = api.defaults.adapter
  const bodies: string[] = []
  api.defaults.adapter = async (config) => {
    const url = config.url ?? ''
    let data: unknown
    if (config.method === 'get' && url.endsWith('/credentials')) {
      data = {
        success: true,
        data: { mode: 'none', configured: false, updated_at: 0 },
      }
    } else if (url.endsWith('/inspect')) {
      // Axios parses the object copy and rounds its unsafe integers. Only the
      // accompanying raw schema text can preserve the provider's actual values.
      data = preciseInspectionEnvelope()
    } else if (url.endsWith('/draft')) {
      assert.equal(typeof config.data, 'string')
      bodies.push(config.data)
      data = {
        success: true,
        data: { ...initial.service, draft_version_id: 'version-new' },
      }
    } else if (config.method === 'put' && url.endsWith('/credentials')) {
      data = {
        success: true,
        data: { mode: 'none', configured: false, updated_at: 0 },
      }
    } else assert.fail(`Unexpected request: ${url}`)
    return { config, status: 200, statusText: 'OK', headers: {}, data }
  }
  const view = await renderEditor(preciseDetail())
  try {
    assert.equal(
      view.container.querySelector('pre')?.textContent,
      preciseInputSchema
    )
    await view.click('Read tool definitions')
    assert.equal(view.container.querySelector('[role="alert"]'), null)
    assert.equal(
      view.button('Save draft').disabled,
      false,
      'identical exact schemas do not require a change acknowledgment'
    )
    await view.click('Save draft')
    assert.equal(bodies.length, 1)
    assert.ok(bodies[0].includes(`"input_schema":${preciseInputSchema}`))
    assert.ok(bodies[0].includes(`"output_schema":${preciseOutputSchema}`))
    assert.equal(bodies[0].includes('input_schema_json'), false)
    assert.equal(bodies[0].includes('output_schema_json'), false)
    assert.equal(bodies[0].includes('9007199254740992'), false)
    const saved = JSON.parse(bodies[0])
    assert.equal(typeof saved.tools[0].input_schema, 'object')
    assert.deepEqual(saved.tools[0].permissions, ['read', 'network'])
    assert.equal(saved.tools[0].price_quota, 25000)
    assert.deepEqual(view.saved, ['service-existing'])
  } finally {
    await view.dispose()
    api.defaults.adapter = originalAdapter
  }
})

test('an unsafe legacy inspection fails visibly and invalidates the earlier save permission', async () => {
  const originalAdapter = api.defaults.adapter
  let reads = 0
  api.defaults.adapter = async (config) => {
    const url = config.url ?? ''
    let data: unknown
    if (config.method === 'get' && url.endsWith('/credentials')) {
      data = {
        success: true,
        data: { mode: 'none', configured: false, updated_at: 0 },
      }
    } else if (url.endsWith('/inspect')) {
      reads++
      data =
        reads === 1
          ? preciseInspectionEnvelope()
          : {
              success: true,
              data: [
                {
                  ...discovered[0],
                  input_schema: JSON.parse(preciseInputSchema),
                },
              ],
            }
    } else assert.fail('A rejected schema must not be saved or executed')
    return { config, status: 200, statusText: 'OK', headers: {}, data }
  }
  const view = await renderEditor(preciseDetail())
  try {
    await view.click('Read tool definitions')
    assert.equal(view.button('Save draft').disabled, false)
    await view.click('Read tool definitions')
    assert.match(
      view.container.querySelector('[role="alert"]')?.textContent ?? '',
      /operation failed/
    )
    assert.equal(view.button('Save draft').disabled, true)
    assert.equal(
      view.container.querySelector('pre')?.textContent,
      preciseInputSchema
    )
    assert.deepEqual(view.saved, [])
  } finally {
    await view.dispose()
    api.defaults.adapter = originalAdapter
  }
})

async function acknowledgePricingDefinitions(
  view: Awaited<ReturnType<typeof renderEditor>>
) {
  const label = [...view.container.querySelectorAll('label')].find((item) =>
    item.textContent?.includes('I reviewed the endpoint')
  )
  const checkbox = label?.querySelector<HTMLInputElement>(
    'input[type="checkbox"]'
  )
  if (checkbox && !checkbox.checked) await act(async () => checkbox.click())
}

test('metered pricing keeps the actual input rate and a separate refundable cap through discovery', async () => {
  const requests = pricingRequests()
  const authorized = structuredClone(initial)
  authorized.tools[0].available_metering_metrics = ['input_tokens']
  authorized.tools[0].input_schema = JSON.stringify(discovered[0].input_schema)
  const view = await renderEditor(authorized)
  try {
    await view.click('Read tool definitions')
    await view.select('#billing-mode-search', 'input_tokens')
    await view.input('#price-search', '2.94')
    await view.input('#token-limit-search', '65536')
    await view.click('Read tool definitions')
    await acknowledgePricingDefinitions(view)
    await view.click('Save draft')
    assert.equal(requests.drafts.length, 1)
    const tool = requests.drafts[0].tools.find(({ name }) => name === 'search')
    assert.ok(tool)
    assert.equal(tool.billing_mode, 'input_tokens')
    assert.equal(tool.input_token_price_quota, 1470000)
    assert.equal(tool.max_input_tokens, 65536)
    assert.equal(tool.price_quota, 96338)
  } finally {
    await view.dispose()
    requests.restore()
  }
})

test('ordinary publishers cannot select unverified usage pricing', async () => {
  const requests = pricingRequests()
  const view = await renderEditor()
  try {
    await readNewService(view)
    const mode = view.container.querySelector<HTMLSelectElement>(
      '#billing-mode-search'
    )
    assert.ok(mode)
    assert.equal(
      mode.querySelector<HTMLOptionElement>('[value="input_tokens"]')?.disabled,
      true
    )
    assert.equal(
      mode.querySelector<HTMLOptionElement>('[value="metered"]')?.disabled,
      true
    )
    assert.match(view.container.textContent ?? '', /platform-controlled meter/)
    await view.select('#billing-mode-search', 'input_tokens')
    await view.input('#price-search', '2.94')
    assert.equal(view.button('Save draft').disabled, true)
  } finally {
    await view.dispose()
    requests.restore()
  }
})

test('authorized resource pricing saves a combination and refundable cap', async () => {
  const requests = pricingRequests()
  const authorized = structuredClone(initial)
  authorized.tools[0].input_schema = JSON.stringify(discovered[0].input_schema)
  authorized.tools[0].available_metering_metrics = [
    'cpu_core_milliseconds',
    'memory_mib_seconds',
  ]
  authorized.tools[0].billing_mode = 'metered'
  authorized.tools[0].billing_rules = [
    { metric: 'cpu_core_milliseconds', rate_quota: 500000, max_quantity: 2000 },
    { metric: 'memory_mib_seconds', rate_quota: 250000, max_quantity: 1024 },
  ]
  authorized.tools[0].price_quota = 1250000
  const view = await renderEditor(authorized)
  try {
    await view.click('Read tool definitions')
    assert.match(view.container.textContent ?? '', /CPU core-second/)
    assert.match(view.container.textContent ?? '', /Memory GiB-second/)
    await acknowledgePricingDefinitions(view)
    await view.click('Save draft')
    assert.equal(requests.drafts.length, 1)
    const tool = requests.drafts[0].tools[0]
    assert.equal(tool.billing_mode, 'metered')
    assert.deepEqual(tool.billing_rules, authorized.tools[0].billing_rules)
    assert.equal(tool.price_quota, 1250000)
    assert.equal(tool.input_token_price_quota, 0)
    assert.equal(tool.max_input_tokens, 0)
  } finally {
    await view.dispose()
    requests.restore()
  }
})

test('changing the display unit mid-editor retains one-credit prices and metered rate payloads', async () => {
  const requests = pricingRequests()
  const authorized = structuredClone(initial)
  authorized.tools[0].available_metering_metrics = ['input_tokens']
  authorized.tools[0].input_schema = JSON.stringify(discovered[0].input_schema)
  const view = await renderEditor(authorized)
  try {
    await view.click('Read tool definitions')
    await view.select('#billing-mode-search', 'input_tokens')
    await view.input('#price-search', '2.94')
    await view.input('#token-limit-search', '65536')
    await act(async () =>
      view.container
        .querySelector<HTMLInputElement>('#select-new_tool')
        ?.click()
    )
    await acknowledgePricingDefinitions(view)
    await view.select('#billing-mode-new_tool', 'paid')
    await view.input('#price-new_tool', '0.000002')
    for (const unit of ['USD', 'CREDIT', 'CNY'] as const) {
      await act(async () =>
        useWalletCurrencyPreferenceStore.getState().setPreference(unit)
      )
      const rate =
        view.container.querySelector<HTMLInputElement>('#price-search')
      const smallest =
        view.container.querySelector<HTMLInputElement>('#price-new_tool')
      assert.ok(rate)
      assert.ok(smallest)
      assert.equal(
        rate.value,
        unit === 'USD' ? '0.42' : unit === 'CREDIT' ? '1470000' : '2.94'
      )
      assert.equal(
        smallest.value,
        unit === 'USD'
          ? '0.000000285714285714285714285715'
          : unit === 'CREDIT'
            ? '1'
            : '0.000002'
      )
      assert.equal(view.button('Save draft').disabled, false)
    }
    await act(async () =>
      useWalletCurrencyPreferenceStore.getState().setPreference('USD')
    )
    const smallestInput =
      view.container.querySelector<HTMLInputElement>('#price-new_tool')
    assert.ok(smallestInput)
    await view.input('#price-new_tool', smallestInput.value)
    await view.click('Read tool definitions')
    await view.click('Save draft')
    const rate = requests.drafts[0].tools.find((tool) => tool.name === 'search')
    assert.ok(rate)
    assert.equal(rate.input_token_price_quota, 1470000)
    assert.equal(rate.price_quota, 96338)
    assert.equal(rate.max_input_tokens, 65536)
    const smallestTool = requests.drafts[0].tools.find(
      (tool) => tool.name === 'new_tool'
    )
    assert.ok(smallestTool)
    assert.equal(smallestTool.price_quota, 1)
  } finally {
    await view.dispose()
    requests.restore()
  }
})
