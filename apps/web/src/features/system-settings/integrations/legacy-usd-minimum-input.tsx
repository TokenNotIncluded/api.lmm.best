/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useEffect, useRef, useState, type ComponentProps } from 'react'
import { useTranslation } from 'react-i18next'

import { Input } from '@/components/ui/input'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import {
  legacyMinimumToUsdInput,
  usdToLegacyMinimum,
} from '@/lib/payment-pricing'

type LegacyUsdMinimumInputProps = Omit<
  ComponentProps<typeof Input>,
  'value' | 'onChange' | 'type' | 'step'
> & {
  value: number
  onChange: (legacyAmount: number) => void
}

/** Shows actual USD while retaining the gateway's existing integer storage contract. */
export function LegacyUsdMinimumInput({
  value,
  onChange,
  disabled,
  ...props
}: LegacyUsdMinimumInputProps) {
  const { t } = useTranslation()
  const { config } = useWalletCurrency()
  const projection = legacyMinimumToUsdInput(value, config)
  const available = legacyMinimumToUsdInput(0, config) !== ''
  const [draft, setDraft] = useState(projection)
  const lastEmitted = useRef<number | undefined>(undefined)
  const original = useRef({ value, projection })
  const previousProjection = useRef(projection)

  useEffect(() => {
    if (
      Object.is(value, lastEmitted.current) &&
      projection === previousProjection.current
    ) {
      return
    }
    previousProjection.current = projection
    if (Object.is(value, lastEmitted.current) && Number.isNaN(value)) return
    original.current = { value, projection }
    setDraft(projection)
  }, [value, projection])

  return (
    <div className='space-y-2'>
      <Input
        {...props}
        type='number'
        step='any'
        min={0}
        disabled={disabled || !available}
        value={draft}
        onChange={(event) => {
          const next = event.target.value
          setDraft(next)
          const legacy =
            next === original.current.projection &&
            Number.isSafeInteger(original.current.value)
              ? original.current.value
              : usdToLegacyMinimum(next, config)
          lastEmitted.current = legacy
          previousProjection.current = legacyMinimumToUsdInput(legacy, config)
          onChange(legacy)
        }}
      />
      <p className='text-muted-foreground text-xs'>
        {available
          ? t(
              'The stored minimum uses whole recharge increments. Enter a USD amount that equals a whole increment.'
            )
          : t(
              'The fixed Credit denomination is unavailable. This minimum cannot be edited yet.'
            )}
      </p>
      {available && Number.isNaN(value) ? (
        <p role='alert' className='text-destructive text-xs'>
          {t('Enter a USD amount that equals a whole recharge increment.')}
        </p>
      ) : null}
    </div>
  )
}
