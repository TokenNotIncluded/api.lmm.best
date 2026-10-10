/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useTranslation } from 'react-i18next'

import {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverTitle,
  PopoverTrigger,
} from '@/components/ui/popover'
import { cn } from '@/lib/utils'

export function PlatformCreditHelp({ className }: { className?: string }) {
  const { t } = useTranslation()

  return (
    <Popover>
      <PopoverTrigger
        render={
          <button
            type='button'
            aria-label={t('Wallet balance')}
            className={cn(
              'text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-ring/60 pointer-events-auto inline-flex size-7 items-center justify-center rounded-full text-xs font-semibold leading-none outline-none focus-visible:ring-2',
              className
            )}
          />
        }
      >
        <sup aria-hidden='true'>?</sup>
      </PopoverTrigger>
      <PopoverContent
        side='top'
        align='start'
        collisionPadding={12}
        className='max-w-[calc(100vw-1.5rem)] gap-1.5 rounded-xl border p-3 shadow-lg'
      >
        <PopoverTitle className='text-sm font-semibold'>
          {t('Wallet balance')}
        </PopoverTitle>
        <PopoverDescription className='text-xs leading-5'>
          {t(
            'Your balance is stored in credits. Display currency does not change the amount charged at checkout.'
          )}
        </PopoverDescription>
      </PopoverContent>
    </Popover>
  )
}

export function PlatformCreditAmount({
  value,
  className,
}: {
  value: string
  className?: string
}) {
  if (value === '-') return <span className={className}>{value}</span>

  return (
    <span className={cn('inline-flex items-baseline', className)}>
      <span>{value}</span>
      <PlatformCreditHelp />
    </span>
  )
}
