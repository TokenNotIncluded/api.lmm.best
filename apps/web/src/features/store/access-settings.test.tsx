/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type { StoreAccessSettings } from './access-types'

const dom = new Window({ url: 'https://shop.example.test/store/seller' })
dom.document.write('<!doctype html><html><head></head><body></body></html>')
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLTextAreaElement',
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
  'CSSStyleSheet',
  'localStorage',
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
const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { api } = await import('@/lib/api')
const { StoreMerchantTermsForm, StoreMerchantTermsAcceptance } =
  await import('./merchant-terms')
const { useStoreMerchantTerms } = await import('./use-store-merchant-terms')
const originalGet = api.get
const originalPut = api.put
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { StoreProductAccessSettings } = await import('./access-settings')
const { storeAccessSupported, storeVisibility, storePurchaseLoginRequired } =
  await import('./access-types')
const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
let root: ReturnType<typeof createRoot> | undefined
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 35))
}
async function mount(node: React.ReactNode) {
  const host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  const mounted = root
  await act(async () => {
    mounted.render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>)
    await flush()
  })
  await act(flush)
}
async function click(id: string) {
  const node = document.getElementById(id)
  assert.ok(node, id)
  await act(async () => {
    node.click()
    await flush()
  })
}
async function button(text: string) {
  const node = [...document.querySelectorAll<HTMLButtonElement>('button')].find(
    (button) => button.textContent?.trim() === text
  )
  assert.ok(node, text)
  await act(async () => {
    node.click()
    await flush()
  })
}
function Fixture({
  initial,
  changed,
}: {
  initial: StoreAccessSettings
  changed: StoreAccessSettings[]
}) {
  const [value, setValue] = useState(initial)
  return (
    <StoreProductAccessSettings
      value={value}
      onChange={(next) => {
        changed.push(next)
        setValue(next)
      }}
    />
  )
}
afterEach(async () => {
  const mounted = root
  if (mounted) await act(async () => mounted.unmount())
  root = undefined
  api.get = originalGet
  api.put = originalPut
  document.body.replaceChildren()
})
after(() => dom.happyDOM.abort())

test('legacy products keep public browsing and required sign-in; capability requires literal true', () => {
  assert.equal(storeVisibility({}), 'public')
  assert.equal(storePurchaseLoginRequired({}), true)
  assert.equal(storeVisibility({ test_mode: true }), 'private')
  assert.equal(
    storeVisibility({ visibility: 'public', test_mode: true }),
    'public'
  )
  assert.equal(storeAccessSupported({ store_access_supported: 'true' }), false)
  assert.equal(storeAccessSupported({}), false)
  assert.equal(storeAccessSupported({ store_access_supported: true }), true)
})
test('guest collection changes only after the seller confirms, and cancel preserves settings', async () => {
  const changed: StoreAccessSettings[] = []
  await mount(
    <Fixture
      initial={{
        visibility: 'public',
        purchase_login_required: true,
        pickup_login_required: true,
      }}
      changed={changed}
    />
  )
  await click('store-purchase-login')
  assert.equal(changed.length, 0)
  assert.match(
    document.body.textContent || '',
    /Continuing turns off account-only collection/
  )
  await button('Cancel')
  assert.equal(changed.length, 0)
  await click('store-purchase-login')
  await button('Allow guest collection')
  assert.deepEqual(changed, [
    {
      visibility: 'public',
      purchase_login_required: false,
      pickup_login_required: false,
    },
  ])
})
test('registered visibility requires sign-in and private replaces the independent test-mode toggle', async () => {
  const changed: StoreAccessSettings[] = []
  await mount(
    <Fixture
      initial={{
        visibility: 'public',
        purchase_login_required: false,
        pickup_login_required: false,
      }}
      changed={changed}
    />
  )
  await click('store-visibility-registered')
  assert.equal(changed.at(-1)?.purchase_login_required, true)
  const login = document.getElementById('store-purchase-login')
  assert.ok(login)
  assert.equal((login as HTMLInputElement).checked, true)
  assert.equal(login.hasAttribute('disabled'), true)
  await click('store-visibility-private')
  assert.equal(changed.at(-1)?.visibility, 'private')
  assert.match(document.body.textContent || '', /Only you can view and buy/)
})

