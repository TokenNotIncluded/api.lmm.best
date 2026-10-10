/* Copyright (C) 2026 LIghtJUNction; licensed under AGPL-3.0-or-later. */
import { useMemo, useState } from 'react'

import { compareOpenUICells } from './policy'

type TableProps = {
  title: string
  columns: string[]
  rows: (string | number)[][]
}

export function OpenUIDataTable({ title, columns, rows }: TableProps) {
  const [sort, setSort] = useState<{
    column: number
    descending: boolean
  } | null>(null)
  const orderedRows = useMemo(
    () =>
      sort
        ? [...rows].sort(
            (left, right) =>
              compareOpenUICells(left[sort.column], right[sort.column]) *
              (sort.descending ? -1 : 1)
          )
        : rows,
    [rows, sort]
  )
  return (
    <div className='min-w-0 overflow-x-auto'>
      <table className='w-full text-left text-sm'>
        <caption className='text-foreground pb-3 text-left font-medium'>
          {title}
        </caption>
        <thead>
          <tr>
            {columns.map((column, index) => (
              <th
                key={index}
                scope='col'
                aria-sort={
                  sort?.column === index
                    ? sort.descending
                      ? 'descending'
                      : 'ascending'
                    : 'none'
                }
                className='text-muted-foreground border-border border-b py-2 pr-4 font-medium'
              >
                <button
                  type='button'
                  className='focus-visible:ring-ring inline-flex min-h-9 items-center gap-2 rounded-sm text-left focus-visible:ring-2 focus-visible:outline-none'
                  onClick={() =>
                    setSort((previous) => ({
                      column: index,
                      descending:
                        previous?.column === index && !previous.descending,
                    }))
                  }
                >
                  {column}
                  <span aria-hidden='true'>
                    {sort?.column === index
                      ? sort.descending
                        ? '↓'
                        : '↑'
                      : '↕'}
                  </span>
                </button>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {orderedRows.map((row, rowIndex) => (
            <tr
              key={rowIndex}
              className='border-border/50 border-b last:border-0'
            >
              {columns.map((_, columnIndex) => (
                <td
                  key={columnIndex}
                  className='max-w-72 py-3 pr-4 align-top break-words tabular-nums'
                >
                  {row[columnIndex] ?? '—'}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
