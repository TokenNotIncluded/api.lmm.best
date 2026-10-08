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
  const selectClass = 'bg-background h-11 w-full rounded-md border px-3 text-sm'
  return (
    <div className='space-y-4'>
      <div className='flex flex-wrap items-end justify-between gap-4'>
        <div className='w-56 max-w-full'>
          {' '}
          <Field>
            <FieldLabel htmlFor='store-catalogue-sort'>
              {t('Sort products')}
            </FieldLabel>
            <select
              id='store-catalogue-sort'
              value={value.sort ?? 'comprehensive'}
              className={selectClass}
              onChange={(event) =>
                onChange({
                  ...value,
                  sort: event.target.value as StoreCatalogueFilters['sort'],
                })
              }
            >
              <option value='comprehensive'>{t('Comprehensive order')}</option>
              <option value='sales'>{t('Best selling')}</option>
              <option value='newest'>{t('Newest products')}</option>
            </select>
          </Field>
        </div>
        <ToggleGroup
          value={[view]}
          onValueChange={(values) => {
            if (values[0] === 'cards' || values[0] === 'list') {
              onViewChange(values[0])
            }
          }}
          variant='default'
          aria-label={t('Product view')}
        >
          <ToggleGroupItem value='cards'>{t('Cards')}</ToggleGroupItem>
          <ToggleGroupItem value='list'>{t('List')}</ToggleGroupItem>
        </ToggleGroup>
      </div>
      <details className='group'>
        <summary className='text-muted-foreground hover:text-foreground cursor-pointer py-2 text-sm'>
          {t('More filters')}
          {Object.entries(value).filter(
            ([key, item]) => key !== 'sort' && item !== undefined && item !== ''
          ).length > 0 && (
            <span className='text-foreground ms-2'>{t('Filters active')}</span>
          )}
        </summary>
        <div className='space-y-4 pt-4'>
          <FieldGroup className='grid gap-3 sm:grid-cols-2 lg:grid-cols-4'>
            <Field>
              <FieldLabel htmlFor='store-catalogue-stock'>
                {t('Stock availability')}
              </FieldLabel>
              <select
                id='store-catalogue-stock'
                value={value.stock ?? ''}
                className={selectClass}
                onChange={(event) =>
                  onChange({
                    ...value,
                    stock:
                      event.target.value === ''
                        ? undefined
                        : (event.target
                            .value as StoreCatalogueFilters['stock']),
                  })
                }
              >
                <option value=''>{t('Any stock')}</option>
                <option value='in_stock'>{t('In stock')}</option>
                <option value='out_of_stock'>{t('Out of stock')}</option>
              </select>
            </Field>
            {(
              [
                ['autoDelivery', 'Automatic delivery'],
                ['aiProcessing', 'AI processing'],
                ['guestPurchase', 'Guest purchase available'],
              ] as const
            ).map(([key, label]) => (
              <Field key={key}>
                <FieldLabel htmlFor={`store-catalogue-${key}`}>
                  {t(label)}
                </FieldLabel>
                <select
                  id={`store-catalogue-${key}`}
                  value={value[key] === undefined ? '' : String(value[key])}
                  className={selectClass}
                  onChange={(event) =>
                    onChange({
                      ...value,
                      [key]:
                        event.target.value === ''
                          ? undefined
                          : event.target.value === 'true',
                    })
                  }
                >
                  <option value=''>{t('Any')}</option>
                  <option value='true'>{t('Yes')}</option>
                  <option value='false'>{t('No')}</option>
                </select>
              </Field>
            ))}
          </FieldGroup>
          <div className='flex flex-wrap items-end justify-between gap-3'>
            <form
              className='flex min-w-0 flex-wrap items-end gap-2'
              onSubmit={(event) => {
                event.preventDefault()
                onChange({ ...value, tag: tag.trim() || undefined })
              }}
            >
              <Field className='w-56 max-w-full'>
                <FieldLabel htmlFor='store-catalogue-tag'>
                  {t('Seller tag')}
                </FieldLabel>
                <Input
                  id='store-catalogue-tag'
                  className='h-11'
                  value={tag}
                  onChange={(event) => setTag(event.target.value)}
                  maxLength={128}
                />
              </Field>
              <Button type='submit' variant='outline' className='h-11'>
                {t('Filter by tag')}
              </Button>
              <Button
                type='button'
                variant='ghost'
                className='h-11'
                onClick={() => {
                  setTag('')
                  onChange({ sort: 'comprehensive' })
                }}
              >
                {t('Reset filters')}
              </Button>
            </form>
          </div>
        </div>
      </details>
    </div>
  )
}
