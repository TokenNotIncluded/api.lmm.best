/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useState, type ComponentProps } from 'react'

import { Input } from '@/components/ui/input'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import { isCreditAmount } from '@/lib/quota-input'

/** The controlled value is always raw credit, never a display currency. */
export function CreditAmountInput({
  value,
  onValueChange,
  allowNegative = false,
  ...props
}: Omit<
  ComponentProps<typeof Input>,
  'value' | 'onChange' | 'type' | 'step' | 'min'
> & {
  value: number | undefined
  onValueChange: (credits: number) => void
  allowNegative?: boolean
}) {
  const display = useWalletCurrency()
  const signature = JSON.stringify([display.currency, display.config])
  const [edit, setEdit] = useState<{
    text: string
    credits: number
    signature: string
  }>()
  const text =
    edit && Object.is(edit.credits, value) && edit.signature === signature
      ? edit.text
      : isCreditAmount(value, allowNegative)
        ? display.quotaToInput(value)
        : ''

  return (
    <Input
      {...props}
      type='number'
      min={allowNegative ? undefined : 0}
      step={display.step}
      value={text}
      aria-invalid={
        props['aria-invalid'] ||
        (value !== undefined && !isCreditAmount(value, allowNegative))
      }
      onChange={(event) => {
        const text = event.target.value
        const credits =
          display.currency === 'CREDIT' && !Number.isSafeInteger(Number(text))
            ? Number.NaN
            : display.amountToQuota(text)
        setEdit({
          text,
          credits: isCreditAmount(credits, allowNegative)
            ? credits
            : Number.NaN,
          signature,
        })
        onValueChange(
          isCreditAmount(credits, allowNegative) ? credits : Number.NaN
        )
      }}
    />
  )
}
