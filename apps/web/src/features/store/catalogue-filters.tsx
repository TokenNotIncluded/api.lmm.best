/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { ArrowDownUp, LayoutGrid, List, SlidersHorizontal } from 'lucide-react'
import { useId, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { cn } from '@/lib/utils'

import type { StoreCatalogueFilters } from './catalogue-types'

function CatalogueSelect({
  id,
  label,
  value,
  items,
  onChange,
  className,
  icon,
}: {
  id: string
  label: string
  value: string
  items: { value: string; label: string }[]
  onChange: (value: string) => void
  className?: string
  icon?: ReactNode
}) {
  return (
    <Select
      items={items}
      value={value}
      onValueChange={(next) => {
        if (next !== null) onChange(next)
      }}
    >
      <SelectTrigger
        id={id}
        aria-label={label}
        className={cn(
          'border-border/70 bg-background text-foreground w-full min-w-0 gap-2 rounded-xl px-3 text-base shadow-xs hover:bg-accent/60 data-popup-open:border-ring/60 data-popup-open:bg-accent/60 data-[size=default]:h-11 dark:bg-background dark:hover:bg-accent/60 sm:text-sm',
          className
        )}
      >
        {icon}
        <SelectValue className='min-w-0 truncate' />
      </SelectTrigger>
      <SelectContent
        align='start'
        alignItemWithTrigger={false}
        sideOffset={8}
        className='rounded-2xl p-1.5'
      >
        {items.map((item) => (
          <SelectItem
            key={item.value}
            value={item.value}
            className='min-h-11 rounded-xl data-selected:bg-accent/60 data-highlighted:bg-accent data-highlighted:text-accent-foreground [&_[data-slot=select-item-text]]:min-w-0 [&_[data-slot=select-item-text]]:whitespace-normal'
          >
            {item.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

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
  return (
    <div className='flex flex-col gap-3'>
      <div className='flex items-center gap-1.5 sm:gap-3'>
        {supported && (
          <>
            <CatalogueSelect
              id='store-catalogue-sort'
              label={t('Sort products')}
              value={value.sort ?? 'comprehensive'}
              className='flex-1 sm:max-w-52'
              icon={
                <ArrowDownUp
                  className='text-muted-foreground size-4'
                  aria-hidden='true'
                />
              }
              items={[
                { value: 'comprehensive', label: t('Comprehensive order') },
                { value: 'sales', label: t('Best selling') },
                { value: 'newest', label: t('Newest products') },
              ]}
              onChange={(sort) =>
                onChange({
                  ...value,
                  sort: sort as StoreCatalogueFilters['sort'],
                })
              }
            />
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
                  <CatalogueSelect
                    id='store-catalogue-stock'
                    label={t('Stock availability')}
                    value={value.stock ?? ''}
                    items={[
                      { value: '', label: t('Any stock') },
                      { value: 'in_stock', label: t('In stock') },
                      { value: 'out_of_stock', label: t('Out of stock') },
                    ]}
                    onChange={(stock) =>
                      onChange({
                        ...value,
                        stock: stock
                          ? (stock as StoreCatalogueFilters['stock'])
                          : undefined,
                      })
                    }
                  />
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
                    <CatalogueSelect
                      id={`store-catalogue-${key}`}
                      label={t(label)}
                      value={value[key] === undefined ? '' : String(value[key])}
                      items={[
                        { value: '', label: t('Any') },
                        { value: 'true', label: t('Yes') },
                        { value: 'false', label: t('No') },
                      ]}
                      onChange={(next) =>
                        onChange({
                          ...value,
                          [key]: next === '' ? undefined : next === 'true',
                        })
                      }
                    />
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
