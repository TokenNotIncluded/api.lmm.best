/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

import type { Grant, MarketTool } from './api'

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

const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { ToolArgumentsForm } = await import('./schema-form')
const { argumentIssue } = await import('./schema-form-utils')
const { CallDialog } = await import('./tool-actions')
const { marketAPI } = await import('./api')

;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
).IS_REACT_ACT_ENVIRONMENT = true
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const originalInvoke = marketAPI.invoke
const rendered: {
  root: ReturnType<typeof createRoot>
  cache: InstanceType<typeof QueryClient>
}[] = []
const schema = JSON.stringify({
  type: 'object',
  properties: {
    query: { type: 'string', title: 'Query' },
    amount: { type: 'number', title: 'Amount' },
    count: { type: 'integer', title: 'Count' },
    enabled: { type: 'boolean', title: 'Enabled' },
    mode: { type: 'string', title: 'Mode', enum: ['read', 'write'] },
    choice: { type: 'integer', title: 'Choice', enum: [1, 2] },
    options: { type: 'object', title: 'Options' },
    items: { type: 'array', title: 'Items' },
  },
})
const initial =
  '{"query":"initial","amount":2.5,"count":1,"enabled":false,"mode":"write","options":{"nested":[1,2]},"items":[{"id":5}],"extra":{"trace":["keep"]},"__proto__":{"safe":true}}'
const flush = () => new Promise<void>((resolve) => setTimeout(resolve, 15))

function Harness({
  definition,
  initialValue,
  disabled,
}: {
  definition: string
  initialValue: string
  disabled?: boolean
}) {
  const [value, setValue] = useState(initialValue)
  return (
    <>
      <ToolArgumentsForm
        schema={definition}
        value={value}
        onChange={setValue}
        disabled={disabled}
      />
      <output data-testid='arguments'>{value}</output>
    </>
  )
}
function element<T extends Element>(selector: string): T {
  const result = document.body.querySelector<T>(selector)
  assert.ok(result, `Missing element: ${selector}`)
  return result
}
function button(text: string) {
  const result = [...document.body.querySelectorAll('button')].find(
    (item) => item.textContent === text
  )
  assert.ok(result, `Missing button: ${text}`)
  return result
}
function field<T extends HTMLInputElement | HTMLSelectElement>(
  text: string
): T {
  const label = [...document.body.querySelectorAll('label')].find(
    (item) => item.textContent === text || item.firstChild?.textContent === text
  )
  assert.ok(label, `Missing field label: ${text}`)
  const result = document.getElementById(label.htmlFor)
  assert.ok(result, `Missing field: ${text}`)
  return result as T
}
function rawArguments() {
  return (
    element<HTMLOutputElement>('[data-testid="arguments"]').textContent ?? ''
  )
}
function argumentsObject() {
  return JSON.parse(rawArguments()) as Record<string, unknown>
}
async function click(target: HTMLElement) {
  await act(async () => {
    target.click()
    await flush()
  })
}
async function setValue(
  input: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement,
  value: string
) {
  await act(async () => {
    if (input.tagName === 'SELECT') input.value = value
    else {
      const prototype =
        input.tagName === 'TEXTAREA'
          ? dom.HTMLTextAreaElement.prototype
          : dom.HTMLInputElement.prototype
      const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
      assert.ok(setter)
      setter.call(input, value)
    }
    input.dispatchEvent(new Event('input', { bubbles: true }))
    input.dispatchEvent(new Event('change', { bubbles: true }))
    await flush()
  })
}
async function mount(node: React.ReactNode) {
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
        <I18nextProvider i18n={i18n}>{node}</I18nextProvider>
      </QueryClientProvider>
    )
    await flush()
  })
}
async function waitFor(predicate: () => boolean) {
  for (let attempt = 0; attempt < 80; attempt++) {
    if (predicate()) return
    await act(flush)
  }
  assert.fail(
    `Expected schema form state did not arrive: ${document.body.textContent}`
  )
}

afterEach(async () => {
  for (const { root, cache } of rendered.splice(0)) {
    await act(async () => {
      root.unmount()
      await flush()
    })
    cache.clear()
  }
  marketAPI.invoke = originalInvoke
  document.body.replaceChildren()
})
after(() => dom.close())

