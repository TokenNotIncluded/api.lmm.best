/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import { storeApi } from './api'
import { useStoreCategories } from './category-support'
import { StoreError, StoreLoading } from './shared'
import type { StoreCategory, StoreCategoryInput, StoreProduct } from './types'

export function StoreCategorySelect({
  id,
  value,
  items,
  current,
  onChange,
  disabled = false,
  all = false,
}: {
  id: string
  value: string
  items: StoreCategory[]
  current?: StoreProduct['category']
  onChange: (value: string) => void
  disabled?: boolean
  all?: boolean
}) {
  const { t } = useTranslation()
  const retained = current && !items.some((item) => item.id === current.id)
  return (
    <Field data-disabled={disabled}>
      <FieldLabel htmlFor={id}>{t('Product category')}</FieldLabel>
      <NativeSelect
        id={id}
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
      >
        <NativeSelectOption value=''>
          {t(all ? 'All categories' : 'Uncategorized')}
        </NativeSelectOption>
        {retained && (
          <NativeSelectOption value={current.id} disabled>
            {t('{{name}} (inactive)', { name: current.name })}
          </NativeSelectOption>
        )}
        {items.map((item) => (
          <NativeSelectOption key={item.id} value={item.id}>
            {item.name}
          </NativeSelectOption>
        ))}
      </NativeSelect>
    </Field>
  )
}

export function StoreProductCategoryEditor({
  product,
}: {
  product: StoreProduct
}) {
  const { t } = useTranslation()
  const query = useStoreCategories(true)
  const client = useQueryClient()
  const [value, setValue] = useState(product.category_id ?? '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  async function save(event: React.FormEvent) {
    event.preventDefault()
    if (busy || value === (product.category_id ?? '')) return
    setBusy(true)
    setError(null)
    try {
      await storeApi.productCategory(product.id, value)
      await client.invalidateQueries({ queryKey: ['store'] })
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(false)
    }
  }
  return (
    <form onSubmit={(event) => void save(event)} className='max-w-lg space-y-2'>
      <StoreError
        error={error || query.error}
        retry={() => void query.refetch()}
      />
      {query.data?.supported && (
        <div className='flex flex-wrap items-end gap-2'>
          <StoreCategorySelect
            id={`store-category-${product.id}`}
            value={value}
            items={query.data.items}
            current={product.category}
            disabled={busy}
            onChange={setValue}
          />
          <Button
            type='submit'
            size='sm'
            variant='outline'
            disabled={busy || value === (product.category_id ?? '')}
          >
            {t('Save category')}
          </Button>
        </div>
      )}
    </form>
  )
}

export function StoreCategoriesManager() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const query = useQuery({
    queryKey: ['store', 'admin-categories'],
    queryFn: storeApi.adminCategories,
    retry: false,
  })
  async function refresh() {
    await client.invalidateQueries({ queryKey: ['store'] })
  }
  return (
    <section className='space-y-4 rounded-lg border p-4'>
      <h2 className='font-semibold'>{t('Store categories')}</h2>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Create categories for all sellers. Disabling a category keeps its existing products and labels.'
        )}
      </p>
      <StoreError error={query.error} retry={() => void query.refetch()} />
      {query.isPending ? (
        <StoreLoading />
      ) : (
        query.data?.supported && (
          <div className='space-y-4'>
            <StoreCategoryForm onSaved={refresh} />
            {query.data.items.map((category) => (
              <StoreCategoryForm
                key={`${category.id}-${category.updated_at}`}
                category={category}
                onSaved={refresh}
              />
            ))}
          </div>
        )
      )}
    </section>
  )
}

function StoreCategoryForm({
  category,
  onSaved,
}: {
  category?: StoreCategory
  onSaved: () => Promise<void>
}) {
  const { t } = useTranslation()
  const [name, setName] = useState(category?.name ?? '')
  const [sortOrder, setSortOrder] = useState(String(category?.sort_order ?? 0))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const prefix = `store-category-admin-${category?.id ?? 'new'}`
  async function save(
    event?: React.FormEvent,
    active = category?.active ?? true
  ) {
    event?.preventDefault()
    if (busy) return
    const order = Number(sortOrder)
    if (
      !name.trim() ||
      !Number.isSafeInteger(order) ||
      Math.abs(order) > 1_000_000
    ) {
      return
    }
    setBusy(true)
    setError(null)
    try {
      const body: StoreCategoryInput = {
        name: name.trim(),
        sort_order: order,
        active,
      }
      if (category) await storeApi.updateCategory(category.id, body)
      else await storeApi.createCategory(body)
      if (!category) {
        setName('')
        setSortOrder('0')
      }
      await onSaved()
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(false)
    }
  }
  return (
    <form
      onSubmit={(event) => void save(event)}
      className='space-y-2 border-t pt-3'
    >
      <FieldGroup className='flex-row flex-wrap items-end gap-3'>
        <Field className='min-w-40 flex-1' data-disabled={busy}>
          <FieldLabel htmlFor={`${prefix}-name`}>
            {t('Category name')}
          </FieldLabel>
          <Input
            id={`${prefix}-name`}
            value={name}
            required
            maxLength={320}
            disabled={busy}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <Field className='w-28' data-disabled={busy}>
          <FieldLabel htmlFor={`${prefix}-order`}>{t('Sort order')}</FieldLabel>
          <Input
            id={`${prefix}-order`}
            type='number'
            min={-1_000_000}
            max={1_000_000}
            step={1}
            value={sortOrder}
            required
            disabled={busy}
            onChange={(event) => setSortOrder(event.target.value)}
          />
        </Field>
        <Button type='submit' disabled={busy}>
          {t(category ? 'Save' : 'Add category')}
        </Button>
        {category && (
          <Button
            type='button'
            variant='outline'
            disabled={busy}
            onClick={() => void save(undefined, !category.active)}
          >
            {t(category.active ? 'Disable category' : 'Enable category')}
          </Button>
        )}
      </FieldGroup>
      {category && !category.active && (
        <FieldDescription>{t('Inactive category')}</FieldDescription>
      )}
      <StoreError error={error} />
    </form>
  )
}
