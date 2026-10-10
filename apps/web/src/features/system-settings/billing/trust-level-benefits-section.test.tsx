/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

import type { UpdateOptionRequest } from '../types'
import type { TrustLevelBenefitsConfig } from './trust-level-benefits-schema'

const domWindow = new Window({
  url: 'https://console.example.test/system-settings/billing/levels-benefits',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
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
  'scrollTo',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
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
const {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} = await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { getSystemSettingsNavGroups } =
  await import('@/components/layout/config/system-settings.config')
const { SettingsPageProvider } =
  await import('../components/settings-page-context')
const { TrustLevelBenefitsSection } =
  await import('./trust-level-benefits-section')
const { getBillingSectionNavItems } = await import('./section-registry')

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
after(() => domWindow.close())
const config: TrustLevelBenefitsConfig = {
  version: 1,
  paid_activation_enabled: true,
  decay_period_days: 30,
  tiers: [0, 1, 2, 3, 4].map((level) => ({
    level,
    min_paid_credits: level * 100,
    discount_ratio: 1 - level / 100,
    benefits:
      level === 0
        ? ['standard_access']
        : ['developer_access', 'usage_discount'],
  })),
  role_tiers: [
    {
      level: 5,
      role: 10,
      discount_ratio: 0.9,
      benefits: ['administrator_access', 'usage_discount'],
    },
    {
      level: 6,
      role: 100,
      discount_ratio: 0.85,
      benefits: ['superadministrator_access', 'usage_discount'],
    },
  ],
}

async function renderSection(raw = JSON.stringify(config), supported = true) {
  const container = document.createElement('div')
  const mount = document.createElement('div')
  const actionsContainer = document.createElement('div')
  container.append(mount, actionsContainer)
  document.body.append(container)
  const root = createRoot(mount)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  queryClient.setQueryData(['system-options'], {
    success: true,
    message: '',
    data: [{ key: 'TrustLevelBenefits', value: raw }],
    capabilities: { trust_level_benefits: supported },
  })
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <SettingsPageProvider actionsContainer={actionsContainer}>
            <TrustLevelBenefitsSection value={raw} />
          </SettingsPageProvider>
        </I18nextProvider>
      </QueryClientProvider>
    ),
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([
      createRoute({
        getParentRoute: () => rootRoute,
        path: '/',
        component: () => null,
      }),
    ]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await act(async () => {
    root.render(<RouterProvider router={router} />)
    await new Promise((resolve) => setTimeout(resolve, 20))
  })
  return {
    container,
    cleanup: async () => {
      await act(async () => root.unmount())
      queryClient.clear()
      container.remove()
    },
  }
}

async function edit(container: HTMLElement, name: string, value: string) {
  const input = container.querySelector<HTMLInputElement>(
    `input[name="${name}"]`
  )
  assert.ok(input)
  const setValue = Object.getOwnPropertyDescriptor(
    domWindow.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setValue)
  await act(async () => {
    setValue.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
}
async function submit(container: HTMLElement) {
  const form = container.querySelector('form')
  assert.ok(form)
  await act(async () => {
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await new Promise((resolve) => setTimeout(resolve, 40))
  })
}

test('the dedicated settings navigation points to an editor with exactly four recharge thresholds', async () => {
  const entry = getBillingSectionNavItems(i18n.t).find(
    (item) => item.url === '/system-settings/billing/levels-benefits'
  )
  assert.equal(entry?.title, 'Levels & Benefits')
  assert.ok(
    getSystemSettingsNavGroups(i18n.t).some((group) =>
      group.items.some(
        (item) =>
          'items' in item &&
          item.items?.some((child) => child.url === entry?.url)
      )
    )
  )
  const rendered = await renderSection()
  try {
    const thresholds = rendered.container.querySelectorAll<HTMLInputElement>(
      'input[name$="min_paid_credits"]'
    )
    assert.equal(thresholds.length, 4)
    assert.deepEqual(
      [...thresholds].map((input) => input.value),
      ['100', '200', '300', '400']
    )
    assert.equal(
      rendered.container.querySelectorAll('input[name$="discount_ratio"]')
        .length,
      7
    )
    assert.equal(
      rendered.container.querySelectorAll(
        'input[name^="tiers.5"], input[name^="tiers.6"]'
      ).length,
      0
    )
    assert.match(
      rendered.container.textContent ?? '',
      /There are no recharge thresholds for L5 or L6/
    )
  } finally {
    await rendered.cleanup()
  }
})

test('saves the complete versioned config in one option write and preserves independent tier values', async () => {
  const original = api.put
  const originalGet = api.get
  api.get = (async () => ({
    data: {
      success: true,
      message: '',
      data: [{ key: 'TrustLevelBenefits', value: JSON.stringify(config) }],
      capabilities: { trust_level_benefits: true },
    },
  })) as typeof api.get
  const updates: UpdateOptionRequest[] = []
  api.put = (async (url: string, request: UpdateOptionRequest) => {
    assert.equal(url, '/api/option/')
    updates.push(request)
    return { data: { success: true, message: '' } }
  }) as typeof api.put
  const rendered = await renderSection()
  try {
    await edit(rendered.container, 'tiers.4.min_paid_credits', '450')
    await edit(rendered.container, 'tiers.2.discount_ratio', '12.5')
    await edit(rendered.container, 'role_tiers.1.discount_ratio', '20')
    const usage = rendered.container.querySelector<HTMLElement>(
      '#level-3-benefit-usage_discount'
    )
    assert.ok(usage)
    await act(async () => usage.click())
    await submit(rendered.container)
    assert.equal(updates.length, 1)
    assert.equal(updates[0].key, 'TrustLevelBenefits')
    const saved = JSON.parse(
      String(updates[0].value)
    ) as TrustLevelBenefitsConfig
    assert.equal(saved.tiers[4].min_paid_credits, 450)
    assert.equal(saved.tiers[2].discount_ratio, 0.875)
    assert.deepEqual(saved.tiers[3].benefits, ['developer_access'])
    assert.deepEqual(saved.tiers[1], config.tiers[1])
    assert.equal(saved.tiers.length, 5)
    assert.ok(saved.role_tiers)
    assert.ok(config.role_tiers)
    assert.equal(saved.role_tiers[1].discount_ratio, 0.8)
    assert.deepEqual(saved.role_tiers[0], config.role_tiers[0])
    assert.equal('min_paid_credits' in saved.role_tiers[1], false)
    await submit(rendered.container)
    assert.equal(
      updates.length,
      1,
      'an acknowledged unchanged config must not be written twice'
    )
  } finally {
    api.put = original
    api.get = originalGet
    await rendered.cleanup()
  }
})

test('invalid cumulative thresholds never write, and a rejected save retains the editable draft', async () => {
  const original = api.put
  const originalGet = api.get
  api.get = (async () => ({
    data: {
      success: true,
      message: '',
      data: [{ key: 'TrustLevelBenefits', value: JSON.stringify(config) }],
      capabilities: { trust_level_benefits: true },
    },
  })) as typeof api.get
  const updates: UpdateOptionRequest[] = []
  api.put = (async (_url: string, request: UpdateOptionRequest) => {
    updates.push(request)
    return { data: { success: false, message: 'Rejected configuration' } }
  }) as typeof api.put
  const rendered = await renderSection()
  try {
    await edit(rendered.container, 'tiers.2.min_paid_credits', '100')
    await submit(rendered.container)
    assert.equal(updates.length, 0)
    assert.match(
      rendered.container.textContent ?? '',
      /Recharge thresholds must increase from L1 to L4/
    )
    await edit(rendered.container, 'tiers.2.min_paid_credits', '250')
    await edit(rendered.container, 'decay_period_days', '')
    await submit(rendered.container)
    assert.equal(
      updates.length,
      0,
      'a blank review period must not silently disable decay'
    )
    await edit(rendered.container, 'decay_period_days', '30')
    await submit(rendered.container)
    assert.equal(updates.length, 1)
    assert.equal(
      rendered.container.querySelector<HTMLInputElement>(
        'input[name="tiers.2.min_paid_credits"]'
      )?.value,
      '250'
    )
    const save = rendered.container.querySelector<HTMLButtonElement>(
      '.settings-form-actions button:last-child'
    )
    assert.ok(
      save && !save.disabled,
      'a rejected save must keep the dirty draft available for retry'
    )
  } finally {
    api.put = original
    api.get = originalGet
    await rendered.cleanup()
  }
})

test('missing server configuration never substitutes invented thresholds or exposes a save action', async () => {
  const rendered = await renderSection('')
  try {
    assert.equal(rendered.container.querySelector('form'), null)
    assert.equal(rendered.container.querySelector('input'), null)
    assert.match(
      rendered.container.textContent ?? '',
      /Level configuration unavailable/
    )
  } finally {
    await rendered.cleanup()
  }
})

test('an older server is not editable and a rolled-back capability is checked again before writing', async () => {
  const old = await renderSection(JSON.stringify(config), false)
  try {
    assert.equal(old.container.querySelector('form'), null)
    assert.match(
      old.container.textContent ?? '',
      /server does not support configurable level benefits/
    )
  } finally {
    await old.cleanup()
  }
  const originalGet = api.get
  const originalPut = api.put
  let writes = 0
  api.get = (async () => ({
    data: { success: true, message: '', data: [], capabilities: {} },
  })) as typeof api.get
  api.put = (async () => {
    writes++
    return { data: { success: true, message: '' } }
  }) as typeof api.put
  const rendered = await renderSection()
  try {
    await edit(rendered.container, 'tiers.4.min_paid_credits', '500')
    await submit(rendered.container)
    assert.equal(writes, 0)
    assert.equal(
      rendered.container.querySelector<HTMLInputElement>(
        'input[name="tiers.4.min_paid_credits"]'
      )?.value,
      '500'
    )
    assert.match(
      rendered.container.textContent ?? '',
      /server does not support configurable level benefits/
    )
  } finally {
    api.get = originalGet
    api.put = originalPut
    await rendered.cleanup()
  }
})
