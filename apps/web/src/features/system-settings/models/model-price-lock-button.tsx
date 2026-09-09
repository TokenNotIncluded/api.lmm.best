/*
Copyright (C) 2026 LIghtJUNction
*/

import { Lock, LockOpen } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

export function ModelPriceLockButton({
  locked,
  disabled,
  onToggle,
}: {
  locked: boolean
  disabled?: boolean
  onToggle: () => void
}) {
  const { t } = useTranslation()
  const label = t(locked ? 'Unlock model prices' : 'Lock model prices')
  return (
    <Button
      type='button'
      variant='ghost'
      size='icon'
      className='shrink-0'
      aria-label={label}
      aria-pressed={locked}
      title={label}
      disabled={disabled}
      onClick={(event) => {
        event.stopPropagation()
        onToggle()
      }}
    >
      {locked ? <Lock className='text-amber-600' /> : <LockOpen />}
    </Button>
  )
}
