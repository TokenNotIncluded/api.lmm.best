/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({ url: 'https://console.example.test/settings' })
dom.document.write('<!doctype html><html><head></head><body></body></html>')
Object.defineProperty(dom.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'NodeFilter',
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
  'localStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { useForm } = await import('react-hook-form')
const { zodResolver } = await import('@hookform/resolvers/zod')
const z = await import('zod')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { useSystemConfigStore, DEFAULT_CURRENCY_CONFIG } =
  await import('@/stores/system-config-store')
const { useAuthStore } = await import('@/stores/auth-store')
const { LegacyUsdMinimumInput } = await import('./legacy-usd-minimum-input')
const { PaymentMethodDialog } = await import('./payment-method-dialog')
const { WaffoSettingsSection } = await import('./waffo-settings-section')
const originalConfig = useSystemConfigStore.getState().config
const originalAuth = useAuthStore.getState().auth
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const currencyConfig = {
  ...DEFAULT_CURRENCY_CONFIG,
  currencyUnit: 'credit' as const,
  creditsPerUsd: 500000,
  creditsPerUsdExact: '500000',
  quotaPerUnit: 500000,
  cnyPerUsd: 6.8,
}
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 20))
}
async function render(element: React.ReactNode) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(<I18nextProvider i18n={i18n}>{element}</I18nextProvider>)
    await flush()
  })
  return {
    container,
    close: async () => {
      await act(async () => root.unmount())
    },
  }
}
async function edit(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(
    dom.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setter)
  await act(async () => {
    setter.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await flush()
  })
}
async function submit(form: HTMLFormElement) {
  await act(async () => {
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await flush()
  })
}
const minimumSchema = z.object({
  minimum: z.number().int().min(0).max(Number.MAX_SAFE_INTEGER),
})
function MinimumForm({ onSave }: { onSave: (value: number) => void }) {
  const form = useForm({
    resolver: zodResolver(minimumSchema),
    defaultValues: { minimum: 1 },
  })
  return (
    <form onSubmit={form.handleSubmit((data) => onSave(data.minimum))}>
      <LegacyUsdMinimumInput
        name='minimum'
        value={form.watch('minimum')}
        onChange={(value) => form.setValue('minimum', value)}
      />
      <button type='submit'>Save</button>
    </form>
  )
}
beforeEach(() => {
  useSystemConfigStore.getState().setConfig({ currency: currencyConfig })
  useAuthStore.setState({ auth: { ...originalAuth, user: null } })
})
afterEach(() => {
  useSystemConfigStore.setState({ config: originalConfig })
  useAuthStore.setState({ auth: originalAuth })
  document.body.replaceChildren()
})
after(() => dom.close())

test('projected minimum preserves untouched storage and rejects an inexact USD edit', async () => {
  const saved: number[] = []
  const ui = await render(<MinimumForm onSave={(value) => saved.push(value)} />)
  try {
    const input = ui.container.querySelector<HTMLInputElement>('input')
    const form = ui.container.querySelector('form')
    assert.ok(input)
    assert.ok(form)
    const originalDisplay = input.value
    assert.match(originalDisplay, /^1$/)
    await submit(form)
    assert.deepEqual(saved, [1])
    await edit(input, '1.5')
    assert.equal(input.value, '1.5')
    assert.match(
      ui.container.textContent ?? '',
      /Enter a USD amount that equals a whole recharge increment/
    )
    await submit(form)
    assert.deepEqual(saved, [1])
    // Returning to the original USD amount restores its stored integer.
    await edit(input, originalDisplay)
    await submit(form)
    assert.deepEqual(saved, [1, 1])
    await edit(input, '10')
    await submit(form)
    assert.deepEqual(saved, [1, 1, 10])
    assert.equal(input.value, '10')
  } finally {
    await ui.close()
  }
})

