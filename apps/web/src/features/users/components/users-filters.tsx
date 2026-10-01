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
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import { getGroups } from '../api'
import type { UserSortBy, UserSortOrder } from '../types'

type Filters = {
  risk: 'all' | 'high' | 'medium' | 'low'
  transfers: 'all' | 'sent' | 'received' | 'none'
  usage: 'all' | 'zero' | 'consumed'
  funding: 'all' | 'paid' | 'unpaid'
  checkin: 'all' | 'yes' | 'no'
  sortBy: UserSortBy
  sortOrder: UserSortOrder
}
type Props = { search: Filters; onChange: (patch: Partial<Filters>) => void }

function Choice({
  label,
  value,
  options,
  onChange,
}: {
  label: string
  value: string
  options: { value: string; label: string }[]
  onChange: (value: string) => void
}) {
  return (
    <div className='min-w-36 flex-1 space-y-1.5 sm:flex-none'>
      <Label>{label}</Label>
      <Select
        value={value}
        items={options}
        onValueChange={(value) => {
          if (value !== null) onChange(value)
        }}
      >
        <SelectTrigger aria-label={label} className='h-11 w-full sm:h-9'>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {options.map((item) => (
            <SelectItem key={item.value} value={item.value}>
              {item.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}

export function UsersFilters({ search, onChange }: Props) {
  const { t } = useTranslation()
  const fields = [
    {
      key: 'risk',
      label: 'Risk score',
      values: [
        ['all', 'All'],
        ['high', 'High risk (0.8–1)'],
        ['medium', 'Medium risk (0.4–0.8)'],
        ['low', 'Low risk (0–0.4)'],
      ],
    },
    {
      key: 'transfers',
      label: 'Transfers',
      values: [
        ['all', 'All'],
        ['sent', 'Sent transfers'],
        ['received', 'Received transfers'],
        ['none', 'No transfers'],
      ],
    },
    {
      key: 'usage',
      label: 'Usage',
      values: [
        ['all', 'All'],
        ['zero', 'Zero consumption'],
        ['consumed', 'Has consumption'],
      ],
    },
    {
      key: 'funding',
      label: 'Top-up',
      values: [
        ['all', 'All'],
        ['paid', 'Has paid top-ups'],
        ['unpaid', 'No paid top-ups'],
      ],
    },
    {
      key: 'checkin',
      label: 'Check-in',
      values: [
        ['all', 'All'],
        ['yes', 'Has check-ins'],
        ['no', 'No check-ins'],
      ],
    },
  ] as const
  return (
    <>
      {fields.map((field) => (
        <Choice
          key={field.key}
          label={t(field.label)}
          value={search[field.key]}
          options={field.values.map(([value, label]) => ({
            value,
            label: t(label),
          }))}
          onChange={(value) => onChange({ [field.key]: value })}
        />
      ))}
    </>
  )
}

export function UsersSort({ search, onChange }: Props) {
  const { t } = useTranslation()
  const options: [UserSortBy, string][] = [
    ['id', 'ID'],
    ['username', 'Username'],
    ['risk_score', 'Risk score'],
    ['transferred_quota', 'Transferred out'],
    ['received_quota', 'Received transfers'],
    ['checkin_quota', 'Check-in rewards'],
    ['used_quota', 'Used quota'],
    ['request_count', 'Requests'],
    ['quota', 'Remaining quota'],
    ['topup_quota', 'Top-up'],
    ['topup_money', 'Top-up amount'],
    ['group', 'Group'],
    ['created_at', 'Created At'],
    ['last_login_at', 'Last Login'],
    ['assistant_violations', 'Violations'],
  ]
  return (
    <div className='flex w-full flex-wrap gap-2 sm:w-auto'>
      <Choice
        label={t('Sort by')}
        value={search.sortBy}
        options={options.map(([value, label]) => ({ value, label: t(label) }))}
        onChange={(value) => onChange({ sortBy: value as UserSortBy })}
      />
      <Choice
        label={t('Sort order')}
        value={search.sortOrder}
        options={[
          { value: 'desc', label: t('Descending') },
          { value: 'asc', label: t('Ascending') },
        ]}
        onChange={(value) => onChange({ sortOrder: value as UserSortOrder })}
      />
    </div>
  )
}

export function UsersGroupFilter({
  value,
  onChange,
}: {
  value: string
  onChange: (value: string) => void
}) {
  const { t } = useTranslation()
  const { data } = useQuery({ queryKey: ['user-groups'], queryFn: getGroups })
  const groups = data?.data ?? []
  return (
    <Choice
      label={t('Group')}
      value={value || '__all__'}
      options={[
        { value: '__all__', label: t('All') },
        ...Array.from(new Set([...groups, ...(value ? [value] : [])])).map(
          (group) => ({ value: group, label: group })
        ),
      ]}
      onChange={(next) => onChange(next === '__all__' ? '' : next)}
    />
  )
}