test('guided editing preserves primitive types and unrelated advanced object properties', async () => {
  await mount(<Harness definition={schema} initialValue={initial} />)
  assert.equal(field<HTMLSelectElement>('Enabled').value, 'false')
  await setValue(field('Query'), 'changed query')
  await setValue(field('Amount'), '6.25')
  await setValue(field('Count'), '3')
  await setValue(field('Mode'), '0')
  await setValue(field('Choice'), '1')
  await setValue(field('Enabled'), 'true')
  await setValue(field('Enabled'), 'false')
  const values = argumentsObject()
  assert.equal(values.query, 'changed query')
  assert.equal(values.amount, 6.25)
  assert.equal(values.count, 3)
  assert.equal(values.mode, 'read')
  assert.equal(values.choice, 2)
  assert.equal(values.enabled, false)
  assert.deepEqual(values.options, { nested: [1, 2] })
  assert.deepEqual(values.items, [{ id: 5 }])
  assert.deepEqual(values.extra, { trace: ['keep'] })
  assert.deepEqual(values.__proto__, { safe: true })
  assert.equal(({} as Record<string, unknown>).safe, undefined)
  await setValue(field('Enabled'), '')
  assert.equal(Object.hasOwn(argumentsObject(), 'enabled'), false)
  assert.equal(field<HTMLSelectElement>('Enabled').value, '')
})

test('advanced JSON keeps unfinished raw input and only returns to guided form for a valid object', async () => {
  await mount(<Harness definition={schema} initialValue={initial} />)
  await click(button('Advanced JSON'))
  const input = element<HTMLTextAreaElement>('textarea')
  assert.equal(input.value, initial)
  const unfinished = '{"query":"unfinished"'
  await setValue(input, unfinished)
  assert.equal(input.value, unfinished)
  assert.equal(rawArguments(), unfinished)
  assert.equal(button('Parameter form').disabled, true)
  await click(button('Parameter form'))
  assert.equal(element<HTMLTextAreaElement>('textarea').value, unfinished)
  await setValue(input, '[]')
  assert.equal(input.value, '[]')
  assert.equal(button('Parameter form').disabled, true)
  await setValue(input, '{"query":"restored","extra":{"remain":true}}')
  assert.equal(button('Parameter form').disabled, false)
  await click(button('Parameter form'))
  assert.equal(document.body.querySelector('textarea'), null)
  assert.equal(field<HTMLInputElement>('Query').value, 'restored')
  assert.deepEqual(argumentsObject().extra, { remain: true })
})

test('advanced exact numbers survive switching to the parameter form and editing another field', async () => {
  await mount(<Harness definition={schema} initialValue='{}' />)
  await click(button('Advanced JSON'))
  const exact =
    '{"query":"original","count":9007199254740993,"amount":1.0000000000000000000000000001,"options":{"items":[9007199254740995]},"extra":{"decimal":0.123456789012345678901}}'
  await setValue(element('textarea'), exact)
  await click(button('Parameter form'))
  assert.equal(document.body.querySelector('textarea'), null)
  const previews = new Set(
    [...document.body.querySelectorAll('pre')].map((item) => item.textContent)
  )
  assert.ok(previews.has('9007199254740993'))
  assert.ok(previews.has('1.0000000000000000000000000001'))
  assert.equal(document.body.querySelectorAll('input').length, 1)
  await setValue(field('Query'), 'changed')
  await click(button('Advanced JSON'))
  const submitted = element<HTMLTextAreaElement>('textarea').value
  assert.match(submitted, /"count": 9007199254740993/)
  assert.match(submitted, /"amount": 1\.0000000000000000000000000001/)
  assert.match(submitted, /"options": \{"items":\[9007199254740995\]\}/)
  assert.match(submitted, /"extra": \{"decimal":0\.123456789012345678901\}/)
  assert.equal(JSON.parse(submitted).query, 'changed')
})

test('reference schemas start in advanced JSON without inventing guided fields', async () => {
  const reference = JSON.stringify({
    $ref: '#/$defs/arguments',
    properties: { query: { type: 'string' } },
  })
  await mount(
    <Harness definition={reference} initialValue='{"query":"preserved"}' />
  )
  assert.equal(
    element<HTMLTextAreaElement>('textarea').value,
    '{"query":"preserved"}'
  )
  assert.equal(document.body.querySelector('input'), null)
  assert.equal(
    [...document.body.querySelectorAll('button')].some(
      (item) => item.textContent === 'Parameter form'
    ),
    false
  )
})