test('missing fixed denomination disables editing while preserving the existing minimum', async () => {
  useSystemConfigStore.getState().setConfig({
    currency: { ...currencyConfig, creditsPerUsd: 0, creditsPerUsdExact: '' },
  })
  const saved: number[] = []
  const ui = await render(<MinimumForm onSave={(value) => saved.push(value)} />)
  try {
    const input = ui.container.querySelector<HTMLInputElement>('input')
    const form = ui.container.querySelector('form')
    assert.ok(input)
    assert.ok(form)
    assert.equal(input.disabled, true)
    assert.match(ui.container.textContent ?? '', /denomination is unavailable/)
    await submit(form)
    assert.deepEqual(saved, [1])
    await act(async () =>
      useSystemConfigStore.getState().setConfig({ currency: currencyConfig })
    )
    assert.equal(input.disabled, false)
    assert.match(input.value, /^1$/)
    await submit(form)
    assert.deepEqual(saved, [1, 1])
  } finally {
    await ui.close()
  }
})

test('custom gateway previews native direct rates and preserves USD limits and exact old pricing on save', async () => {
  const saved: import('./payment-method-dialog').PaymentMethodData[] = []
  const original = {
    name: 'Native gateway',
    type: 'epay',
    icon: '',
    min_topup: '1.2500',
    max_topup: '2.5000',
    settlement_unit: 'LDC',
    unit_price: '1.2300',
    settlement_units_per_platform_unit: '1.2300',
  }
  const ui = await render(
    <PaymentMethodDialog
      open
      onOpenChange={() => undefined}
      onSave={(value) => saved.push(value)}
      editData={original}
    />
  )
  try {
    assert.match(
      document.body.textContent ?? '',
      /1 USD credited costs 1\.23 LDC/
    )
    assert.match(
      document.body.textContent ?? '',
      /Minimum credited amount per payment \(USD, optional\)/
    )
    const form = document.querySelector('form')
    assert.ok(form)
    await submit(form)
    assert.equal(saved.length, 1)
    for (const key of [
      'min_topup',
      'max_topup',
      'settlement_unit',
      'unit_price',
      'settlement_units_per_platform_unit',
    ] as const) {
      assert.equal(saved[0][key], original[key], key)
    }
    assert.equal(saved[0].settlement_units_per_usd, undefined)
    assert.equal(saved[0].settlement_currency, undefined)
  } finally {
    await ui.close()
  }
})

test('canonical real-USD settlement fields remain exact and independent of display preferences', async () => {
  const saved: import('./payment-method-dialog').PaymentMethodData[] = []
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...currencyConfig, cnyPerUsd: 999 } })
  const original = {
    name: 'Fiat gateway',
    type: 'epay',
    icon: '',
    min_topup: '0.50',
    max_topup: '2.5',
    settlement_currency: 'CNY',
    settlement_units_per_usd: '6.8000',
    platform_units_per_usd: '6.8000',
  }
  const ui = await render(
    <PaymentMethodDialog
      open
      onOpenChange={() => undefined}
      onSave={(value) => saved.push(value)}
      editData={original}
    />
  )
  try {
    assert.match(
      document.body.textContent ?? '',
      /Settlement preview: 1 USD = 6\.8000 CNY/
    )
    const form = document.querySelector('form')
    assert.ok(form)
    await submit(form)
    assert.equal(saved.length, 1)
    for (const key of [
      'min_topup',
      'max_topup',
      'settlement_currency',
      'settlement_units_per_usd',
      'platform_units_per_usd',
    ] as const) {
      assert.equal(saved[0][key], original[key], key)
    }
  } finally {
    await ui.close()
  }
})

test('ordinary Epay defaults to CNY without silently inserting a settlement unit', async () => {
  const saved: import('./payment-method-dialog').PaymentMethodData[] = []
  const ui = await render(
    <PaymentMethodDialog
      open
      onOpenChange={() => undefined}
      onSave={(value) => saved.push(value)}
      editData={{
        name: 'Ordinary gateway',
        type: 'epay',
        icon: '',
        unit_price: '1.0',
      }}
    />
  )
  try {
    assert.match(document.body.textContent ?? '', /1 USD credited costs 1 CNY/)
    const form = document.querySelector('form')
    assert.ok(form)
    await submit(form)
    assert.equal(saved.length, 1)
    assert.equal(saved[0].unit_price, '1.0')
    assert.equal(saved[0].settlement_unit, undefined)
  } finally {
    await ui.close()
  }
})

