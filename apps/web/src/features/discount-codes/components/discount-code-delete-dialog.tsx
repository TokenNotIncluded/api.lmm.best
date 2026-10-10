/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'

import type { DiscountCode } from '../types'

interface DiscountCodeDeleteDialogProps {
  row: DiscountCode | null
  pending: boolean
  onOpenChange: (open: boolean) => void
  onConfirm: () => void
}

/**
 * Replaces `window.confirm`, which could not state what deletion destroys:
 * the code stops working for anyone holding a distributed share link.
 */
export function DiscountCodeDeleteDialog({
  row,
  pending,
  onOpenChange,
  onConfirm,
}: DiscountCodeDeleteDialogProps) {
  const { t } = useTranslation()

  return (
    <ConfirmDialog
      open={row !== null}
      onOpenChange={onOpenChange}
      destructive
      isLoading={pending}
      title={t('Delete this discount code?')}
      desc={
        <>
          <span className='font-mono font-semibold'>{row?.code}</span>
          <br />
          {t(
            'Anyone holding a shared link for this code can no longer redeem it.'
          )}
          <br />
          {t('This action cannot be undone.')}
        </>
      }
      confirmText={pending ? t('Deleting...') : t('Delete')}
      handleConfirm={onConfirm}
    />
  )
}
