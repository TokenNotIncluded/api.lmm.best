/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

import type { StoreCatalogueFilters } from './catalogue-types'

export function StoreCatalogueFiltersPanel({
  value,
  onChange,
  view,
  onViewChange,
}: {
  value: StoreCatalogueFilters
  onChange: (value: StoreCatalogueFilters) => void
  view: 'cards' | 'list'
  onViewChange: (value: 'cards' | 'list') => void
}) {
  const { t } = useTranslation()
  const [tag, setTag] = useState(value.tag ?? '')
  const selectClass = 'bg-muted/40 h-11 w-full rounded-md border-0 px-3 text-sm focus-visible:outline-2 focus-visible:outline-ring'
  const activeCount = [value.stock, value.tag, value.autoDelivery, value.aiProcessing, value.guestPurchase]
    .filter((item) => item !== undefined && item !== '').length
  function reset() {
    setTag('')
    onChange({ sort: 'comprehensive' })
  }
  return (
    <div className='space-y-5 py-5'>
      <div className='flex flex-wrap items-end justify-between gap-5'>
        <Field className='w-56 max-w-full'>
          <FieldLabel htmlFor='store-catalogue-sort'>{t('Sort products')}</FieldLabel>
          <select
            id='store-catalogue-sort'
            value={value.sort ?? 'comprehensive'}
            className={selectClass}
            onChange={(event) => onChange({ ...value, sort: event.target.value as StoreCatalogueFilters['sort'] })}
          >
            <option value='comprehensive'>{t('Comprehensive order')}</option>
            <option value='sales'>{t('Best selling')}</option>
            <option value='newest'>{t('Newest products')}</option>
          </select>
        </Field>
        <ToggleGroup
          value={[view]}
          onValueChange={(values) => {
            if (values[0] === 'cards' || values[0] === 'list') onViewChange(values[0])
          }}
          aria-label={t('Product view')}
        >
          <ToggleGroupItem value='cards'>{t('Cards')}</ToggleGroupItem>
          <ToggleGroupItem value='list'>{t('List')}</ToggleGroupItem>
        </ToggleGroup>
      </div>
      <details>
        <summary className='text-muted-foreground focus-visible:outline-ring w-fit cursor-pointer rounded-sm py-2 text-sm focus-visible:outline-2'>
          {t('Filters')}{activeCount > 0 && ` (${activeCount})`}
        </summary>
        <div className='space-y-5 pt-5'>
          <FieldGroup className='grid gap-5 sm:grid-cols-2 lg:grid-cols-4'>
            <Field>
              <FieldLabel htmlFor='store-catalogue-stock'>{t('Stock availability')}</FieldLabel>
              <select
                id='store-catalogue-stock'
                value={value.stock ?? ''}
                className={selectClass}
                onChange={(event) => onChange({
                  ...value,
                  stock: event.target.value === '' ? undefined : event.target.value as StoreCatalogueFilters['stock'],
                })}
              >
                <option value=''>{t('Any stock')}</option>
                <option value='in_stock'>{t('In stock')}</option>
                <option value='out_of_stock'>{t('Out of stock')}</option>
              </select>
            </Field>
            {([
              ['autoDelivery', 'Automatic delivery'],
              ['aiProcessing', 'AI processing'],
              ['guestPurchase', 'Guest purchase available'],
            ] as const).map(([key, label]) => (
              <Field key={key}>
                <FieldLabel htmlFor={`store-catalogue-${key}`}>{t(label)}</FieldLabel>
                <select
                  id={`store-catalogue-${key}`}
                  value={value[key] === undefined ? '' : String(value[key])}
                  className={selectClass}
                  onChange={(event) => onChange({
                    ...value,
                    [key]: event.target.value === '' ? undefined : event.target.value === 'true',
                  })}
                >
                  <option value=''>{t('Any')}</option>
                  <option value='true'>{t('Yes')}</option>
                  <option value='false'>{t('No')}</option>
                </select>
              </Field>
            ))}
          </FieldGroup>
          <form
            className='flex min-w-0 flex-wrap items-end gap-3'
            onSubmit={(event) => {
              event.preventDefault()
              onChange({ ...value, tag: tag.trim() || undefined })
            }}
          >
            <Field className='w-56 max-w-full'>
              <FieldLabel htmlFor='store-catalogue-tag'>{t('Seller tag')}</FieldLabel>
              <Input id='store-catalogue-tag' className='h-11' value={tag} onChange={(event) => setTag(event.target.value)} maxLength={128} />
            </Field>
            <Button type='submit' variant='secondary' className='h-11'>{t('Filter by tag')}</Button>
          </form>
        </div>
      </details>
      {activeCount > 0 && (
        <Button type='button' variant='ghost' className='h-11' onClick={reset}>{t('Reset filters')}</Button>
      )}
    </div>
  )
}
