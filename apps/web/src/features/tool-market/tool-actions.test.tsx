/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, test } from 'node:test'

import { Window } from 'happy-dom'

import type { CallResponse, Grant, MarketTool } from './api'

const dom = new Window({ url: 'https://console.example.test/tool-market' })
Object.defineProperty(dom.document, 'compatMode', { value: 'CSS1Compat' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'HTMLTextAreaElement',
  'HTMLSelectElement',
  'SVGElement',
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

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { marketAPI, MarketAPIError } = await import('./api')
const { CallDialog, GrantDialog, CallResult } = await import('./tool-actions')
const { resetMarketCurrencyTest, useWalletCurrencyPreferenceStore } =
  await import('./currency-test-support')

;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
).IS_REACT_ACT_ENVIRONMENT = true
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const original = {
  grant: marketAPI.grant,
  install: marketAPI.install,
  invoke: marketAPI.invoke,
  result: marketAPI.result,
  report: marketAPI.report,
}
const units = 500000
const endpoint = 'https://provider.example.test/mcp'
const tool: MarketTool = {
  tool_id: 'tool-search',
  version_id: 'version-3',
  name: 'Search provider',
  description: 'Search records',
  input_schema: JSON.stringify({
    type: 'object',
    properties: { q: { type: 'string' } },
    required: ['q'],
  }),
  output_schema: '',
  permissions: JSON.stringify(['network']),
  price_quota: 125000,
}
const grant: Grant = {
  id: 'grant-5',
  client_id: 'cursor-work',
  tool_id: tool.tool_id,
  version_id: tool.version_id,
  max_price_quota: tool.price_quota,
  max_total_quota: 500000,
  max_calls: 4,
  reserved_quota: 0,
  spent_quota: 0,
  successful_calls: 0,
  reserved_calls: 0,
  expires_at: Math.floor(Date.now() / 1000) + 3600,
  revoked_at: 0,
}
type InvokeInput = Parameters<typeof marketAPI.invoke>[0]
const rendered: {
  root: ReturnType<typeof createRoot>
  cache: InstanceType<typeof QueryClient>
}[] = []
const flush = () => new Promise<void>((resolve) => setTimeout(resolve, 15))

function response(
  execution: string,
  settlement: string,
  result?: unknown
): CallResponse {
  return {
    call: {
      id: 'call-original',
      service_id: 'service-search',
      tool_id: tool.tool_id,
      version_id: tool.version_id,
      client_id: grant.client_id,
      execution_status: execution,
      settlement_status: settlement,
      price_quota: tool.price_quota,
      created_at: 100,
      resolve_by: Math.floor(Date.now() / 1000) + 3600,
    },
    result,
    result_expired: false,
  }
}
function awaiting(state: string, message = 'Confirm this provider action.') {
  return response('awaiting_confirmation', 'held', {
    requestState: state,
    inputRequests: {
      confirmation: {
        mode: 'form',
        message,
        schema: {
          type: 'object',
          properties: { confirmed: { type: 'boolean' } },
          required: ['confirmed'],
        },
      },
    },
  })
}
function element<T extends Element>(selector: string): T {
  const result = document.body.querySelector<T>(selector)
  assert.ok(result, `Missing element: ${selector}`)
  return result
}
function findButton(text: string) {
  return [...document.body.querySelectorAll('button')].find(
    (item) => item.textContent === text
  )
}
function button(text: string) {
  const result = findButton(text)
  assert.ok(result, `Missing button: ${text}`)
  return result
}
async function waitFor(predicate: () => boolean) {
  for (let attempt = 0; attempt < 80; attempt++) {
    if (predicate()) return
    await act(flush)
  }
  assert.fail(
    `Expected tool dialog did not become ready: ${document.body.textContent}`
  )
}
async function click(target: HTMLElement) {
  await act(async () => {
    target.click()
    await flush()
  })
}
async function setValue(
  input: HTMLInputElement | HTMLTextAreaElement,
  value: string
) {
  const prototype =
    input.tagName === 'TEXTAREA'
      ? dom.HTMLTextAreaElement.prototype
      : dom.HTMLInputElement.prototype
  const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
  assert.ok(setter)
  await act(async () => {
    setter.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
    input.dispatchEvent(new Event('change', { bubbles: true }))
    await flush()
  })
}
async function mount(kind: 'call' | 'grant', onClose = () => {}) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const cache = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity } },
  })
  rendered.push({ root, cache })
  await act(async () => {
    root.render(
      <QueryClientProvider client={cache}>
        <I18nextProvider i18n={i18n}>
          {kind === 'call' ? (
            <CallDialog
              tool={tool}
              grant={grant}
              endpoint={endpoint}
              units={units}
              onClose={onClose}
            />
          ) : (
            <GrantDialog
              tool={tool}
              clientID={grant.client_id}
              endpoint={endpoint}
              units={units}
              onClose={onClose}
            />
          )}
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flush()
  })
  await waitFor(() => document.body.querySelector('[role="dialog"]') !== null)
}
async function advancedArguments(q = 'original query') {
  await click(button('Advanced JSON'))
  const input = element<HTMLTextAreaElement>('textarea')
  await setValue(input, JSON.stringify({ q }))
  return input
}
const runLabel = 'Run for up to 0.25 USD'

