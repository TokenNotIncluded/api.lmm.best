/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import vm from 'node:vm'

const bootstrap = readFileSync(
  new URL('../public/extore-callback.js', import.meta.url),
  'utf8'
)
const html = readFileSync(new URL('../index.html', import.meta.url), 'utf8')
const tracker = html
  .match(/<script>\s*\/\/ Also fail closed[\s\S]*?<\/script>/)[0]
  .replace(/<\/?script>/g, '')
function page(path, runBootstrap = true) {
  const location = new URL('https://shop.example' + path)
  const elements = []
  const document = {
    createElement: (tag) => ({ tag, dataset: {} }),
    head: { appendChild: (el) => elements.push(el) },
  }
  const history = {
    state: null,
    replaceState: (_s, _t, value) => {
      location.href = new URL(value, location).href
    },
  }
  const window = { location, history }
  const context = vm.createContext({
    window,
    location,
    document,
    URLSearchParams,
  })
  if (runBootstrap) vm.runInContext(bootstrap, context)
  vm.runInContext(tracker, context)
  return { window, elements }
}
test('captures callbacks before trackers, strips history, and blocks external connections', () => {
  for (const query of [
    'code=fixture&state=fixture&iss=fixture',
    'error=access_denied&state=fixture',
    'code=one&code=two',
    'state=only',
  ]) {
    const { window, elements } = page('/store/manage?' + query)
    assert.equal(
      window.__lmmExtoreCallback,
      'https://shop.example/store/manage?' + query
    )
    assert.equal(window.location.href, 'https://shop.example/store/manage')
    assert.equal(
      elements.some((el) => el.tag === 'script'),
      false
    )
    assert.equal(
      elements.find((el) => el.name === 'referrer')?.content,
      'no-referrer'
    )
    assert.match(
      elements.find((el) => el.httpEquiv === 'Content-Security-Policy')
        ?.content,
      /connect-src 'self'/
    )
  }
})
test('ordinary pages keep analytics; a missing bootstrap never enables callback tracking', () => {
  assert.equal(page('/').elements.filter((el) => el.tag === 'script').length, 1)
  assert.equal(
    page('/store/manage?code=fixture', false).elements.filter(
      (el) => el.tag === 'script'
    ).length,
    0
  )
  assert.ok(html.indexOf('/extore-callback.js') < html.indexOf('rel="icon"'))
  assert.doesNotMatch(bootstrap, /localStorage|sessionStorage|fetch\(/)
})
