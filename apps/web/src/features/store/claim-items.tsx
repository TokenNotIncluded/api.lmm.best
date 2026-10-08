/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Field,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'

import { StoreDeliveredItem } from './delivered-item'
import { deliveryItemText } from './delivery-template'
import { CopyStoreValue } from './shared'

interface StoreClaimItemsProps {
  items: string[]
  itemStockIds?: string[]
  itemPositions?: number[]
  variantName?: string
  deliveryTemplate?: string
}

export function StoreClaimItems(props: StoreClaimItemsProps) {
  const positions =
    props.itemPositions?.length === props.items.length &&
    props.itemPositions.every(
      (value) => Number.isSafeInteger(value) && value > 0
    ) &&
    new Set(props.itemPositions).size === props.items.length
      ? props.itemPositions
      : props.items.map((_, index) => index + 1)
  const stockIds =
    props.itemStockIds?.length === props.items.length &&
    props.itemStockIds.every(
      (value) => typeof value === 'string' && value.length > 0
    ) &&
    new Set(props.itemStockIds).size === props.items.length
      ? props.itemStockIds
      : undefined
  return (
    <StoreClaimSelection
      key={JSON.stringify([stockIds, positions])}
      {...props}
      positions={positions}
      stockIds={stockIds}
    />
  )
}

function StoreClaimSelection({
  items,
  variantName,
  deliveryTemplate,
  positions,
  stockIds,
}: StoreClaimItemsProps & { positions: number[]; stockIds?: string[] }) {
  const { t } = useTranslation()
  const [selected, setSelected] = useState<Set<number>>(() => new Set())
  const selectedItems = items.filter((_, index) => selected.has(index))
  return (
    <FieldSet className='gap-4'>
      <FieldLegend>{t('{{count}} items', { count: items.length })}</FieldLegend>
      <div className='flex flex-wrap items-center gap-2'>
        <Button
          type='button'
          size='sm'
          variant='outline'
          disabled={!items.length}
          onClick={() => setSelected(new Set(items.map((_, index) => index)))}
        >
          {t('Select all')}
        </Button>
        <Button
          type='button'
          size='sm'
          variant='outline'
          disabled={!items.length}
          onClick={() =>
            setSelected(
              new Set(
                items.flatMap((_, index) =>
                  selected.has(index) ? [] : [index]
                )
              )
            )
          }
        >
          {t('Invert selection')}
        </Button>
        <CopyStoreValue
          value={selectedItems
            .map((item) => deliveryItemText(item, deliveryTemplate, t))
            .join('\n')}
          label='Copy selected items'
          disabled={!selectedItems.length}
        />
        <CopyStoreValue
          value={items
            .map((item) => deliveryItemText(item, deliveryTemplate, t))
            .join('\n')}
          label='Copy all items'
          disabled={!items.length}
        />
        <span aria-live='polite' className='text-muted-foreground text-sm'>
          {t('{{count}} selected', { count: selectedItems.length })}
        </span>
      </div>
      <FieldGroup className='gap-4'>
        {items.map((item, index) => {
          const position = positions[index]
          const itemId = `pickup-item-${position - 1}`
          const selectionId = `${itemId}-selected`
          return (
            <div
              key={stockIds?.[index] || position}
              className='flex min-w-0 flex-col gap-3'
            >
              <Field orientation='horizontal'>
                <Checkbox
                  id={selectionId}
                  checked={selected.has(index)}
                  onCheckedChange={(checked) => {
                    setSelected((previous) => {
                      const next = new Set(previous)
                      if (checked) next.add(index)
                      else next.delete(index)
                      return next
                    })
                  }}
                />
                <FieldLabel htmlFor={selectionId}>
                  {t('Select item {{number}}', { number: position })}
                </FieldLabel>
              </Field>
              <StoreDeliveredItem
                raw={item}
                template={deliveryTemplate}
                index={position - 1}
                variantName={variantName}
              />
            </div>
          )
        })}
      </FieldGroup>
    </FieldSet>
  )
}