test('complex fields open the existing advanced JSON without discarding nested values', async () => {
  await mount(<Harness definition={schema} initialValue={initial} />)
  assert.ok(
    [...document.body.querySelectorAll('pre')].some((item) =>
      item.textContent?.includes('nested')
    )
  )
  await click(button('Edit in advanced JSON'))
  assert.equal(element<HTMLTextAreaElement>('textarea').value, initial)
  const next = argumentsObject()
  next.options = { nested: ['edited'], more: { enabled: false } }
  await setValue(element('textarea'), JSON.stringify(next))
  await click(button('Parameter form'))
  await setValue(field('Query'), 'only query changes')
  assert.deepEqual(argumentsObject().options, {
    nested: ['edited'],
    more: { enabled: false },
  })
  assert.deepEqual(argumentsObject().items, [{ id: 5 }])
})

test('incomplete numeric drafts stay visible and invalidate arguments instead of reusing a previous number', async () => {
  const numericSchema = JSON.stringify({
    type: 'object',
    properties: {
      amount: { type: 'number', title: 'Amount' },
      count: { type: 'integer', title: 'Count' },
    },
    required: ['amount'],
  })
  await mount(
    <Harness definition={numericSchema} initialValue='{"amount":7,"count":3}' />
  )
  const amount = field<HTMLInputElement>('Amount')
  assert.equal(amount.type, 'text')
  for (const partial of ['-', '1.', '1e']) {
    await setValue(amount, partial)
    assert.equal(amount.value, partial)
    assert.equal(argumentsObject().amount, partial)
    assert.equal(
      argumentIssue(rawArguments(), numericSchema),
      'Invalid parameter: amount'
    )
  }
  await setValue(amount, '-3.5')
  assert.equal(argumentsObject().amount, -3.5)
  assert.equal(argumentIssue(rawArguments(), numericSchema), null)
  await setValue(field('Count'), '1.5')
  assert.equal(
    argumentIssue(rawArguments(), numericSchema),
    'Invalid parameter: count'
  )
  await setValue(field('Count'), '2')
  assert.equal(argumentsObject().count, 2)
  assert.equal(argumentIssue(rawArguments(), numericSchema), null)
  await setValue(amount, '')
  assert.equal(Object.hasOwn(argumentsObject(), 'amount'), false)
  assert.equal(
    argumentIssue(rawArguments(), numericSchema),
    'Missing parameter: amount'
  )
})

test('an invalid optional numeric draft cannot execute a call with an old or silently omitted value', async () => {
  const numericTool: MarketTool = {
    tool_id: 'numeric-tool',
    version_id: 'numeric-version',
    name: 'Numeric tool',
    description: '',
    input_schema: JSON.stringify({
      type: 'object',
      properties: { amount: { type: 'number', title: 'Amount', default: 9 } },
    }),
    output_schema: '',
    permissions: '[]',
    price_quota: 0,
  }
  const numericGrant: Grant = {
    id: 'numeric-grant',
    client_id: 'numeric-client',
    tool_id: numericTool.tool_id,
    version_id: numericTool.version_id,
    max_price_quota: 0,
    max_total_quota: 0,
    max_calls: 2,
    reserved_quota: 0,
    spent_quota: 0,
    successful_calls: 0,
    reserved_calls: 0,
    expires_at: Math.floor(Date.now() / 1000) + 3600,
    revoked_at: 0,
  }
  const inputs: Parameters<typeof marketAPI.invoke>[0][] = []
  marketAPI.invoke = async (input) => {
    inputs.push(structuredClone(input))
    return {
      call: {
        id: 'numeric-call',
        service_id: 'numeric-service',
        tool_id: numericTool.tool_id,
        version_id: numericTool.version_id,
        client_id: numericGrant.client_id,
        execution_status: 'succeeded',
        settlement_status: 'settled',
        price_quota: 0,
        created_at: 100,
        resolve_by: 200,
      },
      result_expired: false,
    }
  }
  await mount(
    <CallDialog
      tool={numericTool}
      grant={numericGrant}
      endpoint='https://provider.example.test/mcp'
      units={500000}
      onClose={() => {}}
    />
  )
  await waitFor(() => document.body.querySelector('[role="dialog"]') !== null)
  const amount = field<HTMLInputElement>('Amount')
  assert.equal(amount.value, '9')
  await setValue(amount, '-')
  await click(button('Run free tool'))
  await waitFor(
    () =>
      document.body.textContent?.includes('Check parameter amount.') === true
  )
  assert.equal(amount.value, '-')
  assert.equal(inputs.length, 0)
  await setValue(amount, '-9.2')
  await click(button('Run free tool'))
  await waitFor(() => inputs.length === 1)
  assert.deepEqual(inputs[0].arguments, { amount: -9.2 })
})

