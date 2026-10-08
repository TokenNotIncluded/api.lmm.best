/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { LayoutGrid, List, SlidersHorizontal } from 'lucide-react'
import { useId, useState, type ReactNode } from 'react'
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
  children,
  supported = true,
  extraActive = false,
}: {
  value: StoreCatalogueFilters
  onChange: (value: StoreCatalogueFilters) => void
  view: 'cards' | 'list'
  onViewChange: (value: 'cards' | 'list') => void
  children?: ReactNode
  supported?: boolean
  extraActive?: boolean
}) {
  const { t } = useTranslation()
  const [tag, setTag] = useState(value.tag ?? '')
  const [open, setOpen] = useState(false)
  const panelId = useId()
  const active =
    extraActive ||
    Object.entries(value).some(
      ([key, item]) => key !== 'sort' && item !== undefined && item !== ''
    )
  const selectClass =
    'bg-background h-11 w-full rounded-xl border px-3 text-base sm:text-sm'
  return (
    <div className='flex flex-col gap-3'>
      <div className='flex items-center gap-1.5 sm:gap-3'>
        {supported && (
          <>
            <label htmlFor='store-catalogue-sort' className='sr-only'>
              {t('Sort products')}
            </label>
            <select
              id='store-catalogue-sort'
              value={value.sort ?? 'comprehensive'}
              className='text-foreground h-11 min-w-0 flex-1 rounded-lg bg-transparent pe-3 text-base sm:max-w-52 sm:text-sm'
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
            <ToggleGroup
              value={[view]}
              onValueChange={(values) => {
                if (values[0] === 'cards' || values[0] === 'list') {
                  onViewChange(values[0])
                }
              }}
              variant='default'
              aria-label={t('Product view')}
              className='ms-auto shrink-0 gap-0 rounded-full'
            >
              <ToggleGroupItem
                value='cards'
                aria-label={t('Cards')}
                className='size-11 rounded-full!'
              >
                <LayoutGrid className='size-4' aria-hidden='true' />
                <span className='sr-only'>{t('Cards')}</span>
              </ToggleGroupItem>
              <ToggleGroupItem
                value='list'
                aria-label={t('List')}
                className='size-11 rounded-full!'
              >
                <List className='size-4' aria-hidden='true' />
                <span className='sr-only'>{t('List')}</span>
              </ToggleGroupItem>
            </ToggleGroup>
          </>
        )}
        <Button
          type='button'
          variant='ghost'
          className='relative ms-auto size-11 shrink-0 rounded-full sm:ms-0 sm:w-auto sm:gap-2 sm:px-4'
          aria-label={t('More filters')}
          aria-expanded={open}
          aria-controls={panelId}
          onClick={() => setOpen((value) => !value)}
        >
          <SlidersHorizontal className='size-4' aria-hidden='true' />
          <span className='sr-only sm:not-sr-only'>{t('More filters')}</span>
          {active && (
            <span className='bg-primary absolute end-1 top-1 size-1.5 rounded-full'>
              <span className='sr-only'>{t('Filters active')}</span>
            </span>
          )}
        </Button>
      </div>
      <div id={panelId} hidden={!open}>
        <div className='bg-muted/40 flex flex-col gap-5 rounded-2xl p-4 sm:p-5'>
          {children}
          {supported && (
            <>
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
                  <Field className='w-full sm:w-56'>
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
            </>
          )}
        </div>
      </div>
    </div>
  )
}
