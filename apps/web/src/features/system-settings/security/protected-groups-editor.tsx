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
/*
Copyright (C) 2026 LIghtJUNction
*/
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { MultiSelect } from '@/components/multi-select'
import { Skeleton } from '@/components/ui/skeleton'

import {
  inspectProtectedGroupRules,
  replaceEnabledRuleGroups,
} from './protected-group-rules.js'

interface ProtectedGroupsEditorProps {
  value: string
  availableGroups: string[]
  isLoading: boolean
  onChange: (value: string) => void
}

export function ProtectedGroupsEditor({
  value,
  availableGroups,
  isLoading,
  onChange,
}: ProtectedGroupsEditorProps) {
  const { t } = useTranslation()
  const [error, setError] = useState<string | null>(null)
  const state = inspectProtectedGroupRules(value)
  const options = useMemo(
    () =>
      [...new Set([...availableGroups, ...state.groups])]
        .sort((left, right) => left.localeCompare(right))
        .map((group) => ({ label: group, value: group })),
    [availableGroups, state.groups]
  )

  let description = t(
    'Add or remove groups here to update every enabled advanced security rule.'
  )
  if (!state.valid) {
    description = t('Fix the rules JSON before editing protected groups.')
  } else if (state.enabledRuleCount === 0) {
    description = t(
      'Enable at least one advanced security rule before editing groups.'
    )
  }

  const handleChange = (groups: string[]) => {
    const updated = replaceEnabledRuleGroups(value, groups)
    if (!updated) {
      setError(
        t(
          'Each advanced security rule must include at least one explicit group.'
        )
      )
      return
    }
    setError(null)
    onChange(updated)
  }

  return (
    <div className='border-border/60 bg-muted/20 space-y-3 rounded-lg border p-4'>
      <div className='space-y-1'>
        <label
          htmlFor='advanced-security-protected-groups'
          className='text-sm font-medium'
        >
          {t('Protected groups')}
        </label>
        <p
          id='advanced-security-protected-groups-description'
          className='text-muted-foreground text-sm'
        >
          {description}
        </p>
      </div>
      {isLoading ? (
        <Skeleton className='h-9 w-full' />
      ) : (
        <MultiSelect
          id='advanced-security-protected-groups'
          options={options}
          selected={state.groups}
          onChange={handleChange}
          placeholder={t('Select protected groups')}
          allowCreate
          createLabel={t('Add "{{value}}"')}
          emptyText={t('No results found')}
          disabled={!state.valid || state.enabledRuleCount === 0}
          maxVisibleChips={12}
        />
      )}
      {error ? (
        <p className='text-destructive text-sm' role='alert'>
          {error}
        </p>
      ) : null}
    </div>
  )
}
