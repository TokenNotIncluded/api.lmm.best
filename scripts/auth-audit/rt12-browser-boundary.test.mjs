// RT-12: production frontend functions, not a browser or database acceptance test.
// Run with local dependencies only. No HTTP request or provider is contacted.
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import vm from 'node:vm'
import {
  getOAuthSessionStorage,
  markOAuthBindPopup,
  resolveOAuthCallbackMode,
} from '../../apps/web/src/features/auth/lib/oauth-callback-mode.ts'

const require = createRequire(new URL('../../apps/web/package.json', import.meta.url))
const ts = require('typescript')
const files = {
  component: '../../apps/web/src/features/profile/components/tabs/account-bindings-tab.tsx',
  constants: '../../apps/web/src/features/auth/constants.ts',
  mode: '../../apps/web/src/features/auth/lib/oauth-callback-mode.ts',
}
function readSource(relative) {
  const file = fileURLToPath(new URL(relative, import.meta.url))
  const bytes = readFileSync(file)
  const blob = createHash('sha1').update(`blob ${bytes.length}\0`).update(bytes).digest('hex')
  console.log(`# source ${relative}: git-blob=${blob}`)
  const source = ts.createSourceFile(file, bytes.toString('utf8'), ts.ScriptTarget.Latest, true)
  assert.equal(source.parseDiagnostics.length, 0, `Source parse failed: ${file}`)
  return source
}
const component = readSource(files.component)
const constants = readSource(files.constants)
readSource(files.mode)
function declaration(source, name) {
  const matches = []
  function visit(node) {
    if (ts.isVariableDeclaration(node) && node.name.getText(source) === name) matches.push(node)
    ts.forEachChild(node, visit)
  }
  visit(source)
  assert.equal(matches.length, 1, `Expected exactly one production declaration: ${name}`)
  assert.ok(matches[0].initializer, `Missing initializer: ${name}`)
  return matches[0].initializer
}
function constant(name) {
  const node = declaration(constants, name)
  assert.ok(ts.isStringLiteral(node), `${name} is no longer a string literal; review the harness`)
  return node.text
}
const callbackType = constant('OAUTH_BIND_CALLBACK_MESSAGE')
const resultType = constant('OAUTH_BIND_RESULT_MESSAGE')
const clearCall = declaration(component, 'clearPendingOAuthBinding')
assert.ok(ts.isCallExpression(clearCall) && clearCall.expression.getText(component) === 'useCallback')
assert.ok(ts.isArrowFunction(clearCall.arguments[0]))
const handler = declaration(component, 'handleMessage')
assert.ok(ts.isArrowFunction(handler))
// Extract unchanged production function bodies. Fail on structure drift; never
// substitute a copied predicate, a synthetic backend, or an automatic pass.
const compiled = ts.transpileModule(`
const clearPendingOAuthBinding = ${clearCall.arguments[0].getText(component)};
globalThis.handle = ${handler.getText(component)};
`, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None }, reportDiagnostics: true })
assert.equal((compiled.diagnostics ?? []).filter(d => d.category === ts.DiagnosticCategory.Error).length, 0)

function memoryStorage() {
  const values = new Map()
  return { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value) }
}
function fixture(reply = async () => ({ data: { success: false, message: 'local rejection' } })) {
  const effects = { calls: [], replies: [], refreshes: 0, reloads: 0, notices: [], stopped: 0 }
  const popup = { closed: false, postMessage: (message, origin) => effects.replies.push({ message, origin }) }
  const pending = { provider: 'oidc', state: 'synthetic-flow-a', popup, stopCloseWatcher: () => effects.stopped++ }
  const ref = { current: pending }
  const context = vm.createContext({
    Error,
    window: { location: { origin: 'https://rt12.invalid' } },
    pendingOAuthBinding: ref,
    OAUTH_BIND_CALLBACK_MESSAGE: callbackType,
    OAUTH_BIND_RESULT_MESSAGE: resultType,
    api: { get: async (...args) => { effects.calls.push(args); return reply(...args) } },
    t: text => text,
    toast: { success: text => effects.notices.push(['success', text]), error: text => effects.notices.push(['error', text]) },
    onUpdate: () => effects.refreshes++,
    fetchCustomBindings: async () => { effects.reloads++ },
  })
  vm.runInContext(compiled.outputText, context, { timeout: 1000 })
  const event = { origin: 'https://rt12.invalid', source: popup, data: { type: callbackType, provider: pending.provider, state: pending.state, code: 'local-code' } }
  return { handle: context.handle, ref, pending, effects, event }
}

