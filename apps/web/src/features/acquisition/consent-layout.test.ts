/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

import { Window } from 'happy-dom'

const stylesheet = readFileSync(
  new URL('../../styles/console-editorial.css', import.meta.url),
  'utf8'
)
const consentSource = readFileSync(
  new URL('./consent.tsx', import.meta.url),
  'utf8'
)
const consoleSelector =
  "#root:has(> .console-editorial[data-slot='sidebar-wrapper'])"

function renderLayout(console: boolean, expanded: boolean, width = 390) {
  const window = new Window({ width, height: 844 })
  const document = window.document
  const style = document.createElement('style')
  style.textContent = stylesheet
  document.head.append(style)
  const root = document.createElement('div')
  root.id = 'root'
  const content = document.createElement('div')
  if (console) {
    content.className = 'console-editorial'
    content.dataset.slot = 'sidebar-wrapper'
  } else {
    content.className = 'forge-public-shell'
  }
  const privacy = document.createElement(expanded ? 'section' : 'button')
  privacy.dataset.slot = expanded
    ? 'source-consent-panel'
    : 'source-consent-toggle'
  root.append(content, privacy)
  document.body.append(root)
  return { window, root, content, privacy }
}

// These are selector/layout contracts. Browser acceptance verifies actual pixels,
// scrolling, expanded controls and the existing privacy withdrawal interaction.
test('the mobile console and collapsed privacy entry share one bounded viewport', () => {
  assert.match(consentSource, /data-slot='source-consent-toggle'/)
  const rendered = renderLayout(true, false)
  try {
    const { window, root, content, privacy } = rendered
    assert.equal(root.matches(consoleSelector), true)
    const rootStyle = window.getComputedStyle(root)
    assert.equal(rootStyle.display, 'flex')
    assert.equal(rootStyle.flexDirection, 'column')
    // Happy DOM does not calculate dynamic viewport height. Keep the unit
    // declaration as a source contract; the browser verifies its pixel result.
    const rootDeclaration = stylesheet.match(
      /#root:has\(> \.console-editorial\[data-slot='sidebar-wrapper'\]\) \{([^}]+)\}/
    )?.[1]
    assert.ok(rootDeclaration)
    assert.match(rootDeclaration, /height:\s*100dvh;/)
    assert.equal(rootStyle.overflow, 'hidden')
    assert.equal(window.getComputedStyle(content).height, 'auto')
    assert.equal(window.getComputedStyle(content).flexGrow, '1')
    const privacyStyle = window.getComputedStyle(privacy)
    assert.equal(privacyStyle.margin, '0px')
    assert.equal(privacyStyle.minHeight, '44px')
    assert.equal(privacyStyle.flexShrink, '0')
  } finally {
    rendered.window.close()
  }
})

test('expanded console privacy stays scrollable inside its reserved height', () => {
  assert.match(consentSource, /data-slot='source-consent-panel'/)
  const rendered = renderLayout(true, true)
  try {
    const style = rendered.window.getComputedStyle(rendered.privacy)
    assert.equal(style.maxHeight, '50dvh')
    assert.equal(style.overflowY, 'auto')
    assert.equal(style.flexShrink, '0')
  } finally {
    rendered.window.close()
  }
})

test('public pages retain document flow instead of acquiring the fixed console viewport', () => {
  const rendered = renderLayout(false, true)
  try {
    assert.equal(rendered.root.matches(consoleSelector), false)
    const rootStyle = rendered.window.getComputedStyle(rendered.root)
    assert.notEqual(rootStyle.display, 'flex')
    assert.notEqual(rootStyle.height, '100dvh')
    assert.notEqual(
      rendered.window.getComputedStyle(rendered.privacy).maxHeight,
      '50dvh'
    )
  } finally {
    rendered.window.close()
  }
})

test('desktop console privacy retains its existing flow from the 768px boundary', () => {
  for (const width of [768, 1440]) {
    const rendered = renderLayout(true, true, width)
    try {
      const rootStyle = rendered.window.getComputedStyle(rendered.root)
      assert.notEqual(rootStyle.display, 'flex')
      assert.notEqual(rootStyle.overflow, 'hidden')
      const privacyStyle = rendered.window.getComputedStyle(rendered.privacy)
      assert.notEqual(privacyStyle.maxHeight, '50dvh')
      assert.notEqual(privacyStyle.overflowY, 'auto')
    } finally {
      rendered.window.close()
    }
  }
})
