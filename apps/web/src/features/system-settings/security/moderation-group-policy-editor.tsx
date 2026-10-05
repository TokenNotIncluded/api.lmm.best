/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useEffect, useState } from 'react'
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
import { useWalletCurrency } from '@/hooks/use-wallet-currency'

import { SystemJsonCodeEditor } from '../components/system-json-code-editor'
import {
  MODERATION_CATEGORIES,
  MODERATION_CATEGORY_LABELS,
  MODERATION_MAX_CATEGORY_FINE_USD,
  isModerationFineUsd,
  moderationFineDisplayUsd,
  normalizeModerationPolicyUsd,
  parseModerationGroupPolicies,
  type ModerationMode,
} from './moderation-config'

export function ModerationGroupPolicyEditor(props: {
  value: string
  groups: string[]
  disabled?: boolean
  onChange: (value: string) => void
  onValidityChange?: (valid: boolean) => void
}) {
  const { t } = useTranslation()
  const { onValidityChange } = props
  const { config } = useWalletCurrency()
  const [error, setError] = useState('')
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  useEffect(() => {
    setError('')
    setDrafts({})
    onValidityChange?.(true)
  }, [props.value, onValidityChange])
  const reject = (message: string, id: string, text: string) => {
    setError(message)
    setDrafts((previous) => ({ ...previous, [id]: text }))
    props.onValidityChange?.(false)
  }
  const policies = parseModerationGroupPolicies(props.value)
  if (!policies) {
    return (
      <div className='space-y-2'>
        <p className='text-destructive text-sm' role='alert'>
          {t(
            'Use explicit groups, valid modes, and category fees from 0 to 1000 USD with up to six decimal places.'
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
      {error ? (
        <p className='text-destructive text-sm' role='alert'>
          {t(error)}
        </p>
      ) : null}
      <p className='text-muted-foreground text-sm'>
        {t(
          'Policies apply to the user’s account group. Every group starts off; the routing group does not select which users are reviewed.'
        )}
      </p>
      {groups.map((group, index) => {
        const policy = policies[group] ?? {
          mode: 'off' as const,
          category_fines_usd: {},
          amount_currency: 'USD' as const,
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
                      value={
                        drafts[`${group}:${category}`] ??
                        (policy.amount_currency === 'USD' ||
                        (config.legacyPricingUnitsPerUsd ?? 0) > 0
                          ? moderationFineDisplayUsd(
                              policy.category_fines_usd[category] ?? 0,
                              policy,
                              config.legacyPricingUnitsPerUsd ?? 0
                            )
                          : '')
                      }
                      disabled={
                        props.disabled ||
                        (policy.amount_currency !== 'USD' &&
                          !((config.legacyPricingUnitsPerUsd ?? 0) > 0))
                      }
                      onChange={(event) => {
                        const text = event.target.value
                        const amount =
                          text.trim() === '' ? Number.NaN : Number(text)
                        if (!isModerationFineUsd(amount)) {
                          reject(
                            'Enter a USD fine from 0 to 1000 with at most six decimal places.',
                            `${group}:${category}`,
                            text
                          )
                          return
                        }
                        const unchangedFines = { ...policy.category_fines_usd }
                        delete unchangedFines[category]
                        const normalized = normalizeModerationPolicyUsd(
                          { ...policy, category_fines_usd: unchangedFines },
                          config.legacyPricingUnitsPerUsd ?? 0
                        )
                        if (!normalized) {
                          reject(
                            'This legacy group cannot be converted to USD without changing an existing fine. Keep its current prices or explicitly replace its policy in JSON.',
                            `${group}:${category}`,
                            text
                          )
                          return
                        }
                        setError('')
                        setDrafts({})
                        props.onValidityChange?.(true)
                        props.onChange(
                          JSON.stringify({
                            ...policies,
                            [group]: {
                              ...normalized,
                              category_fines_usd: {
                                ...normalized.category_fines_usd,
                                [category]: amount,
                              },
                            },
                          })
                        )
                      }}
                    />
                    {policy.amount_currency !== 'USD' ? (
                      <p className='text-muted-foreground text-xs'>
                        {t(
                          'Stored legacy price: {{amount}} legacy pricing units. Editing uses USD.',
                          {
                            amount: String(
                              policy.category_fines_usd[category] ?? 0
                            ),
                          }
                        )}
                      </p>
                    ) : null}
                  </div>
                ))}
              </div>
            ) : null}
          </fieldset>
        )
      })}
      {error ? (
        <SystemJsonCodeEditor
          configurationKey='ModerationGroupPolicies'
          value={props.value}
          onChange={props.onChange}
          heightClassName='h-48 min-h-48 max-h-48'
          aria-label={t('Group review policies')}
        />
      ) : null}
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
