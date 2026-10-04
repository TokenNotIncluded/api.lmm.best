/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useTranslation } from 'react-i18next'

import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import { SystemJsonCodeEditor } from '../components/system-json-code-editor'
import {
  MODERATION_CATEGORIES,
  MODERATION_CATEGORY_LABELS,
  MODERATION_MAX_CATEGORY_FINE_USD,
  parseModerationGroupPolicies,
  type ModerationMode,
} from './moderation-config'

export function ModerationGroupPolicyEditor(props: {
  value: string
  groups: string[]
  disabled?: boolean
  onChange: (value: string) => void
}) {
  const { t } = useTranslation()
  const policies = parseModerationGroupPolicies(props.value)
  if (!policies) {
    return (
      <div className='space-y-2'>
        <p className='text-destructive text-sm' role='alert'>
          {t(
            'Use explicit groups, valid modes, and category fees from $0 to $1000 with up to six decimal places.'
          )}
        </p>
        <SystemJsonCodeEditor
          configurationKey='ModerationGroupPolicies'
          value={props.value}
          onChange={props.onChange}
          heightClassName='h-48 min-h-48 max-h-48'
          aria-label={t('Group review policies')}
        />
      </div>
    )
  }
  const groups = [
    ...new Set([...props.groups, ...Object.keys(policies)]),
  ].sort()
  return (
    <div className='space-y-4' data-testid='moderation-group-policy-editor'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Policies apply to the user’s account group. Every group starts off; the routing group does not select which users are reviewed.'
        )}
      </p>
      {groups.map((group, index) => {
        const policy = policies[group] ?? {
          mode: 'off' as const,
          category_fines_usd: {},
        }
        const updateMode = (mode: ModerationMode) =>
          props.onChange(
            JSON.stringify({ ...policies, [group]: { ...policy, mode } })
          )
        return (
          <fieldset
            key={group}
            className='min-w-0 space-y-4 border-b pb-4 last:border-b-0'
          >
            <legend className='mb-2 max-w-full text-sm font-medium break-all'>
              {group}
            </legend>
            <div className='space-y-2'>
              <Label htmlFor={`moderation-policy-${index}`}>
                {t('Review mode')}
              </Label>
              <Select
                value={policy.mode}
                onValueChange={(value) =>
                  value && updateMode(value as ModerationMode)
                }
                disabled={props.disabled}
              >
                <SelectTrigger id={`moderation-policy-${index}`}>
                  <SelectValue>
                    {t(
                      policy.mode === 'strict'
                        ? 'Strict — category fine'
                        : policy.mode === 'tolerant'
                          ? 'Tolerant — warning only'
                          : 'Off'
                    )}
                  </SelectValue>
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    <SelectItem value='off'>{t('Off')}</SelectItem>
                    <SelectItem value='tolerant'>
                      {t('Tolerant — warning only')}
                    </SelectItem>
                    <SelectItem value='strict'>
                      {t('Strict — category fine')}
                    </SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
            {policy.mode === 'strict' ? (
              <div
                className='grid gap-3 sm:grid-cols-2'
                data-testid={`moderation-fines-${group}`}
              >
                {MODERATION_CATEGORIES.map((category, categoryIndex) => (
                  <div key={category} className='space-y-1.5'>
                    <Label
                      htmlFor={`moderation-fine-${index}-${categoryIndex}`}
                    >
                      {t(MODERATION_CATEGORY_LABELS[category])} (USD)
                    </Label>
                    <Input
                      id={`moderation-fine-${index}-${categoryIndex}`}
                      type='number'
                      min={0}
                      max={MODERATION_MAX_CATEGORY_FINE_USD}
                      step={0.000001}
                      value={policy.category_fines_usd[category] ?? 0}
                      disabled={props.disabled}
                      onChange={(event) => {
                        const amount =
                          event.target.value === ''
                            ? 0
                            : Number(event.target.value)
                        if (Number.isFinite(amount)) {
                          props.onChange(
                            JSON.stringify({
                              ...policies,
                              [group]: {
                                ...policy,
                                category_fines_usd: {
                                  ...policy.category_fines_usd,
                                  [category]: amount,
                                },
                              },
                            })
                          )
                        }
                      }}
                    />
                  </div>
                ))}
              </div>
            ) : null}
          </fieldset>
        )
      })}
      {groups.length === 0 ? (
        <p className='text-muted-foreground text-sm'>
          {t('No account groups are available. Review stays off.')}
        </p>
      ) : null}
      <p className='text-muted-foreground text-xs'>
        {t(
          'Deductions round down to the wallet’s smallest supported amount. Smaller fees only trigger a warning.'
        )}
      </p>
      <p className='text-muted-foreground text-xs'>
        {t(
          'A flagged input incurs only the highest configured category fine, once. Unset categories cost zero; deductions cannot exceed the positive wallet balance. Flagged outputs do not penalize users or increase their risk score.'
        )}
      </p>
    </div>
  )
}
