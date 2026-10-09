/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import {
  Activity,
  ArrowUpRight,
  ChartNoAxesCombined,
  Check,
  Clock3,
  Sparkles,
  Wallet,
} from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'

import {
  chartDomain,
  type AssistantVisualization,
} from './assistant-visualization-data'

import './assistant-visualization.css'

const tones = [
  'var(--chart-1)',
  'var(--chart-2)',
  'var(--chart-3)',
  'var(--chart-4)',
  'var(--chart-5)',
]
const icons = {
  activity: Activity,
  wallet: Wallet,
  clock: Clock3,
  check: Check,
  sparkles: Sparkles,
  chart: ChartNoAxesCombined,
}

export function AssistantVisualizationCard({
  visual,
  onChoose,
  provenance,
}: {
  visual: AssistantVisualization
  onChoose?: (text: string) => void
  provenance?: string
}) {
  const { t } = useTranslation()
  const [selected, setSelected] = useState<string | null>(null)
  const id = useId()
  return (
    <section
      className='assistant-visual'
      aria-labelledby={`${id}-title`}
      data-visualization={visual.kind}
    >
      <header className='assistant-visual-heading'>
        <span className='assistant-visual-eyebrow'>{t('Visualization')}</span>
        <h3 id={`${id}-title`}>{visual.title}</h3>
      </header>
      {visual.kind === 'chart' && <VisualChart visual={visual} />}
      {visual.kind === 'statistics' && (
        <dl className='assistant-visual-stats'>
          {visual.items?.map((item, index) => {
            const Icon = icons[item.icon as keyof typeof icons] ?? Activity
            return (
              <div key={index}>
                <Icon aria-hidden='true' className='size-4' />
                <dt>{item.label}</dt>
                <dd>{item.value}</dd>
                {item.detail && <p>{item.detail}</p>}
              </div>
            )
          })}
        </dl>
      )}
      {visual.kind === 'choices' && (
        <div className='assistant-visual-choices'>
          {visual.items?.map((item, index) => (
            <Button
              key={index}
              type='button'
              variant='outline'
              className='h-auto min-h-14 justify-between gap-4 text-start whitespace-normal'
              disabled={!onChoose}
              aria-pressed={selected === item.value}
              onClick={() => {
                setSelected(item.value)
                onChoose?.(item.value)
              }}
            >
              <span>
                <span className='block'>{item.label}</span>
                {item.detail && (
                  <span className='text-muted-foreground mt-1 block text-xs font-normal'>
                    {item.detail}
                  </span>
                )}
              </span>
              {selected === item.value ? (
                <Check className='size-4 shrink-0' />
              ) : (
                <ArrowUpRight className='size-4 shrink-0' />
              )}
            </Button>
          ))}
          <p role='status' className='text-muted-foreground text-xs'>
            {t(
              selected
                ? 'Choice added to the composer. Review it before sending.'
                : 'Choose an option to fill the composer. No action is submitted.'
            )}
          </p>
        </div>
      )}
      {visual.kind === 'flowchart' && <VisualFlow visual={visual} />}
      <footer className='assistant-visual-source'>
        {provenance || t('Data supplied by the assistant')}
        {visual.source ? ` · ${visual.source}` : ''}
      </footer>
    </section>
  )
}