test('LinuxDO direct pricing requires an explicit native unit before saving', async () => {
  const saved: import('./payment-method-dialog').PaymentMethodData[] = []
  const ui = await render(
    <PaymentMethodDialog
      open
      onOpenChange={() => undefined}
      onSave={(value) => saved.push(value)}
      editData={{
        name: 'LinuxDO Credit',
        type: 'epay',
        icon: '',
        unit_price: '1.0',
      }}
    />
  )
  try {
    assert.doesNotMatch(
      document.body.textContent ?? '',
      /1 USD credited costs 1 CNY/
    )
    const form = document.querySelector('form')
    assert.ok(form)
    await submit(form)
    assert.equal(saved.length, 0)
    const unit = document.querySelector<HTMLInputElement>(
      'input[name="settlement_unit"]'
    )
    assert.ok(unit)
    await edit(unit, 'LDC')
    assert.match(document.body.textContent ?? '', /1 USD credited costs 1 LDC/)
    await submit(form)
    assert.equal(saved.length, 1)
    assert.equal(saved[0].settlement_unit, 'LDC')
    assert.equal(saved[0].unit_price, '1.0')
  } finally {
    await ui.close()
  }
})

test('fixed denomination retains the original minimum after an inexact edit', async () => {
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...currencyConfig,
      creditsPerUsd: 500000,
      creditsPerUsdExact: '500000',
      legacyPricingUnitsPerUsd: 1,
    },
  })
  const saved: number[] = []
  const ui = await render(<MinimumForm onSave={(value) => saved.push(value)} />)
  try {
    const input = ui.container.querySelector<HTMLInputElement>('input')
    const form = ui.container.querySelector('form')
    assert.ok(input)
    assert.ok(form)
    const originalDisplay = input.value
    assert.match(originalDisplay, /^1$/)
    await edit(input, '1.5')
    await submit(form)
    assert.deepEqual(saved, [])
    await edit(input, originalDisplay)
    await submit(form)
    assert.deepEqual(saved, [1])
    assert.equal(input.value, originalDisplay)
  } finally {
    await ui.close()
  }
})

test('Waffo shows its integer minimum as actual USD and emits legacy integers for valid edits', async () => {
  const changes: unknown[] = []
  const values = {
    WaffoEnabled: true,
    WaffoApiKey: '',
    WaffoPrivateKey: '',
    WaffoPublicCert: '',
    WaffoSandboxPublicCert: '',
    WaffoSandboxApiKey: '',
    WaffoSandboxPrivateKey: '',
    WaffoSandbox: false,
    WaffoMerchantId: '',
    WaffoCurrency: 'USD',
    WaffoUnitPrice: 1,
    WaffoMinTopUp: 1,
    WaffoNotifyUrl: '',
    WaffoReturnUrl: '',
    WaffoPayMethods: '[]',
  }
  const ui = await render(
    <WaffoSettingsSection
      values={values}
      onValueChange={(key, value) => changes.push({ key, value })}
      payMethods={[]}
      onPayMethodsChange={() => undefined}
    />
  )
  try {
    assert.match(ui.container.textContent ?? '', /Minimum top-up \(USD\)/)
    assert.match(
      ui.container.textContent ?? '',
      /fixed Credit denomination converts credited value to USD/
    )
    const input = [
      ...ui.container.querySelectorAll<HTMLInputElement>(
        'input[type="number"]'
      ),
    ].find((candidate) => candidate.value === '1')
    assert.ok(input)
    assert.deepEqual(changes, [])
    await edit(input, '10')
    assert.deepEqual(changes, [{ key: 'WaffoMinTopUp', value: 10 }])
  } finally {
    await ui.close()
  }
})
