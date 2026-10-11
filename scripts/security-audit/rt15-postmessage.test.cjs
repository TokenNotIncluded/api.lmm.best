// Source-handler unit tests only. No browser, session issuance, binding API, or DB.
// Uses the checkout's handlers instead of reimplementing their decision logic.
const assert = require('node:assert/strict')
const { readFileSync } = require('node:fs')
const path = require('node:path')
const { createRequire } = require('node:module')
const { test } = require('node:test')
const root = path.resolve(process.env.RT15_SOURCE_ROOT || path.join(__dirname, '../..'))
let ts
try {
  ts = createRequire(path.join(root, 'apps/web/package.json'))('typescript')
} catch {
  ts = require('typescript')
}
const account = 'apps/web/src/features/profile/components/tabs/account-bindings-tab.tsx'
const telegram = 'apps/web/src/features/profile/components/dialogs/telegram-bind-dialog.tsx'
function loadHandler(file, name, scope) {
  const source = ts.createSourceFile(file, readFileSync(path.join(root, file), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  const matches = []
  function visit(node) {
    if (ts.isVariableDeclaration(node) && node.name.getText(source) === name) {
      let value = node.initializer
      if (value && ts.isCallExpression(value) && value.expression.getText(source) === 'useCallback') value = value.arguments[0]
      assert.ok(value && ts.isArrowFunction(value), `Unsupported handler shape: ${name}`)
      matches.push(value.getText(source))
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
  assert.equal(matches.length, 1, `Expected one real ${name} handler`)
  const compiled = ts.transpileModule(`module.exports = (${matches[0]})`, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
    reportDiagnostics: true,
  })
  assert.equal((compiled.diagnostics || []).filter(d => d.category === ts.DiagnosticCategory.Error).length, 0)
  const module = { exports: undefined }
  new Function('module', ...Object.keys(scope), compiled.outputText)(module, ...Object.values(scope))
  return module.exports
}
function accountFixture(response = Promise.resolve({ data: { success: true } })) {
  const requests = [], replies = [], successes = []
  const popup = { closed: false, postMessage: (...args) => replies.push(args) }
  const pending = { provider: 'fixture-provider', state: 'fixture-flow', popup, stopCloseWatcher() {} }
  const pendingOAuthBinding = { current: pending }
  const clearPendingOAuthBinding = loadHandler(account, 'clearPendingOAuthBinding', { pendingOAuthBinding })
  const handler = loadHandler(account, 'handleMessage', {
    window: { location: { origin: 'https://panel.test' } },
    pendingOAuthBinding, clearPendingOAuthBinding,
    OAUTH_BIND_CALLBACK_MESSAGE: 'fixture-callback', OAUTH_BIND_RESULT_MESSAGE: 'fixture-result',
    t: x => x, toast: { success: x => successes.push(x), error() {} },
    onUpdate() {}, fetchCustomBindings: async () => {},
    api: { get: (...args) => { requests.push(args); return response } },
  })
  const event = { origin: 'https://panel.test', source: popup,
    data: { type: 'fixture-callback', provider: pending.provider, state: pending.state, code: 'fixture-code' } }
  return { handler, event, pendingOAuthBinding, requests, replies, successes }
}
for (const [name, mutate] of [
  ['foreign origin', e => { e.origin = 'https://foreign.test' }],
  ['same-site child origin', e => { e.origin = 'https://child.panel.test' }],
  ['opaque origin', e => { e.origin = 'null' }],
  ['different window', e => { e.source = {} }],
  ['null window', e => { e.source = null }],
  ['old state', e => { e.data.state = 'old-flow' }],
  ['different provider', e => { e.data.provider = 'other-provider' }],
  ['wrong type', e => { e.data.type = 'other-message' }],
  ['null message', e => { e.data = null }],
]) {
  test(`OAuth callback rejects ${name} before requesting a bind`, async () => {
    const f = accountFixture(); mutate(f.event); await f.handler(f.event)
    assert.equal(f.requests.length, 0); assert.equal(f.replies.length, 0)
    assert.notEqual(f.pendingOAuthBinding.current, null)
  })
}
test('valid OAuth callback requests binding once and replies to the exact origin', async () => {
  const f = accountFixture(); await f.handler(f.event); await f.handler(f.event)
  assert.equal(f.requests.length, 1)
  assert.equal(f.requests[0][0], '/api/oauth/fixture-provider')
  assert.equal(f.requests[0][1].params.state, 'fixture-flow')
  assert.equal(f.replies.length, 1); assert.equal(f.replies[0][1], 'https://panel.test')
  assert.equal(f.pendingOAuthBinding.current, null)
})
test('concurrent callback replay is consumed before the first API await', async () => {
  let resolve
  const response = new Promise(r => { resolve = r })
  const f = accountFixture(response)
  const first = f.handler(f.event)
  await f.handler(f.event)
  assert.equal(f.requests.length, 1)
  resolve({ data: { success: true } }); await first
  assert.equal(f.replies.length, 1)
})
test('no pending binding cannot request a bind', async () => {
  const f = accountFixture(); f.pendingOAuthBinding.current = null
  await f.handler(f.event); assert.equal(f.requests.length, 0)
})
for (const [name, success] of [['backend rejection', false], ['backend success', true]]) {
  test(`callback UI follows ${name}, not the message alone`, async () => {
    const f = accountFixture(Promise.resolve({ data: { success } }))
    await f.handler(f.event)
    assert.equal(f.successes.length, success ? 1 : 0)
    assert.equal(f.replies[0][0].success, success)
  })
}
for (const [name, mutate] of [
  ['foreign origin', e => { e.origin = 'https://foreign.test' }],
  ['same-site child origin', e => { e.origin = 'https://child.panel.test' }],
  ['old flow', e => { e.data.flow_token = 'old-flow' }],
]) {
  test(`Telegram result rejects ${name}`, () => {
    let updates = 0
    const handler = loadHandler(telegram, 'handleBindResult', {
      window: { location: { origin: 'https://panel.test' } }, flowToken: 'fixture-flow',
      TELEGRAM_BIND_RESULT_MESSAGE: 'fixture-telegram-result',
      t: x => x, toast: { success() {} }, getServerErrorMessageKey() {}, setError() {},
      onSuccess() { updates++ }, onOpenChange() {},
    })
    const event = { origin: 'https://panel.test', source: {}, data: {
      type: 'fixture-telegram-result', flow_token: 'fixture-flow', success: true,
    } }
    mutate(event); handler(event); assert.equal(updates, 0)
  })
}