beforeEach(async () => {
  resetMarketCurrencyTest()
  await i18n.changeLanguage('en')
})

afterEach(async () => {
  for (const { root, cache } of rendered.splice(0)) {
    await act(async () => {
      root.unmount()
      await flush()
    })
    cache.clear()
  }
  Object.assign(marketAPI, original)
  document.body.replaceChildren()
})
after(() => dom.close())

test('grant currency and language changes preserve the exact one-credit payload', async () => {
  let payload: Parameters<typeof marketAPI.grant>[0] | undefined
  marketAPI.grant = async (input) => {
    payload = input
    return grant
  }
  marketAPI.install = async () => null
  await mount('grant')
  const input = element<HTMLInputElement>('#grant-total')
  await act(async () => {
    useWalletCurrencyPreferenceStore.getState().setPreference('CREDIT')
  })
  assert.equal(input.value, String(tool.price_quota))
  assert.equal(input.step, '1')
  await setValue(input, '1')
  await act(async () => {
    useWalletCurrencyPreferenceStore.getState().setPreference('USD')
  })
  assert.equal(input.value, '0.000002')
  assert.equal(input.step, 'any')
  await act(async () => {
    useWalletCurrencyPreferenceStore.getState().setPreference('CNY')
    await i18n.changeLanguage('zh-CN')
  })
  assert.equal(input.value, '0.000014')
  await act(async () => {
    await i18n.changeLanguage('en')
  })
  assert.equal(input.value, '0.000014')
  assert.match(document.body.textContent ?? '', /Total spending limit \(CNY\)/)
  await click(button('Add and authorize tool'))
  await waitFor(() => payload !== undefined)
  assert.equal(payload?.max_total_quota, 1)
  assert.equal(payload?.max_price_quota, tool.price_quota)
})

test('invalid grant drafts stay blocked after a currency switch', async () => {
  let requests = 0
  marketAPI.grant = async () => {
    requests++
    return grant
  }
  await mount('grant')
  const input = element<HTMLInputElement>('#grant-total')
  await setValue(input, '0.000000000000000000000000000001')
  assert.equal(button('Add and authorize tool').disabled, true)
  await act(async () => {
    useWalletCurrencyPreferenceStore.getState().setPreference('CREDIT')
  })
  assert.equal(input.value, '')
  assert.equal(button('Add and authorize tool').disabled, true)
  assert.equal(requests, 0)
})

test('adding a tool requires explicit confirmation and grants before loading the exact client version', async () => {
  const operations: { kind: string; input: unknown }[] = []
  let closed = false
  marketAPI.grant = async (input) => {
    operations.push({ kind: 'grant', input })
    return grant
  }
  marketAPI.install = async (input, loaded) => {
    operations.push({ kind: 'install', input: { ...input, loaded } })
    return null
  }
  await mount('grant', () => {
    closed = true
  })
  assert.equal(operations.length, 0)
  assert.equal(closed, false)
  assert.match(document.body.textContent ?? '', /cursor-work/)
  assert.match(document.body.textContent ?? '', /provider\.example\.test/)
  await click(button('Add and authorize tool'))
  await waitFor(() => closed)
  assert.deepEqual(
    operations.map((item) => item.kind),
    ['grant', 'install']
  )
  const limits = operations[0].input as Parameters<typeof marketAPI.grant>[0]
  assert.equal(limits.client_id, grant.client_id)
  assert.equal(limits.tool_id, tool.tool_id)
  assert.equal(limits.version_id, tool.version_id)
  assert.equal(limits.max_price_quota, tool.price_quota)
  assert.equal(limits.max_total_quota, tool.price_quota)
  assert.equal(limits.max_calls, 1)
  assert.ok(limits.expires_at > Date.now() / 1000)
  assert.deepEqual(operations[1].input, {
    client_id: grant.client_id,
    tool_id: tool.tool_id,
    version_id: tool.version_id,
    loaded: true,
  })
})

