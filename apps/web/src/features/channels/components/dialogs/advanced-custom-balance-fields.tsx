/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
  FieldTitle,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

import { validateAdvancedCustomBalance } from '../../lib/advanced-custom'
import type { AdvancedCustomBalanceConfig } from '../../types'

export function AdvancedCustomBalanceFields({
  value,
  onChange,
}: {
  value?: AdvancedCustomBalanceConfig
  onChange: (value: AdvancedCustomBalanceConfig) => void
}) {
  const { t } = useTranslation()
  const id = useId()
  const config =
    value && typeof value === 'object' && !Array.isArray(value) ? value : {}
  const method =
    typeof config.method === 'string'
      ? config.method.trim().toUpperCase() || 'GET'
      : 'GET'
  const invalid = Boolean(value && validateAdvancedCustomBalance(value))
  const update = (patch: Partial<AdvancedCustomBalanceConfig>) =>
    onChange({ ...config, ...patch })

  return (
    <FieldSet className='border-t pt-4'>
      <FieldLegend>{t('Balance lookup')}</FieldLegend>
      <FieldDescription>
        {t(
          'Go backend only. Rust does not support Advanced Custom balance queries.'
        )}
      </FieldDescription>
      <FieldGroup className='gap-4'>
        <Field data-invalid={invalid}>
          <FieldTitle id={`${id}-method`}>{t('Balance method')}</FieldTitle>
          <ToggleGroup
            aria-labelledby={`${id}-method`}
            variant='outline'
            value={[method]}
            onValueChange={(values) => {
              const next = values[0]
              if (next !== 'GET' && next !== 'POST') return
              update({
                method: next,
                body_template:
                  next === 'GET' ? undefined : config.body_template,
              })
            }}
          >
            <ToggleGroupItem value='GET'>GET</ToggleGroupItem>
            <ToggleGroupItem value='POST'>POST</ToggleGroupItem>
          </ToggleGroup>
        </Field>
        {method === 'POST' ? (
          <Field data-invalid={invalid}>
            <FieldLabel htmlFor={`${id}-body`}>
              {t('POST body template')}
            </FieldLabel>
            <Textarea
              id={`${id}-body`}
              className='font-mono'
              value={config.body_template ?? ''}
              aria-invalid={invalid}
              onChange={(event) =>
                update({
                  body_template:
                    event.target.value === '' ? undefined : event.target.value,
                })
              }
              placeholder='{"api_key":"{api_key}"}'
            />
            <FieldDescription>
              {t(
                'Optional JSON body. Use {api_key} inside a JSON string. Maximum 16 KiB.'
              )}
            </FieldDescription>
          </Field>
        ) : null}
        <Field data-invalid={invalid}>
          <FieldLabel htmlFor={`${id}-pointer`}>
            {t('Balance JSON pointer')}
          </FieldLabel>
          <Input
            id={`${id}-pointer`}
            className='font-mono'
            value={config.json_pointer ?? ''}
            aria-invalid={invalid}
            onChange={(event) =>
              update({
                json_pointer: event.target.value || undefined,
                scale: event.target.value === '' ? undefined : config.scale,
              })
            }
            placeholder='/data/balance'
          />
          <FieldDescription>
            {t(
              'Leave empty to keep automatic OpenAI balance detection and raw JSON responses.'
            )}
          </FieldDescription>
        </Field>
        <Field data-invalid={invalid} data-disabled={!config.json_pointer}>
          <FieldLabel htmlFor={`${id}-scale`}>{t('Balance scale')}</FieldLabel>
          <Input
            id={`${id}-scale`}
            type='number'
            step='any'
            placeholder='1'
            disabled={!config.json_pointer}
            value={config.scale ?? ''}
            aria-invalid={invalid}
            onChange={(event) =>
              update({
                scale:
                  event.target.value === ''
                    ? undefined
                    : event.target.valueAsNumber,
              })
            }
          />
          <FieldDescription>
            {t('Multiply the selected JSON number by this positive scale.')}
          </FieldDescription>
        </Field>
      </FieldGroup>
    </FieldSet>
  )
}
