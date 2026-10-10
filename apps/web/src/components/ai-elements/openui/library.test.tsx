/* Copyright (C) 2026 LIghtJUNction; licensed under AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

import { renderToStaticMarkup } from 'react-dom/server'

import { assistantOpenUILibrary, assistantOpenUIPrompt } from './library'
import { OpenUIDataTable } from './table'

// Synthetic fixture values, not production data.
const rows = [
  ['Model A', 12],
  ['Model B', 2],
]

describe('assistant OpenUI component contract', () => {
  it('has a small allowlist with no forms, HTML or server tools', () => {
    assert.deepEqual(Object.keys(assistantOpenUILibrary.components).sort(), [
      'BarChart',
      'ConsoleLink',
      'DataTable',
      'Metric',
      'Stack',
    ])
  })
  it('generates a stable prompt from the current component definitions', () => {
    const prompt = assistantOpenUIPrompt()
    assert.ok(prompt.length > 0)
    assert.equal(prompt, assistantOpenUIPrompt())
    for (const name of Object.keys(assistantOpenUILibrary.components)) {
      assert.ok(prompt.includes(name))
    }
  })
  it('keeps the account, billing and confirmation boundaries in the prompt', () => {
    const prompt = assistantOpenUIPrompt()
    for (const rule of [
      'live tool results',
      'confirmation cards',
      'display-only',
      'ordinary questions in Markdown',
    ]) {
      assert.ok(prompt.includes(rule))
    }
  })
  it('renders a semantic table with keyboard-accessible sort controls', () => {
    const html = renderToStaticMarkup(
      <OpenUIDataTable
        title='Requests'
        columns={['Model', 'Requests']}
        rows={rows}
      />
    )
    assert.ok(html.includes('<caption'))
    assert.ok(html.includes('aria-sort="none"'))
    assert.ok(html.includes('type="button"'))
    assert.ok(html.includes('Model A'))
    assert.deepEqual(rows, [
      ['Model A', 12],
      ['Model B', 2],
    ])
  })
  it('escapes data instead of treating model strings as HTML', () => {
    const html = renderToStaticMarkup(
      <OpenUIDataTable
        title='<script>bad()</script>'
        columns={['Name']}
        rows={[['<img src=x onerror=bad()>']]}
      />
    )
    assert.ok(!html.includes('<script>'))
    assert.ok(!html.includes('<img '))
    assert.ok(html.includes('&lt;script&gt;'))
  })
})