test('RT12 callback mode requires the matching provider/state stamp and live opener', () => {
  const storage = memoryStorage()
  const opener = { closed: false }
  assert.equal(resolveOAuthCallbackMode('oidc', 'a', { opener, storage }), 'login')
  assert.equal(markOAuthBindPopup(storage, 'oidc', 'a'), true)
  assert.equal(resolveOAuthCallbackMode('oidc', 'a', { opener, storage }), 'bind')
  assert.equal(resolveOAuthCallbackMode('oidc', 'b', { opener, storage }), 'login')
  assert.equal(resolveOAuthCallbackMode('github', 'a', { opener, storage }), 'login')
  assert.equal(resolveOAuthCallbackMode('oidc', 'a', { opener: { closed: true }, storage }), 'login')
  assert.equal(resolveOAuthCallbackMode('oidc', 'a', { opener: null, storage }), 'login')
  assert.equal(markOAuthBindPopup(storage, 'oidc', 'b'), true)
  assert.equal(resolveOAuthCallbackMode('oidc', 'a', { opener, storage }), 'login')
  assert.equal(resolveOAuthCallbackMode('oidc', 'b', { opener, storage }), 'bind')
  // The stamp selects presentation only; these assertions grant no server authority.
})

test('RT12 storage denial fails closed without bypassing browser policy', () => {
  const owner = { get sessionStorage() { throw new Error('denied') } }
  assert.equal(getOAuthSessionStorage(owner), null)
  const denied = { getItem() { throw new Error('denied') }, setItem() { throw new Error('denied') } }
  assert.equal(markOAuthBindPopup(denied, 'oidc', 'a'), false)
  assert.equal(resolveOAuthCallbackMode('oidc', 'a', { opener: { closed: false }, storage: denied }), 'login')
})

test('RT12 mismatched browser messages never invoke the binding API or consume the pending action', async t => {
  const changes = {
    origin: f => { f.event.origin = 'https://other.invalid' },
    window: f => { f.event.source = {} },
    provider: f => { f.event.data.provider = 'github' },
    state: f => { f.event.data.state = 'synthetic-flow-b' },
    type: f => { f.event.data.type = resultType },
    empty: f => { f.event.data = null },
  }
  for (const [name, change] of Object.entries(changes)) {
    await t.test(name, async () => {
      const f = fixture()
      change(f)
      await f.handle(f.event)
      assert.equal(f.ref.current, f.pending)
      assert.equal(f.effects.calls.length, 0)
      assert.equal(f.effects.replies.length, 0)
      assert.equal(f.effects.refreshes, 0)
      assert.equal(f.effects.stopped, 0)
    })
  }
})

test('RT12 success-looking message cannot override a backend rejection', async () => {
  const f = fixture()
  f.event.data.success = true
  f.event.data.access_token = 'not-a-token'
  f.event.data.user = { id: 999 }
  await f.handle(f.event)
  assert.equal(f.effects.calls.length, 1)
  assert.equal(f.effects.calls[0][0], '/api/oauth/oidc')
  assert.deepEqual(Object.keys(f.effects.calls[0][1].params).sort(), ['code', 'state'])
  assert.equal(f.ref.current, null)
  assert.equal(f.effects.refreshes, 0)
  assert.equal(f.effects.reloads, 0)
  assert.equal(f.effects.replies[0].message.success, false)
  assert.equal(f.effects.replies[0].origin, 'https://rt12.invalid')
})

test('RT12 duplicate callbacks invoke the API once, including while it is unresolved', async () => {
  let complete
  const f = fixture(() => new Promise(resolve => { complete = resolve }))
  const first = f.handle(f.event)
  assert.equal(f.ref.current, null, 'pending action must be removed before the await')
  await Promise.all(Array.from({ length: 8 }, () => f.handle(f.event)))
  assert.equal(f.effects.calls.length, 1)
  assert.equal(f.effects.refreshes, 0)
  complete({ data: { success: true } })
  await first
  assert.equal(f.effects.refreshes, 1, 'positive control requires the backend result')
  assert.equal(f.effects.reloads, 1)
  assert.equal(f.effects.replies.length, 1)
  assert.equal(f.effects.replies[0].message.success, true)
  assert.equal(f.effects.stopped, 1)
})

test('RT12 a late callback cannot consume a newly started binding', async () => {
  const f = fixture()
  const next = { ...f.pending, state: 'synthetic-flow-b', popup: { postMessage() {} } }
  f.ref.current = next
  await f.handle(f.event)
  assert.equal(f.ref.current, next)
  assert.equal(f.effects.calls.length, 0)
})

test('RT12 missing authorization code does not reach the backend', async () => {
  const f = fixture()
  delete f.event.data.code
  f.event.data.success = true
  await f.handle(f.event)
  assert.equal(f.effects.calls.length, 0)
  assert.equal(f.effects.refreshes, 0)
  assert.equal(f.effects.replies[0].message.success, false)
})
