/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'
import vm from 'node:vm'

import {
  buildTestKeyBookmarklet,
  TEST_KEY_WINDOW_FEATURES,
} from './bookmarklet'
import {
  buildTestKeyBookmarkFile,
  TEST_KEY_BOOKMARK_ICON,
  testKeyBookmarkDownloadUrl,
} from './bookmarklet-file'

function decodeAttribute(value: string): string {
  return value
    .replaceAll('&quot;', '"')
    .replaceAll('&#39;', "'")
    .replaceAll('&lt;', '<')
    .replaceAll('&gt;', '>')
    .replaceAll('&amp;', '&')
}

function importedScript(html: string): string {
  const attribute = html.match(/<A HREF="([^"]+)" ICON=/)?.[1]
  assert.ok(attribute)
  return decodeAttribute(attribute)
}

test('exports one bookmark with the exact popup script, not an ordinary URL', () => {
  const html = buildTestKeyBookmarkFile('https://api.lmm.best')
  assert.ok(html.startsWith('<!DOCTYPE NETSCAPE-Bookmark-file-1>'))
  assert.equal((html.match(/<A /g) ?? []).length, 1)
  assert.equal(
    importedScript(html),
    buildTestKeyBookmarklet('https://api.lmm.best')
  )
  assert.match(html, /charset=UTF-8/)
})

test('imported script requests one isolated popup and does not redirect on null', () => {
  const script = importedScript(buildTestKeyBookmarkFile('https://api.lmm.best'))
  const calls: unknown[][] = []
  const result = vm.runInNewContext(script.slice('javascript:'.length), {
    window: {
      open: (...args: unknown[]) => {
        calls.push(args)
        return null
      },
    },
  })
  assert.equal(result, undefined)
  assert.deepEqual(calls, [
    ['https://api.lmm.best/test-key', '_blank', TEST_KEY_WINDOW_FEATURES],
  ])
  assert.match(TEST_KEY_WINDOW_FEATURES, /noopener,noreferrer/)
})

test('contains a real self-contained 32px PNG for the imported bookmark', () => {
  const html = buildTestKeyBookmarkFile('https://api.lmm.best')
  assert.ok(html.includes(`ICON="${TEST_KEY_BOOKMARK_ICON}"`))
  const png = Buffer.from(TEST_KEY_BOOKMARK_ICON.split(',')[1], 'base64')
  assert.equal(png.subarray(0, 8).toString('hex'), '89504e470d0a1a0a')
  assert.equal(png.readUInt32BE(16), 32)
  assert.equal(png.readUInt32BE(20), 32)
})

test('HTML-escapes the localized title without permitting extra tags or attributes', () => {
  const name = 'LMM "测试" <script>alert(1)</script> & \'key\''
  const html = buildTestKeyBookmarkFile('https://api.lmm.best', name)
  assert.equal(html.includes('<script>'), false)
  assert.equal(html.includes('</script>'), false)
  const title = html.match(/<TITLE>(.*?)<\/TITLE>/)?.[1]
  assert.ok(title)
  assert.equal(decodeAttribute(title), name)
  assert.equal((html.match(/<A /g) ?? []).length, 1)
})

test('never exports paths, query parameters, fragments or account data from origin', () => {
  const html = buildTestKeyBookmarkFile(
    'https://api.lmm.best/account?key=sk-private&session=secret#private-fragment'
  )
  assert.equal(html.includes('sk-private'), false)
  assert.equal(html.includes('session'), false)
  assert.equal(html.includes('private-fragment'), false)
  assert.equal(
    importedScript(html),
    buildTestKeyBookmarklet('https://api.lmm.best')
  )
})

test('rejects credentials and non-web addresses before exporting a file', () => {
  for (const origin of [
    'javascript:alert(1)',
    'data:text/html,hello',
    'https://user:pass@api.lmm.best',
    'file:///test',
  ]) {
    assert.throws(() => buildTestKeyBookmarkFile(origin))
  }
})

test('download URL round-trips the UTF-8 file and localized bookmark name', () => {
  const origin = 'http://localhost:5173'
  const name = 'LMM 测试 Key'
  const url = testKeyBookmarkDownloadUrl(origin, name)
  assert.ok(url.startsWith('data:text/html;charset=utf-8,'))
  assert.equal(
    decodeURIComponent(url.slice(url.indexOf(',') + 1)),
    buildTestKeyBookmarkFile(origin, name)
  )
})
