/* Copyright (C) 2026 LIghtJUNction; licensed under AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
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
  it('keeps the server prompt identical to the frontend component contract', () => {
    const file = new URL(
      '../../../../../api-go/controller/assistant_openui_prompt.txt',
      import.meta.url
    )
    assert.equal(readFileSync(file, 'utf8'), assistantOpenUIPrompt())
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
