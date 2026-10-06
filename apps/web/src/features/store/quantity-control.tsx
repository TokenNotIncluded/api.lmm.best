/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { MinusSignIcon, PlusSignIcon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from '@/components/ui/input-group'

import { storeQuantity } from './quantity'
import { STORE_QUANTITY_ERROR } from './quantity-copy'

export function StoreQuantityControl({
  value,
  max,
  disabled = false,
  onChange,
}: {
  value: string
  max: number
  disabled?: boolean
  onChange: (value: string) => void
}) {
  const { t } = useTranslation()
  const quantity = storeQuantity(value)
  const invalid = quantity === undefined || quantity > max
  return (
    <Field data-invalid={invalid && max > 0} data-disabled={disabled}>
      <FieldLabel htmlFor='store-quantity'>{t('Quantity')}</FieldLabel>
      <InputGroup className='h-11 rounded-md'>
        <InputGroupAddon align='inline-start'>
          <Button
            type='button'
            variant='ghost'
            size='icon'
            className='size-11'
            aria-label={t('Decrease quantity')}
            disabled={disabled || quantity === undefined || quantity <= 1}
            onClick={() => {
              if (quantity !== undefined) onChange(String(quantity - 1))
            }}
          >
            <HugeiconsIcon icon={MinusSignIcon} />
          </Button>
        </InputGroupAddon>
        <InputGroupInput
          id='store-quantity'
          className='h-11 text-center tabular-nums'
          type='text'
          inputMode='numeric'
          pattern='[1-9][0-9]*'
          role='spinbutton'
          aria-valuemin={1}
          aria-valuemax={max}
          aria-valuenow={quantity}
          value={value}
          disabled={disabled}
          aria-invalid={invalid && max > 0}
          aria-describedby='store-quantity-help'
          onChange={(event) => onChange(event.target.value)}
          onKeyDown={(event) => {
            if (
              quantity === undefined ||
              !['ArrowUp', 'ArrowDown'].includes(event.key)
            ) {
              return
            }
            event.preventDefault()
            const next = quantity + (event.key === 'ArrowUp' ? 1 : -1)
            if (next >= 1 && next <= max) onChange(String(next))
          }}
        />
        <InputGroupAddon align='inline-end'>
          <Button
            type='button'
            variant='ghost'
            size='icon'
            className='size-11'
            aria-label={t('Increase quantity')}
            disabled={disabled || quantity === undefined || quantity >= max}
            onClick={() => {
              if (quantity !== undefined) onChange(String(quantity + 1))
            }}
          >
            <HugeiconsIcon icon={PlusSignIcon} />
          </Button>
        </InputGroupAddon>
      </InputGroup>
      <FieldDescription id='store-quantity-help'>
        {t('Available to buy: {{count}}', { count: max })}
      </FieldDescription>
      {invalid && max > 0 && (
        <p role='alert' className='text-destructive text-xs'>
          {t(STORE_QUANTITY_ERROR, { max })}
        </p>
      )}
    </Field>
  )
}
