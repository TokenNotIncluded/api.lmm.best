/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
export function csvCell(value: string | number): string {
  let text = String(value)
  if (typeof value === 'string' && /^[\s]*[=+\-@]/.test(text)) text = `'${text}`
  return `"${text.replaceAll('"', '""')}"`
}
export function sourceSummaryCSV(report: {
  from: number
  to: number
  observed_until: number
  channels: {
    source: string
    evidence: string
    registrations: number
    identified_registrations: number
  }[]
  payments: {
    source: string
    currency: string
    paid_micros: number
    refund_micros: number
    net_micros: number
  }[]
  activity_state?: {
    started_at: number
    scanned_through: number
    status: string
    incomplete: boolean
    gap_from?: number
    gap_through?: number
  } | null
  activity?: {
    source: string
    eligible_accounts: number
    successful_accounts: number
    incomplete_accounts?: number
    mature_accounts: number
    retained_accounts: number
    observing_accounts: number
    retention_rate: number | null
  }[]
}): string {
  const rows: (string | number)[][] = [
    ['section', 'source', 'metric', 'value', 'currency'],
    ['metadata', '', 'registration_from', report.from, ''],
    ['metadata', '', 'registration_to', report.to, ''],
    ['metadata', '', 'observed_until', report.observed_until, ''],
  ]
  rows.push(
    [
      'metadata',
      '',
      'activity_started_at',
      report.activity_state?.started_at ?? 'unknown',
      '',
    ],
    [
      'metadata',
      '',
      'activity_processed_through',
      report.activity_state?.scanned_through ?? 'unknown',
      '',
    ],
    [
      'metadata',
      '',
      'activity_status',
      report.activity_state?.status ?? 'unknown',
      '',
    ],
    [
      'metadata',
      '',
      'activity_incomplete',
      report.activity_state
        ? String(report.activity_state.incomplete)
        : 'unknown',
      '',
    ]
  )
  rows.push(
    [
      'metadata',
      '',
      'activity_gap_from',
      report.activity_state?.gap_from ?? 'unknown',
      '',
    ],
    [
      'metadata',
      '',
      'activity_gap_through',
      report.activity_state?.gap_through ?? 'unknown',
      '',
    ]
  )
  for (const row of report.channels) {
    rows.push([
      'registration',
      row.source,
      `accounts:${row.evidence}`,
      row.registrations,
      '',
    ])
    rows.push([
      'registration',
      row.source,
      `identified:${row.evidence}`,
      row.identified_registrations,
      '',
    ])
  }
  for (const row of report.payments) {
    rows.push(
      ['payment', row.source, 'paid_micros', row.paid_micros, row.currency],
      ['payment', row.source, 'refund_micros', row.refund_micros, row.currency],
      ['payment', row.source, 'net_micros', row.net_micros, row.currency]
    )
  }
  for (const row of report.activity ?? []) {
    for (const metric of [
      'eligible_accounts',
      'successful_accounts',
      'incomplete_accounts',
      'mature_accounts',
      'retained_accounts',
      'observing_accounts',
      'retention_rate',
    ] as const) {
      rows.push([
        'observed_text_api',
        row.source,
        metric,
        row[metric] ?? 'unknown',
        '',
      ])
    }
  }
  return `\uFEFF${rows
    .map((row) => row.map(csvCell).join(','))
    .join('\r\n')}\r\n`
}
