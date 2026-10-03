/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
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
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { updateProfileAggregate } = await import('../api')
const { LinkedUsageProfiles, AggregateSourceSummary } =
  await import('./linked-usage-profiles')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const originalAdapter = api.defaults.adapter
afterEach(() => {
  api.defaults.adapter = originalAdapter
  document.body.replaceChildren()
})
after(() => dom.close())

function button(host: HTMLElement, name: string) {
  const found = [...host.querySelectorAll<HTMLButtonElement>('button')].find(
    (element) => element.textContent?.trim() === name
  )
  assert.ok(found, `Missing ${name}`)
  return found
}
async function click(element: HTMLElement) {
  await act(async () => element.click())
}
async function input(element: HTMLInputElement, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(
      dom.HTMLInputElement.prototype,
      'value'
    )?.set?.call(element, value)
    element.dispatchEvent(new Event('input', { bubbles: true }))
    element.dispatchEvent(new Event('change', { bubbles: true }))
  })
}
function mount() {
  const host = document.createElement('div')
  document.body.append(host)
  return { host, root: createRoot(host) }
}

test('live, lifetime snapshot and unavailable sources keep their own periods and unknown metrics', async () => {
  const { host, root } = mount()
  try {
    await act(async () =>
      root.render(
        <I18nextProvider i18n={i18n}>
          <>
            <AggregateSourceSummary
              source={{
                provider: 'cursor',
                url: 'https://cursor.com/@one',
                label: 'Cursor',
                status: 'live',
                source: 'public_ssr',
                tokens: 502_839_373,
                period: 'reported',
                period_start: '2026-09-04',
                period_end: '2026-10-03',
                period_timezone: 'unspecified',
                fetched_at: '2026-10-04T08:15:42Z',
                approximate: false,
              }}
            />
            <AggregateSourceSummary
              source={{
                provider: 'chatgpt',
                url: 'https://chatgpt.com/u/one',
                label: 'Codex',
                status: 'snapshot',
                source: 'owner_snapshot',
                tokens: 117_000_000_000,
                period: 'all',
                observed_at: '2026-10-04T08:15:42Z',
                approximate: true,
                snapshot_source: 'Codex lifetime tokens',
              }}
            />
            <AggregateSourceSummary
              source={{
                provider: 'custom',
                url: 'https://github.com/one',
                label: 'Other',
                status: 'unavailable',
                source: 'owner_snapshot',
                period: 'unknown',
                approximate: false,
              }}
            />
            <AggregateSourceSummary
              source={{
                provider: 'custom',
                url: 'https://github.com/two',
                label: 'Fixed month',
                status: 'snapshot',
                source: 'owner_snapshot',
                tokens: 0,
                period: '30d',
                observed_at: '2026-09-01T08:15:42Z',
                approximate: false,
              }}
            />
          </>
        </I18nextProvider>
      )
    )
    assert.match(host.textContent ?? '', /Automatically read/)
    assert.match(host.textContent ?? '', /502,839,373 Tokens/)
    assert.match(host.textContent ?? '', /2026-09-04 – 2026-10-03/)
    assert.match(host.textContent ?? '', /About 117,000,000,000 Tokens/)
    assert.match(host.textContent ?? '', /All time/)
    assert.match(host.textContent ?? '', /Codex lifetime tokens/)
    assert.match(
      host.textContent ?? '',
      /Last 30 days as observed on 2026-09-01/
    )
    assert.match(host.textContent ?? '', /0 Tokens/)
    const unavailable = host.querySelector('[data-source-status="unavailable"]')
    assert.ok(unavailable)
    assert.equal(unavailable.textContent?.includes('0 Tokens'), false)
    assert.equal(host.textContent?.includes('Requests'), false)
    assert.equal(host.textContent?.includes('Messages'), false)
    assert.equal(host.textContent?.includes('117,502,839,373'), false)
  } finally {
    await act(async () => root.unmount())
  }
})

