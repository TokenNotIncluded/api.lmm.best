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

export function StoreClaimItems({
  items,
  variantName,
  deliveryTemplate,
}: {
  items: string[]
  variantName?: string
  deliveryTemplate?: string
}) {
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
          const itemId = `pickup-item-${index}`
          const selectionId = `${itemId}-selected`
          return (
            <div key={index} className='flex min-w-0 flex-col gap-3'>
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
                  {t('Select item {{number}}', { number: index + 1 })}
                </FieldLabel>
              </Field>
              <StoreDeliveredItem
                raw={item}
                template={deliveryTemplate}
                index={index}
                variantName={variantName}
              />
            </div>
          )
        })}
      </FieldGroup>
    </FieldSet>
  )
}
