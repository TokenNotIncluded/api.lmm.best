/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  chartDomain,
  parseAssistantVisualization,
} from './assistant-visualization-data'

const chart = {
  kind: 'chart',
  title: 'Requests',
  chart_type: 'line',
  labels: ['Mon', 'Tue'],
  series: [{ name: 'Calls', values: [0, 12] }],
}
test('chart contract rejects nonfinite, mismatched and negative donut data', () => {
  assert.ok(parseAssistantVisualization('show_chart', chart))
  for (const values of [[1], [1, Infinity], [1, Number.NaN], [1, 1e20]]) {
    assert.equal(
      parseAssistantVisualization('show_chart', {
        ...chart,
        series: [{ name: 'x', values }],
      }),
      undefined
    )
  }
  assert.equal(
    parseAssistantVisualization('show_chart', {
      ...chart,
      chart_type: 'donut',
      series: [{ name: 'x', values: [-1, 2] }],
    }),
    undefined
  )
  assert.deepEqual(chartDomain([{ values: [0, 0] }]), [0, 1])
  assert.deepEqual(chartDomain([{ values: [-3, 4] }]), [-3, 4])
})
test('choices are data, never executable actions or renderer options', () => {
  const visual = parseAssistantVisualization('show_choices', {
    kind: 'choices',
    title: 'Choose',
    items: [{ label: 'Seven days', value: 'Show last seven days' }],
    dangerouslySetInnerHTML: { __html: '<script>bad()</script>' },
  })
  assert.ok(visual)
  assert.equal('dangerouslySetInnerHTML' in visual, false)
  assert.equal(visual.items?.[0].value, 'Show last seven days')
  assert.equal(
    parseAssistantVisualization('show_choices', {
      kind: 'choices',
      title: 'Bad',
      items: Array.from({ length: 9 }, () => ({ label: 'x', value: 'x' })),
    }),
    undefined
  )
})
test('flow graph rejects duplicate nodes, missing references and self edges', () => {
  const graph = {
    kind: 'flowchart',
    title: 'Flow',
    nodes: [
      { id: 'a', label: 'Start' },
      { id: 'b', label: 'Finish' },
    ],
    edges: [{ from: 'a', to: 'b' }],
  }
  assert.ok(parseAssistantVisualization('show_flowchart', graph))
  assert.equal(
    parseAssistantVisualization('show_flowchart', {
      ...graph,
      nodes: [graph.nodes[0], graph.nodes[0]],
    }),
    undefined
  )
  for (const to of ['a', 'missing']) {
    assert.equal(
      parseAssistantVisualization('show_flowchart', {
        ...graph,
        edges: [{ from: 'a', to }],
      }),
      undefined
    )
  }
})
