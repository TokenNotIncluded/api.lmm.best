import { Plus, Trash2 } from 'lucide-react'
/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

import { DELIVERY_TEMPLATES } from './delivery-template'
import { useStoreMoneyDraft } from './money'
import { STORE_MAX_VARIANTS, type StoreNewVariantDraft } from './new-variants'
import { STORE_PUBLISHING_COPY as copy } from './publishing-copy'

export function StoreNewVariantsEditor({
  variants,
  disabled,
  fixedContentSupported,
  onChange,
}: {
  variants: StoreNewVariantDraft[]
  disabled: boolean
  fixedContentSupported: boolean
  onChange: (variants: StoreNewVariantDraft[]) => void
}) {
  const { t } = useTranslation()
  return (
    <section
      className='bg-muted/35 space-y-4 rounded-2xl p-4 sm:p-5'
      aria-label={t(copy.variantsTitle)}
    >
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div className='space-y-1'>
          <h3 className='font-semibold'>{t(copy.variantsTitle)}</h3>
          <p className='text-muted-foreground max-w-prose text-xs leading-5'>
            {t(copy.variantsHelp)}
          </p>
        </div>
        <Button
          type='button'
          variant='secondary'
          disabled={disabled || variants.length >= STORE_MAX_VARIANTS - 1}
          onClick={() =>
            onChange([
              ...variants,
              {
                key: crypto.randomUUID(),
                name: '',
                price_quota: Number.NaN,
                template: 'card-key',
                enabled: true,
              },
            ])
          }
        >
          <Plus className='size-4' aria-hidden='true' />
          {t('Add variant')}
        </Button>
      </div>
      {variants.map((variant, index) => (
        <StoreNewVariantRow
          key={variant.key}
          value={variant}
          index={index + 2}
          disabled={disabled}
          fixedContentSupported={fixedContentSupported}
          onChange={(value) =>
            onChange(
              variants.map((row) => (row.key === variant.key ? value : row))
            )
          }
          onRemove={() =>
            onChange(variants.filter((row) => row.key !== variant.key))
          }
        />
      ))}
    </section>
  )
}

function StoreNewVariantRow({
  value,
  index,
  disabled,
  fixedContentSupported,
  onChange,
  onRemove,
}: {
  value: StoreNewVariantDraft
  index: number
  disabled: boolean
  fixedContentSupported: boolean
  onChange: (value: StoreNewVariantDraft) => void
  onRemove: () => void
}) {
  const { t } = useTranslation()
  const id = useId()
  const money = useStoreMoneyDraft(value.price_quota)
  return (
    <fieldset
      className='bg-background min-w-0 space-y-4 rounded-xl p-4'
      data-store-new-variant={index}
      disabled={disabled}
    >
      <legend className='sr-only'>
        {t('Product variant')} {index}
      </legend>
      <div className='flex items-center justify-between gap-3'>
        <span className='text-muted-foreground text-xs'>
          {t('Product variant')} {index}
        </span>
        <Button
          type='button'
          size='sm'
          variant='ghost'
          onClick={onRemove}
          disabled={disabled}
          aria-label={`${t('Remove')} ${t('Product variant')} ${index}`}
        >
          <Trash2 className='size-4' aria-hidden='true' />
          {t('Remove')}
        </Button>
      </div>
      <div className='grid gap-4 sm:grid-cols-2'>
        <div className='space-y-2'>
          <Label htmlFor={`${id}-name`}>{t('Variant name')}</Label>
          <Input
            id={`${id}-name`}
            data-variant-name
            required
            maxLength={200}
            value={value.name}
            onChange={(event) =>
              onChange({ ...value, name: event.target.value })
            }
            placeholder={t('Variant name')}
          />
        </div>
        <div className='space-y-2'>
          <Label htmlFor={`${id}-price`}>{t('Unit price')}</Label>
          <div className='flex gap-2'>
            <select
              aria-label={t('Price currency')}
              className='bg-muted h-11 rounded-xl px-3 text-sm'
              value={money.currency}
              onChange={(event) =>
                money.setCurrency(
                  event.target.value as 'USD' | 'CNY' | 'CREDIT'
                )
              }
            >
              {(['USD', 'CNY', 'CREDIT'] as const).map((currency) => (
                <option key={currency} value={currency}>
                  {currency}
                </option>
              ))}
            </select>
            <Input
              id={`${id}-price`}
              data-variant-price
              inputMode='decimal'
              required
              value={money.input}
              onChange={(event) =>
                onChange({
                  ...value,
                  price_quota: money.setInput(event.target.value) ?? Number.NaN,
                })
              }
            />
          </div>
        </div>
        <div className='space-y-2'>
          <Label htmlFor={`${id}-template`}>{t('Delivery template')}</Label>
          <select
            id={`${id}-template`}
            data-variant-template
            className='bg-muted h-11 w-full rounded-xl px-3 text-sm'
            value={value.template}
            onChange={(event) =>
              onChange({
                ...value,
                template: event.target
                  .value as StoreNewVariantDraft['template'],
              })
            }
          >
            {Object.entries(DELIVERY_TEMPLATES)
              .filter(
                ([key]) => key !== 'fixed-content' || fixedContentSupported
              )
              .map(([key, template]) => (
                <option key={key} value={key}>
                  {t(template.label)}
                </option>
              ))}
          </select>
        </div>
        <label className='flex items-center justify-between gap-3 text-sm'>
          {t('Variant enabled')}
          <Switch
            checked={value.enabled}
            disabled={disabled}
            onCheckedChange={(enabled) => onChange({ ...value, enabled })}
          />
        </label>
      </div>
      {value.template === 'fixed-content' && (
        <div className='space-y-2'>
          <Label htmlFor={`${id}-content`}>{t('Delivery content')}</Label>
          <Textarea
            id={`${id}-content`}
            data-variant-content
            rows={4}
            required
            value={value.fixed_content ?? ''}
            onChange={(event) =>
              onChange({ ...value, fixed_content: event.target.value })
            }
          />
          <p className='text-muted-foreground text-xs leading-5'>
            {t(DELIVERY_TEMPLATES[value.template].help)}
          </p>
        </div>
      )}
    </fieldset>
  )
}