test('a rejected grant never loads the tool, and failed loading keeps the saved authorization visible', async () => {
  let installed = 0
  let granted = 0
  let closed = false
  marketAPI.grant = async () => {
    granted++
    throw new MarketAPIError('TOOL_MARKET_BUDGET')
  }
  marketAPI.install = async () => {
    installed++
    throw new Error('transport')
  }
  await mount('grant', () => {
    closed = true
  })
  await click(button('Add and authorize tool'))
  await waitFor(
    () => document.body.textContent?.includes('no remaining allowance') === true
  )
  assert.equal(installed, 0)
  assert.equal(closed, false)
  marketAPI.grant = async () => {
    granted++
    return grant
  }
  await click(button('Add and authorize tool'))
  await waitFor(
    () =>
      document.body.textContent?.includes(
        'Authorization was saved, but loading failed'
      ) === true
  )
  assert.equal(granted, 2)
  assert.equal(installed, 1)
  assert.equal(closed, false)
})

test('required schema parameters are checked before a provider call starts', async () => {
  let invoked = 0
  marketAPI.invoke = async () => {
    invoked++
    return response('succeeded', 'settled')
  }
  await mount('call')
  await click(button(runLabel))
  await waitFor(
    () => document.body.textContent?.includes('Check parameter q.') === true
  )
  assert.equal(invoked, 0)
  assert.equal(element<HTMLInputElement>('input[type="text"]').disabled, false)
})

test('confirmation rounds reuse the original request and arguments and require fresh explicit acceptance', async () => {
  const inputs: InvokeInput[] = []
  marketAPI.invoke = async (input) => {
    inputs.push(structuredClone(input))
    if (inputs.length === 1) return awaiting('state-first')
    if (inputs.length === 2) {
      return awaiting('state-next', 'Confirm the second action.')
    }
    return response('succeeded', 'settled', {
      content: [{ type: 'text', text: 'Finished.' }],
    })
  }
  await mount('call')
  const args = await advancedArguments()
  await click(button(runLabel))
  await waitFor(() => findButton('Confirm and continue') !== undefined)
  assert.equal(args.disabled, true)
  assert.equal(button('Confirm and continue').disabled, true)
  assert.equal(inputs.length, 1)
  assert.equal(findButton('Start another call'), undefined)
  await click(button('Confirm and continue'))
  assert.equal(inputs.length, 1)
  await click(element<HTMLInputElement>('input[type="checkbox"]'))
  assert.equal(button('Confirm and continue').disabled, false)
  await click(button('Confirm and continue'))
  await waitFor(
    () =>
      document.body.textContent?.includes('Confirm the second action.') === true
  )
  assert.equal(button('Confirm and continue').disabled, true)
  assert.equal(
    element('[role="checkbox"]').getAttribute('aria-checked'),
    'false'
  )
  assert.equal(inputs[1].request_id, inputs[0].request_id)
  assert.deepEqual(inputs[1].arguments, inputs[0].arguments)
  assert.equal(inputs[1].request_state, 'state-first')
  assert.deepEqual(inputs[1].input_responses, {
    confirmation: { action: 'accept', content: { confirmed: true } },
  })
  await click(element<HTMLInputElement>('input[type="checkbox"]'))
  await click(button('Confirm and continue'))
  await waitFor(() => document.body.textContent?.includes('Finished.') === true)
  assert.equal(inputs[2].request_id, inputs[0].request_id)
  assert.deepEqual(inputs[2].arguments, inputs[0].arguments)
  assert.equal(inputs[2].request_state, 'state-next')
  assert.deepEqual(inputs[2].input_responses, {
    confirmation: { action: 'accept', content: { confirmed: true } },
  })
  assert.ok(findButton('Start another call'))
})