const exactTool: MarketTool = {
  tool_id: 'exact-tool',
  version_id: 'exact-version',
  name: 'Exact numbers tool',
  description: '',
  input_schema: `{
    "type":"object",
    "properties":{
      "count":{"type":"integer","title":"Count","minimum":9007199254740993,"default":9007199254740993},
      "requiredCount":{"type":"integer","title":"Required count","default":9007199254740995},
      "query":{"type":"string","title":"Query","default":"hi"}
    },
    "required":["requiredCount"]
  }`,
  output_schema: '',
  permissions: '[]',
  price_quota: 0,
}
const exactGrant: Grant = {
  id: 'exact-grant',
  client_id: 'exact-client',
  tool_id: exactTool.tool_id,
  version_id: exactTool.version_id,
  max_price_quota: 0,
  max_total_quota: 0,
  max_calls: 2,
  reserved_quota: 0,
  spent_quota: 0,
  successful_calls: 0,
  reserved_calls: 0,
  expires_at: Math.floor(Date.now() / 1000) + 3600,
  revoked_at: 0,
}
async function mountExactCall() {
  const inputs: Parameters<typeof marketAPI.invoke>[0][] = []
  marketAPI.invoke = async (input) => {
    inputs.push(structuredClone(input))
    return {
      call: {
        id: 'exact-call',
        service_id: 'exact-service',
        tool_id: exactTool.tool_id,
        version_id: exactTool.version_id,
        client_id: exactGrant.client_id,
        execution_status: 'succeeded',
        settlement_status: 'settled',
        price_quota: 0,
        created_at: 100,
        resolve_by: 200,
      },
      result_expired: false,
    }
  }
  await mount(
    <CallDialog
      tool={exactTool}
      grant={exactGrant}
      endpoint='https://provider.example.test/mcp'
      units={500000}
      onClose={() => {}}
    />
  )
  await waitFor(() => document.body.querySelector('[role="dialog"]') !== null)
  return inputs
}

test('running untouched optional and required defaults submits their exact numeric JSON', async () => {
  const inputs = await mountExactCall()
  assert.ok(
    [...document.body.querySelectorAll('pre')].some(
      (item) => item.textContent === '9007199254740993'
    )
  )
  assert.equal(field<HTMLInputElement>('Query').value, 'hi')
  await click(button('Run free tool'))
  await waitFor(() => inputs.length === 1)
  assert.match(inputs[0].arguments_json ?? '', /"count": 9007199254740993/)
  assert.match(
    inputs[0].arguments_json ?? '',
    /"requiredCount": 9007199254740995/
  )
})

test('running advanced JSON after editing an unrelated parameter submits the original exact tokens', async () => {
  const inputs = await mountExactCall()
  await click(button('Advanced JSON'))
  await setValue(
    element('textarea'),
    '{"count":9007199254740997,"requiredCount":9007199254740999,"query":"old","extra":{"fraction":0.123456789012345678901}}'
  )
  await click(button('Parameter form'))
  await setValue(field('Query'), 'new')
  await click(button('Run free tool'))
  await waitFor(() => inputs.length === 1)
  assert.match(inputs[0].arguments_json ?? '', /"count": 9007199254740997/)
  assert.match(
    inputs[0].arguments_json ?? '',
    /"requiredCount": 9007199254740999/
  )
  assert.match(
    inputs[0].arguments_json ?? '',
    /"extra": \{"fraction":0\.123456789012345678901\}/
  )
  assert.equal(JSON.parse(inputs[0].arguments_json ?? '{}').query, 'new')
})

test('locked arguments disable guided controls and advanced editing', async () => {
  await mount(<Harness definition={schema} initialValue={initial} disabled />)
  for (const control of document.body.querySelectorAll<
    HTMLInputElement | HTMLSelectElement | HTMLButtonElement
  >('input, select, button')) {
    assert.equal(control.disabled, true)
  }
  assert.equal(rawArguments(), initial)
})
