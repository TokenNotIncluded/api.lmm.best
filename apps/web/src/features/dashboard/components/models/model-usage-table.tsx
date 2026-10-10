/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { ArrowDown, ArrowUp, ArrowUpDown, Download, Search } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import { toIntlLocale } from '@/i18n/languages'

import {
  modelUsageCsv,
  selectModelUsage,
  summarizeModelUsage,
  type ModelUsageMetric,
  type ModelUsageRow,
  type ModelUsageSort,
} from '../../lib/model-usage'
import {
  registerDashboardUsageTranslations,
  type DashboardUsageText,
} from '../../model-usage-i18n'
import type { QuotaDataItem } from '../../types'

const PAGE_SIZE = 8
const METRICS = ['count', 'tokens', 'quota'] as const

interface ModelUsageTableViewProps {
  rows: ModelUsageRow[]
  total: number
  loading: boolean
  query: string
  sort: ModelUsageSort
  page: number
  text: DashboardUsageText
  number: Intl.NumberFormat
  percent: Intl.NumberFormat
  formatQuota: (quota: number) => string
  onQuery: (query: string) => void
  onSort: (metric: ModelUsageMetric) => void
  onPage: (page: number) => void
  onExport: () => void
}

function ModelUsageTableView(props: ModelUsageTableViewProps) {
  const {
    rows,
    total,
    loading,
    query,
    sort,
    text,
    number,
    percent,
    formatQuota,
    onQuery,
    onSort,
    onPage,
    onExport,
  } = props
  const pages = Math.max(1, Math.ceil(rows.length / PAGE_SIZE))
  const page = Math.min(props.page, pages - 1)
  const visible = rows.slice(page * PAGE_SIZE, (page + 1) * PAGE_SIZE)
  const DirectionIcon = sort.direction === 'ascending' ? ArrowUp : ArrowDown

  return (
    <section
      className='dashboard-usage'
      aria-label={text('title')}
      aria-busy={loading}
    >
      <header className='dashboard-usage__header'>
        <div>
          <h3>{text('title')}</h3>
          <p>{text('description')}</p>
        </div>
        <div className='dashboard-usage__actions'>
          <div className='dashboard-usage__search'>
            <Search aria-hidden='true' size={16} />
            <Input
              type='search'
              aria-label={text('search')}
              placeholder={text('search')}
              value={query}
              onChange={(event) => onQuery(event.target.value)}
              disabled={loading}
            />
          </div>
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={loading || !rows.length}
            onClick={onExport}
          >
            <Download aria-hidden='true' size={16} />
            {text('export')}
          </Button>
        </div>
      </header>
      <div
        className='dashboard-usage__mobile-sort'
        role='group'
        aria-label={text('sort')}
      >
        {METRICS.map((metric) => (
          <Button
            key={metric}
            type='button'
            variant={sort.metric === metric ? 'secondary' : 'ghost'}
            size='sm'
            disabled={loading || !rows.length}
            aria-pressed={sort.metric === metric}
            aria-label={`${text(metric)}: ${text(sort.metric === metric ? sort.direction : 'descending')}`}
            onClick={() => onSort(metric)}
          >
            {text(metric)}
            {sort.metric === metric && (
              <DirectionIcon aria-hidden='true' size={13} />
            )}
          </Button>
        ))}
      </div>
      {loading || !rows.length ? (
        <div className='dashboard-usage__empty' role='status'>
          {text(loading ? 'loading' : total ? 'noResults' : 'empty')}
        </div>
      ) : (
        <table className='dashboard-usage__table' role='table'>
          <caption className='dashboard-usage__caption'>
            {text('shareNote')}
          </caption>
          <thead role='rowgroup'>
            <tr role='row'>
              <th scope='col' role='columnheader'>
                {text('model')}
              </th>
              {METRICS.map((metric) => (
                <th
                  key={metric}
                  scope='col'
                  role='columnheader'
                  aria-sort={sort.metric === metric ? sort.direction : 'none'}
                >
                  <span className='dashboard-usage__mobile-heading'>
                    {text(metric)}
                  </span>
                  <button type='button' onClick={() => onSort(metric)}>
                    {text(metric)}
                    {sort.metric === metric ? (
                      <DirectionIcon aria-hidden='true' size={13} />
                    ) : (
                      <ArrowUpDown aria-hidden='true' size={13} />
                    )}
                  </button>
                </th>
              ))}
              <th scope='col' role='columnheader'>
                {text('share')}
              </th>
            </tr>
          </thead>
          <tbody role='rowgroup'>
            {visible.map((row) => (
              <tr key={row.model} role='row'>
                <th scope='row' role='rowheader'>
                  <span className='dashboard-usage__model'>
                    {row.model || text('unknown')}
                  </span>
                </th>
                <td role='cell' data-label={text('count')}>
                  {number.format(row.count)}
                </td>
                <td role='cell' data-label={text('tokens')}>
                  {number.format(row.tokens)}
                </td>
                <td
                  role='cell'
                  data-label={text('quota')}
                  className='dashboard-usage__cost'
                >
                  {formatQuota(row.quota)}
                </td>
                <td role='cell' data-label={text('share')}>
                  <span className='dashboard-usage__share'>
                    <span className='dashboard-usage__track' aria-hidden='true'>
                      <span
                        style={{
                          width: `${Math.max(0, Math.min(100, (row.share ?? 0) * 100))}%`,
                        }}
                      />
                    </span>
                    <span>
                      {row.share === null ? '—' : percent.format(row.share)}
                    </span>
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <footer className='dashboard-usage__footer'>
        <span role='status'>
          {loading
            ? text('loading')
            : text('results', { shown: rows.length, total })}
        </span>
        <nav aria-label={text('title')}>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            disabled={loading || page === 0}
            onClick={() => onPage(page - 1)}
          >
            {text('previous')}
          </Button>
          <span>{text('page', { page: page + 1, pages })}</span>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            disabled={loading || page + 1 >= pages}
            onClick={() => onPage(page + 1)}
          >
            {text('next')}
          </Button>
        </nav>
      </footer>
    </section>
  )
}

export function ModelUsageTable({
  data,
  loading = false,
}: {
  data: QuotaDataItem[]
  loading?: boolean
}) {
  const { t, i18n } = useTranslation()
  registerDashboardUsageTranslations(i18n)
  const text: DashboardUsageText = (key, values) =>
    t(key, { ...values, ns: 'dashboardUsage' })
  const { currency, formatQuota, quotaToAmount } = useWalletCurrency()
  const [query, setQuery] = useState('')
  const [page, setPage] = useState(0)
  const [sort, setSort] = useState<ModelUsageSort>({
    metric: 'quota',
    direction: 'descending',
  })
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const number = useMemo(() => new Intl.NumberFormat(locale), [locale])
  const percent = useMemo(
    () =>
      new Intl.NumberFormat(locale, {
        style: 'percent',
        maximumFractionDigits: 1,
      }),
    [locale]
  )
  const rows = useMemo(() => summarizeModelUsage(data), [data])
  const unknown = text('unknown')
  const selected = useMemo(
    () => selectModelUsage(rows, query, sort, unknown),
    [rows, query, sort, unknown]
  )

  const exportRows = () => {
    const headers = [
      text('model'),
      text('count'),
      text('tokens'),
      `${text('quota')} (${currency})`,
      `${text('share')} (%)`,
    ]
    const csv = modelUsageCsv(selected, headers, quotaToAmount, unknown)
    const url = URL.createObjectURL(
      new Blob([csv], { type: 'text/csv;charset=utf-8' })
    )
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = `model-usage-${new Date().toISOString().slice(0, 10)}.csv`
    document.body.appendChild(anchor)
    try {
      anchor.click()
    } finally {
      anchor.remove()
      // Allow the browser to consume the download before releasing its URL.
      window.setTimeout(() => URL.revokeObjectURL(url), 1000)
    }
  }

  return (
    <ModelUsageTableView
      rows={selected}
      total={rows.length}
      loading={loading}
      query={query}
      sort={sort}
      page={page}
      text={text}
      number={number}
      percent={percent}
      formatQuota={formatQuota}
      onQuery={(value) => {
        setQuery(value)
        setPage(0)
      }}
      onSort={(metric) => {
        setSort((previous) => ({
          metric,
          direction:
            previous.metric === metric && previous.direction === 'descending'
              ? 'ascending'
              : 'descending',
        }))
        setPage(0)
      }}
      onPage={setPage}
      onExport={exportRows}
    />
  )
}