test('cancelling a pending confirmation releases the same request without an acceptance payload', async () => {
  const inputs: InvokeInput[] = []
  marketAPI.invoke = async (input) => {
    inputs.push(structuredClone(input))
    return inputs.length === 1
      ? awaiting('state-cancel')
      : response('cancelled', 'released')
  }
  await mount('call')
  await advancedArguments()
  await click(button(runLabel))
  await waitFor(() => findButton('Cancel action') !== undefined)
  await click(button('Cancel action'))
  await waitFor(() => findButton('Start another call') !== undefined)
  assert.equal(inputs.length, 2)
  assert.equal(inputs[1].request_id, inputs[0].request_id)
  assert.deepEqual(inputs[1].arguments, inputs[0].arguments)
  assert.equal(inputs[1].request_state, 'state-cancel')
  assert.deepEqual(inputs[1].input_responses, {
    confirmation: { action: 'cancel' },
  })
  assert.equal(findButton('Confirm and continue'), undefined)
})

test('a transport error freezes the original JSON and retry identity; an unknown held result cannot start another call', async () => {
  const inputs: InvokeInput[] = []
  const refreshed: string[] = []
  const unknown = {
    ...response('unknown', 'held'),
    error_code: 'TOOL_MARKET_RESULT_UNKNOWN',
  }
  marketAPI.invoke = async (input) => {
    inputs.push(structuredClone(input))
    if (inputs.length === 1) throw new Error('connection lost after send')
    return unknown
  }
  marketAPI.result = async (id) => {
    refreshed.push(id)
    return unknown
  }
  await mount('call')
  const args = await advancedArguments('do not change this request')
  await click(button(runLabel))
  await waitFor(() => findButton('Retry same request') !== undefined)
  assert.equal(args.disabled, true)
  assert.equal(button('Advanced JSON').disabled, true)
  assert.equal(findButton('Start another call'), undefined)
  await click(button('Retry same request'))
  await waitFor(() => findButton('Refresh result') !== undefined)
  assert.equal(inputs[1].request_id, inputs[0].request_id)
  assert.deepEqual(inputs[1].arguments, { q: 'do not change this request' })
  assert.deepEqual(inputs[1].arguments, inputs[0].arguments)
  assert.equal(findButton('Start another call'), undefined)
  assert.match(document.body.textContent ?? '', /The result is unknown/)
  await click(button('Refresh result'))
  assert.deepEqual(refreshed, ['call-original'])
  assert.equal(inputs.length, 2)
  assert.equal(findButton('Start another call'), undefined)
})

test('a known arguments rejection unlocks the form and a correction gets a new request ID', async () => {
  const inputs: InvokeInput[] = []
  marketAPI.invoke = async (input) => {
    inputs.push(structuredClone(input))
    if (inputs.length === 1) throw new MarketAPIError('TOOL_MARKET_ARGUMENTS')
    return response('succeeded', 'settled', {
      content: [
        {
          type: 'text',
          text: 'Search complete. <script>literal text</script>',
        },
      ],
      structuredContent: { results: [{ title: 'Matched record', count: 3 }] },
    })
  }
  await mount('call')
  const args = await advancedArguments('bad value')
  await click(button(runLabel))
  await waitFor(
    () =>
      document.body.textContent?.includes('Check the tool parameters') === true
  )
  assert.equal(args.disabled, false)
  assert.equal(findButton('Retry same request'), undefined)
  await setValue(args, JSON.stringify({ q: 'corrected value' }))
  await click(button(runLabel))
  await waitFor(
    () => document.body.textContent?.includes('Search complete.') === true
  )
  assert.notEqual(inputs[1].request_id, inputs[0].request_id)
  assert.deepEqual(inputs[1].arguments, { q: 'corrected value' })
  const paragraphs = [...document.body.querySelectorAll('p')]
  assert.ok(
    paragraphs.some(
      (item) =>
        item.textContent === 'Search complete. <script>literal text</script>'
    )
  )
  assert.equal(document.body.querySelector('script'), null)
  const structured = [...document.body.querySelectorAll('pre')].find((item) =>
    item.textContent?.includes('Matched record')
  )
  assert.ok(structured)
  assert.deepEqual(JSON.parse(structured.textContent ?? ''), {
    results: [{ title: 'Matched record', count: 3 }],
  })
})

