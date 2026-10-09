/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
export type AssistantVisualization = {
  kind: 'chart' | 'statistics' | 'choices' | 'flowchart'
  title: string
  source?: string
  chart_type?: 'line' | 'bar' | 'donut'
  unit?: string
  labels?: string[]
  series?: { name: string; values: number[] }[]
  items?: { label: string; value: string; detail?: string; icon?: string }[]
  nodes?: { id: string; label: string }[]
  edges?: { from: string; to: string; label?: string }[]
}
const kinds: Readonly<Record<string, AssistantVisualization['kind']>> = {
  show_chart: 'chart',
  show_statistics: 'statistics',
  show_choices: 'choices',
  show_flowchart: 'flowchart',
}
const object = (value: unknown): Record<string, unknown> | null =>
  value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null
const text = (value: unknown, max: number, optional = false): boolean =>
  (optional && value === undefined) ||
  (typeof value === 'string' &&
    [...value].length <= max &&
    !value.includes('\0') &&
    (optional || value.trim() !== ''))
const list = (value: unknown, max: number, min = 1): value is unknown[] =>
  Array.isArray(value) && value.length >= min && value.length <= max

// Repeat the server bounds at the browser boundary. No executable chart config
// and no raw HTML are accepted, including in a restored or proxied response.
export function parseAssistantVisualization(
  name: string,
  value: unknown
): AssistantVisualization | undefined {
  const visual = object(value)
  if (
    !kinds[name] ||
    !visual ||
    visual.kind !== kinds[name] ||
    !text(visual.title, 120) ||
    !text(visual.source, 300, true) ||
    !text(visual.unit, 30, true)
  ) {
    return undefined
  }
  if (visual.kind === 'chart') {
    if (
      !['line', 'bar', 'donut'].includes(String(visual.chart_type)) ||
      !list(visual.labels, 80) ||
      !visual.labels.every((item) => text(item, 80)) ||
      !list(visual.series, 4)
    ) {
      return undefined
    }
    const count = visual.labels.length
    if (visual.chart_type === 'donut' && visual.series.length !== 1) {
      return undefined
    }
    if (
      visual.series.some((item) => {
        const series = object(item)
        return (
          !series ||
          !text(series.name, 80) ||
          !list(series.values, 80) ||
          series.values.length !== count ||
          series.values.some(
            (number) =>
              typeof number !== 'number' ||
              !Number.isFinite(number) ||
              Math.abs(number) > 1e15 ||
              (visual.chart_type === 'donut' && number < 0)
          )
        )
      })
    ) {
      return undefined
    }
  } else if (visual.kind === 'choices' || visual.kind === 'statistics') {
    if (
      !list(visual.items, 8) ||
      visual.items.some((item) => {
        const row = object(item)
        return (
          !row ||
          !text(row.label, 80) ||
          !text(row.value, visual.kind === 'choices' ? 500 : 100) ||
          !text(row.detail, 180, true) ||
          (row.icon !== undefined &&
            ![
              '',
              'activity',
              'wallet',
              'clock',
              'check',
              'sparkles',
              'chart',
            ].includes(String(row.icon)))
        )
      })
    ) {
      return undefined
    }
  } else if (visual.kind === 'flowchart') {
    if (!list(visual.nodes, 16) || !list(visual.edges ?? [], 24, 0)) {
      return undefined
    }
    const ids = new Set<string>()
    for (const item of visual.nodes) {
      const node = object(item)
      if (
        !node ||
        !text(node.id, 40) ||
        !text(node.label, 80) ||
        ids.has(String(node.id))
      ) {
        return undefined
      }
      ids.add(String(node.id))
    }
    if (
      ((visual.edges ?? []) as unknown[]).some((item) => {
        const edge = object(item)
        return (
          !edge ||
          !ids.has(String(edge.from)) ||
          !ids.has(String(edge.to)) ||
          edge.from === edge.to ||
          !text(edge.label, 80, true)
        )
      })
    ) {
      return undefined
    }
  }
  // Whitelist fields so extra provider fields cannot reach any renderer.
  const {
    kind,
    title,
    source,
    chart_type,
    unit,
    labels,
    series,
    items,
    nodes,
    edges,
  } = visual
  return {
    kind,
    title,
    source,
    chart_type,
    unit,
    labels,
    series,
    items,
    nodes,
    edges,
  } as AssistantVisualization
}

export function chartDomain(series: { values: number[] }[]): [number, number] {
  const values = series.flatMap((item) => item.values)
  const min = Math.min(0, ...values),
    max = Math.max(0, ...values)
  return min === max ? [0, 1] : [min, max]
}
