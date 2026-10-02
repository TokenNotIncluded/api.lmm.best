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
const { ServiceEditor } = await import('./service-editor')
type MarketDetail = import('./api').MarketDetail
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

async function renderEditor(initial?: MarketDetail) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const saved: string[] = []
  await act(async () =>
    root.render(
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={i18n}>
          <ServiceEditor
            initial={initial}
            units={500000}
            onSaved={(id) => saved.push(id)}
            onCancel={() => {}}
          />
        </I18nextProvider>
      </QueryClientProvider>
    )
  )
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
    rerenderInitial: async (next: MarketDetail) => {
      await act(async () =>
        root.render(
          <QueryClientProvider client={client}>
            <I18nextProvider i18n={i18n}>
              <ServiceEditor
                initial={next}
                units={500000}
                onSaved={(id) => saved.push(id)}
                onCancel={() => {}}
              />
            </I18nextProvider>
          </QueryClientProvider>
        )
      )
      await settle()
    },
    click: async (name: string) => {
      await act(async () => {
        if (name === 'Save draft') {
          assert.equal(button(name).disabled, false)
          // Happy DOM's floating-point step validation rejects valid decimal
          // prices. Exercise the submit handler after checking the UI gate.
          const form = container.querySelector('form')
          assert.ok(form)
          form.dispatchEvent(
            new Event('submit', { bubbles: true, cancelable: true })
          )
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