test('large native and structured drawing images use downloadable blobs and keep base64 out of result text', async (context) => {
  const createURL = URL.createObjectURL
  const revokeURL = URL.revokeObjectURL
  const blobs: Blob[] = []
  const revoked: string[] = []
  URL.createObjectURL = (blob) => {
    blobs.push(blob as Blob)
    return `blob:market-image-${blobs.length}`
  }
  URL.revokeObjectURL = (url) => {
    revoked.push(url)
  }
  context.after(() => {
    URL.createObjectURL = createURL
    URL.revokeObjectURL = revokeURL
  })
  const encoded = Buffer.concat([
    Buffer.from('\x89PNG\r\n\x1a\n', 'binary'),
    Buffer.alloc(3 * 1024 * 1024),
  ]).toString('base64')
  const value = {
    content: [{ type: 'image', mimeType: 'image/png', data: encoded }],
    structuredContent: {
      message: 'Image generation completed.',
      data: {
        created: 123,
        data: { b64_json: encoded, revised_prompt: 'A generated image' },
        usage: { total_tokens: 10 },
      },
    },
  }
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const cache = new QueryClient()
  rendered.push({ root, cache })
  await act(async () => {
    root.render(
      <QueryClientProvider client={cache}>
        <I18nextProvider i18n={i18n}>
          <CallResult
            response={response('succeeded', 'settled', value)}
            units={units}
          />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flush()
  })
  assert.equal(document.body.querySelectorAll('img').length, 2)
  assert.equal(document.body.querySelectorAll('a[download]').length, 2)
  assert.equal(blobs.length, 2)
  assert.equal(blobs[0].type, 'image/png')
  assert.ok(blobs[0].size > 3 * 1024 * 1024)
  assert.equal(document.body.textContent?.includes(encoded), false)
  assert.match(document.body.textContent ?? '', /A generated image/)
  assert.match(document.body.textContent ?? '', /total_tokens/)
  for (const image of document.body.querySelectorAll('img')) {
    assert.match(image.src, /^blob:/)
  }
  for (const download of document.body.querySelectorAll('a[download]')) {
    assert.match(download.getAttribute('href') ?? '', /^blob:/)
  }
  await act(async () => {
    root.unmount()
    await flush()
  })
  rendered.pop()
  cache.clear()
  assert.equal(
    revoked.length,
    2,
    'all retained image blobs are released when the result leaves the screen'
  )
})

test('a tool-reported bill can be reported from an expired result without repeating the tool call', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const cache = new QueryClient()
  rendered.push({ root, cache })
  const reported: { id: string; reason: string }[] = []
  marketAPI.report = async (id, reason) => {
    reported.push({ id, reason })
    return {
      call_id: id,
      user_id: 1,
      service_id: 'service-search',
      owner_id: 2,
      reason,
      evidence: '{}',
      status: 'pending',
      review_note: '',
      reviewed_by: 0,
      created_at: 100,
      reviewed_at: 0,
    }
  }
  marketAPI.invoke = async () => {
    assert.fail('Reporting must never execute a tool')
  }
  const bill = response('succeeded', 'settled')
  bill.result_expired = true
  bill.call.usage_source = 'tool_reported'
  bill.call.usage_quantities = { input_tokens: 25 }
  await act(async () => {
    root.render(
      <QueryClientProvider client={cache}>
        <I18nextProvider i18n={i18n}>
          <CallResult response={bill} />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flush()
  })
  assert.match(document.body.textContent ?? '', /Tool-reported usage/)
  await click(button('Report this bill'))
  await setValue(
    element<HTMLTextAreaElement>('textarea'),
    'The reported usage does not match my input.'
  )
  await click(button('Submit report'))
  await waitFor(
    () =>
      document.body.textContent?.includes('Report submitted for review.') ===
      true
  )
  assert.deepEqual(reported, [
    { id: bill.call.id, reason: 'The reported usage does not match my input.' },
  ])
})