const terms = {
  version: '11111111-1111-1111-1111-111111111111',
  content: 'Delivery within one day.',
  required: true as const,
  configured: true,
  accepted: true,
  updated_at: 1,
}
const response = (data: unknown) => ({ data: { success: true, data } })
function TermsFixture({ supported = true }: { supported?: boolean }) {
  const state = useStoreMerchantTerms('product-fixture', supported, 2)
  return (
    <>
      {supported && <StoreMerchantTermsAcceptance {...state} />}
      <button
        type='button'
        id='terms-refresh'
        onClick={() => void state.refresh()}
      >
        Refresh terms
      </button>
      <button type='button' id='terms-order' disabled={!state.ready}>
        Place order
      </button>
      <output id='terms-version'>{state.terms?.version}</output>
    </>
  )
}
async function textarea(value: string) {
  const node = document.getElementById('store-seller-terms')
  assert.ok(node)
  const setter = Object.getOwnPropertyDescriptor(
    dom.HTMLTextAreaElement.prototype,
    'value'
  )?.set
  assert.ok(setter)
  await act(async () => {
    setter.call(node, value)
    node.dispatchEvent(new Event('input', { bubbles: true }))
    await flush()
  })
}
test('seller saves nonempty terms against the exact read version; empty terms cannot disable the requirement', async () => {
  const writes: unknown[] = []
  let saved = 0
  api.put = (async (url: string, body: unknown) => {
    writes.push({ url, body })
    return response(terms)
  }) as typeof api.put
  await mount(
    <StoreMerchantTermsForm
      terms={terms}
      onSaved={async () => {
        saved++
      }}
    />
  )
  await textarea('   ')
  const save = [...document.querySelectorAll<HTMLButtonElement>('button')].find(
    (node) => node.textContent === 'Save'
  )
  assert.ok(save)
  assert.equal(save.disabled, true)
  assert.equal(writes.length, 0)
  await textarea(' New refund and delivery terms. ')
  await button('Save')
  assert.deepEqual(writes, [
    {
      url: '/api/store/my/terms',
      body: {
        content: 'New refund and delivery terms.',
        expected_version: terms.version,
      },
    },
  ])
  assert.equal(saved, 1)
})
test('a saved server acceptance does not manufacture a checkbox; a new terms version requires a fresh agreement', async () => {
  let current = terms
  api.get = (async () => response(current)) as typeof api.get
  await mount(<TermsFixture />)
  const order = document.getElementById('terms-order') as HTMLButtonElement
  assert.equal(order.disabled, true)
  await click('store-accept-seller-terms')
  assert.equal(order.disabled, false)
  current = {
    ...terms,
    version: '22222222-2222-2222-2222-222222222222',
    content: 'Updated delivery terms.',
  }
  await click('terms-refresh')
  assert.equal(order.disabled, true)
  assert.match(document.body.textContent || '', /Updated delivery terms/)
  await click('store-accept-seller-terms')
  assert.equal(order.disabled, false)
})
test('unconfigured or unreadable terms block checkout; absent access capability makes no new endpoint request', async () => {
  let reads = 0
  api.get = (async () => {
    reads++
    return response({ ...terms, version: '', configured: false, content: '' })
  }) as typeof api.get
  await mount(<TermsFixture supported={false} />)
  assert.equal(reads, 0)
  assert.equal(document.getElementById('store-accept-seller-terms'), null)
  const mounted = root
  assert.ok(mounted)
  await act(async () => {
    mounted.render(
      <I18nextProvider i18n={i18n}>
        <TermsFixture />
      </I18nextProvider>
    )
    await flush()
  })
  await act(flush)
  assert.equal(
    (document.getElementById('terms-order') as HTMLButtonElement).disabled,
    true
  )
  assert.match(document.body.textContent || '', /Add purchase terms/)
  api.get = (async () => {
    throw new Error('network unavailable')
  }) as typeof api.get
  await click('terms-refresh')
  assert.equal(
    (document.getElementById('terms-order') as HTMLButtonElement).disabled,
    true
  )
})