function VisualChart({ visual }: { visual: AssistantVisualization }) {
  const { t, i18n } = useTranslation()
  const uid = useId().replaceAll(':', '')
  const labels = visual.labels ?? [],
    series = visual.series ?? []
  const number = new Intl.NumberFormat(toIntlLocale(i18n.language), {
    maximumSignificantDigits: 6,
  })
  const [min, max] = chartDomain(series)
  const y = (value: number) => 230 - ((value - min) / (max - min)) * 192
  const x = (index: number) =>
    58 + (labels.length === 1 ? 250 : (index / (labels.length - 1)) * 500)
  const table = (
    <details className='assistant-visual-table'>
      <summary>{t('View data table')}</summary>
      <div className='overflow-x-auto'>
        <table>
          <caption className='sr-only'>{visual.title}</caption>
          <thead>
            <tr>
              <th scope='col'>{t('Label')}</th>
              {series.map((row, index) => (
                <th key={index} scope='col'>
                  {row.name}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {labels.map((label, index) => (
              <tr key={index}>
                <th scope='row'>{label}</th>
                {series.map((row, j) => (
                  <td key={j}>
                    {number.format(row.values[index])}
                    {visual.unit ? ` ${visual.unit}` : ''}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </details>
  )
  return (
    <>
      {visual.chart_type === 'donut' ? (
        <div className='assistant-visual-donut'>
          <svg viewBox='0 0 240 240' role='img' aria-label={visual.title}>
            <circle
              cx='120'
              cy='120'
              r='83'
              fill='none'
              stroke='var(--muted)'
              strokeWidth='24'
            />
            {(() => {
              const values = series[0]?.values ?? []
              const sum = values.reduce((a, b) => a + b, 0)
              let offset = 0
              return values.map((value, index) => {
                const length = sum > 0 ? (value / sum) * 100 : 0
                const start = offset
                offset += length
                return (
                  <circle
                    key={index}
                    cx='120'
                    cy='120'
                    r='83'
                    fill='none'
                    pathLength='100'
                    stroke={tones[index % tones.length]}
                    strokeWidth='24'
                    strokeDasharray={`${Math.max(0, length - 0.6)} ${100 - Math.max(0, length - 0.6)}`}
                    strokeDashoffset={-start}
                    transform='rotate(-90 120 120)'
                  >
                    <title>{`${labels[index]}: ${number.format(value)}`}</title>
                  </circle>
                )
              })
            })()}
            <text
              x='120'
              y='116'
              textAnchor='middle'
              className='assistant-visual-total'
            >
              {number.format(series[0]?.values.reduce((a, b) => a + b, 0) ?? 0)}
            </text>
            <text
              x='120'
              y='141'
              textAnchor='middle'
              className='assistant-visual-axis'
            >
              {visual.unit || t('Total')}
            </text>
          </svg>
          <ul className='assistant-visual-legend'>
            {labels.map((label, index) => (
              <li key={index}>
                <i style={{ background: tones[index % tones.length] }} />
                <span>{label}</span>
                <strong>{number.format(series[0]?.values[index] ?? 0)}</strong>
              </li>
            ))}
          </ul>
        </div>
      ) : (
        <>
          <svg
            className='assistant-visual-plot'
            viewBox='0 0 600 272'
            role='img'
            aria-label={visual.title}
          >
            <defs>
              {series.map((_, index) => (
                <linearGradient
                  key={index}
                  id={`${uid}-area-${index}`}
                  x1='0'
                  y1='0'
                  x2='0'
                  y2='1'
                >
                  <stop
                    offset='0%'
                    stopColor={tones[index]}
                    stopOpacity='.28'
                  />
                  <stop
                    offset='100%'
                    stopColor={tones[index]}
                    stopOpacity='.015'
                  />
                </linearGradient>
              ))}
            </defs>
            {Array.from({ length: 5 }, (_, index) => {
              const value = min + ((max - min) * index) / 4
              return (
                <g key={index}>
                  <line
                    x1='58'
                    x2='560'
                    y1={y(value)}
                    y2={y(value)}
                    stroke='var(--border)'
                    strokeDasharray='3 5'
                  />
                  <text
                    x='48'
                    y={y(value) + 4}
                    textAnchor='end'
                    className='assistant-visual-axis'
                  >
                    {new Intl.NumberFormat(toIntlLocale(i18n.language), {
                      notation: 'compact',
                      maximumFractionDigits: 1,
                    }).format(value)}
                  </text>
                </g>
              )
            })}
            {series.map((row, j) => {
              const points = row.values
                .map((value, i) => `${x(i)},${y(value)}`)
                .join(' ')
              if (visual.chart_type === 'line') {
                return (
                  <g key={j}>
                    <polygon
                      points={`${x(0)},${y(0)} ${points} ${x(labels.length - 1)},${y(0)}`}
                      fill={`url(#${uid}-area-${j})`}
                    />
                    <polyline
                      points={points}
                      fill='none'
                      stroke={tones[j]}
                      strokeWidth='2.5'
                      strokeLinecap='round'
                      strokeLinejoin='round'
                      vectorEffect='non-scaling-stroke'
                    />
                    {row.values.map((value, i) => (
                      <circle
                        key={i}
                        cx={x(i)}
                        cy={y(value)}
                        r={labels.length > 30 ? 2 : 3.5}
                        fill={tones[j]}
                        stroke='var(--card)'
                        strokeWidth='1.5'
                      >
                        <title>{`${labels[i]} · ${row.name}: ${number.format(value)}`}</title>
                      </circle>
                    ))}
                  </g>
                )
              }
              const slot = 500 / labels.length,
                width = (slot * 0.76) / series.length
              return (
                <g key={j}>
                  {row.values.map((value, i) => (
                    <rect
                      key={i}
                      x={58 + slot * i + slot * 0.12 + width * j}
                      y={Math.min(y(0), y(value))}
                      width={Math.max(0.4, width - 1.5)}
                      height={Math.max(0.5, Math.abs(y(value) - y(0)))}
                      rx={Math.min(4, width / 3)}
                      fill={tones[j]}
                    >
                      <title>{`${labels[i]} · ${row.name}: ${number.format(value)}`}</title>
                    </rect>
                  ))}
                </g>
              )
            })}
            {labels.map((label, index) =>
              index % Math.max(1, Math.ceil(labels.length / 6)) === 0 ? (
                <text
                  key={index}
                  x={
                    visual.chart_type === 'bar'
                      ? 58 + ((index + 0.5) * 500) / labels.length
                      : x(index)
                  }
                  y='256'
                  textAnchor='middle'
                  className='assistant-visual-axis'
                >
                  {[...label].slice(0, 12).join('')}
                </text>
              ) : null
            )}
          </svg>
          <ul className='assistant-visual-series'>
            {series.map((row, index) => (
              <li key={index}>
                <i style={{ background: tones[index] }} />
                {row.name}
                {visual.unit ? ` · ${visual.unit}` : ''}
              </li>
            ))}
          </ul>
        </>
      )}
      {table}
    </>
  )
}

function VisualFlow({ visual }: { visual: AssistantVisualization }) {
  const { t } = useTranslation()
  const uid = useId().replaceAll(':', '')
  const nodes = visual.nodes ?? [],
    edges = visual.edges ?? []
  const position = (id: string) => {
    const index = nodes.findIndex((node) => node.id === id)
    return { x: 20 + (index % 3) * 192, y: 20 + Math.floor(index / 3) * 120 }
  }
  const height = Math.ceil(nodes.length / 3) * 120
  return (
    <div className='assistant-visual-flow'>
      <svg
        viewBox={`0 0 596 ${height}`}
        style={{ minWidth: 500 }}
        role='img'
        aria-label={visual.title}
      >
        <defs>
          <marker
            id={`${uid}-arrow`}
            markerWidth='7'
            markerHeight='7'
            refX='6'
            refY='3.5'
            orient='auto'
          >
            <path d='M0,0 L7,3.5 L0,7 Z' fill='var(--muted-foreground)' />
          </marker>
        </defs>
        {edges.map((edge, index) => {
          const a = position(edge.from),
            b = position(edge.to)
          const sameRow = a.y === b.y
          const ax = sameRow ? a.x + 160 : a.x + 80,
            ay = sameRow ? a.y + 34 : a.y + 68,
            bx = sameRow ? b.x - 4 : b.x + 80,
            by = sameRow ? b.y + 34 : b.y - 4
          return (
            <g key={index}>
              <path
                d={`M${ax},${ay} C${ax + (sameRow ? 16 : 0)},${ay + (sameRow ? 0 : 26)} ${bx - (sameRow ? 16 : 0)},${by - (sameRow ? 0 : 26)} ${bx},${by}`}
                fill='none'
                stroke='var(--muted-foreground)'
                strokeWidth='1.5'
                markerEnd={`url(#${uid}-arrow)`}
              >
                <title>{edge.label || `${edge.from} → ${edge.to}`}</title>
              </path>
            </g>
          )
        })}
        {nodes.map((node, index) => {
          const p = position(node.id)
          const chars = [...node.label]
          return (
            <g key={node.id}>
              <rect
                x={p.x}
                y={p.y}
                width='160'
                height='68'
                rx='14'
                fill='var(--card)'
                stroke='var(--border)'
              />
              <text x={p.x + 14} y={p.y + 19} className='assistant-visual-axis'>
                {String(index + 1).padStart(2, '0')}
              </text>
              <text
                x={p.x + 80}
                y={p.y + 38}
                textAnchor='middle'
                className='assistant-visual-node'
              >
                <tspan x={p.x + 80}>{chars.slice(0, 16).join('')}</tspan>
                {chars.length > 16 && (
                  <tspan x={p.x + 80} dy='16'>
                    {chars.slice(16, 31).join('')}
                    {chars.length > 31 ? '…' : ''}
                  </tspan>
                )}
                <title>{node.label}</title>
              </text>
            </g>
          )
        })}
      </svg>
      <details className='assistant-visual-table'>
        <summary>{t('View process details')}</summary>
        <ol>
          {nodes.map((node) => (
            <li key={node.id}>
              {node.label}
              <ul>
                {edges
                  .filter((edge) => edge.from === node.id)
                  .map((edge, index) => (
                    <li key={index}>
                      → {nodes.find((target) => target.id === edge.to)?.label}
                      {edge.label ? ` · ${edge.label}` : ''}
                    </li>
                  ))}
              </ul>
            </li>
          ))}
        </ol>
      </details>
    </div>
  )
}