test('account form validates, keeps failed drafts, disables busy inputs, retries and clears explicitly', async () => {
  const { host, root } = mount()
  const bodies: Record<string, unknown>[] = []
  let fail = true
  let resolvePending: (() => void) | undefined
  api.defaults.adapter = async (config) => {
    assert.equal(config.url, '/api/user/self/profile-share')
    assert.equal(config.method, 'post')
    const body = JSON.parse(config.data) as Record<string, unknown>
    bodies.push(body)
    if (!fail) {
      await new Promise<void>((resolve) => {
        resolvePending = resolve
      })
    }
    return {
      config,
      headers: {},
      status: 200,
      statusText: 'OK',
      data: fail
        ? { success: false, message: 'private upstream detail' }
        : { success: true, data: { enabled: true, ...body } },
    }
  }
  try {
    await act(async () =>
      root.render(
        <I18nextProvider i18n={i18n}>
          <LinkedUsageProfiles
            state={{
              enabled: true,
              aggregate_usage_enabled: true,
              linked_profiles: [],
            }}
            accountName='Current owner'
            onSave={async (settings) => {
              await updateProfileAggregate(settings)
            }}
            onRefresh={() => {}}
            refreshing={false}
            onToggleModelSharing={() => {}}
            modelSharingBusy={false}
            sharingBusy={false}
          />
        </I18nextProvider>
      )
    )
    assert.match(
      host.querySelector('[data-testid="lmm-self-profile"]')?.textContent ?? '',
      /Current owner/
    )
    await click(button(host, 'Add account'))
    await click(button(host, 'Save linked accounts'))
    assert.equal(bodies.length, 0)
    assert.match(
      host.textContent ?? '',
      /Enter a valid public HTTPS profile URL/
    )
    const url = host.querySelector<HTMLInputElement>('input[type="url"]')
    assert.ok(url)
    await input(url, 'https://cursor.com/@one')
    await click(button(host, 'Add account'))
    const urls = host.querySelectorAll<HTMLInputElement>('input[type="url"]')
    assert.ok(urls[1])
    await input(urls[1], 'https://cursor.com/@two')
    await click(button(host, 'Save linked accounts'))
    assert.match(host.textContent ?? '', /Your changes are still here/)
    assert.equal(urls[1].value, 'https://cursor.com/@two')
    assert.equal(host.textContent?.includes('private upstream detail'), false)
    assert.equal(Object.hasOwn(bodies[0] ?? {}, 'model_usage_enabled'), false)
    fail = false
    await click(button(host, 'Save linked accounts'))
    assert.equal(
      host.querySelector<HTMLInputElement>('input[type="url"]')?.disabled,
      true
    )
    assert.equal(button(host, 'Saving…').disabled, true)
    await act(async () => {
      resolvePending?.()
      await new Promise((resolve) => setTimeout(resolve, 10))
    })
    assert.match(host.textContent ?? '', /Linked accounts saved/)
    for (const remove of host.querySelectorAll<HTMLButtonElement>(
      'button[aria-label^="Remove account"]'
    )) {
      await click(remove)
    }
    const pendingSave = click(button(host, 'Save linked accounts'))
    await pendingSave
    await act(async () => {
      resolvePending?.()
      await new Promise((resolve) => setTimeout(resolve, 10))
    })
    assert.deepEqual(bodies.at(-1), {
      aggregate_usage_enabled: true,
      linked_profiles: [],
    })
  } finally {
    await act(async () => root.unmount())
  }
})

test('provider change drops another account snapshot and additions stop at five', async () => {
  const { host, root } = mount()
  try {
    await act(async () =>
      root.render(
        <I18nextProvider i18n={i18n}>
          <LinkedUsageProfiles
            state={{
              enabled: true,
              linked_profiles: [
                {
                  provider: 'chatgpt',
                  url: 'https://chatgpt.com/u/one',
                  snapshot: {
                    tokens: 117_000_000_000,
                    period: 'all',
                    observed_at: '2026-10-04T08:15:42Z',
                    approximate: true,
                  },
                },
              ],
            }}
            onSave={async () => {}}
            onRefresh={() => {}}
            refreshing={false}
            onToggleModelSharing={() => {}}
            modelSharingBusy={false}
            sharingBusy={false}
          />
        </I18nextProvider>
      )
    )
    assert.ok(host.querySelector('input[inputmode="numeric"]'))
    const provider = host.querySelector<HTMLSelectElement>(
      'select[id$="-provider"]'
    )
    assert.ok(provider)
    await act(async () => {
      provider.value = 'cursor'
      provider.dispatchEvent(new Event('change', { bubbles: true }))
    })
    assert.equal(
      host.querySelector<HTMLInputElement>('input[type="url"]')?.value,
      ''
    )
    assert.equal(host.querySelector('input[inputmode="numeric"]'), null)
    for (let i = 0; i < 4; i++) await click(button(host, 'Add account'))
    assert.equal(button(host, 'Add account').disabled, true)
    assert.match(host.textContent ?? '', /5 of 5 accounts/)
  } finally {
    await act(async () => root.unmount())
  }
})
