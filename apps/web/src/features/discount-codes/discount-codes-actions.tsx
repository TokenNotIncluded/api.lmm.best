/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { Copy, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'

type DiscountCodesActionsProps = {
  selectedCount: number
  cleanupPending: boolean
  onRefresh: () => void
  onCopySelected: () => void
  onOpenCleanup: () => void
  onCreate: () => void
}

export function DiscountCodesActions({
  selectedCount,
  cleanupPending,
  onRefresh,
  onCopySelected,
  onOpenCleanup,
  onCreate,
}: DiscountCodesActionsProps) {
  const { t } = useTranslation()
  return (
    <div className='flex flex-wrap justify-end gap-2'>
      <Button
        variant='outline'
        size='sm'
        aria-label={t('Refresh')}
        title={t('Refresh')}
        onClick={onRefresh}
      >
        <RefreshCw className='size-4' />
        <span className='hidden sm:inline'>{t('Refresh')}</span>
      </Button>
      <Button
        variant='outline'
        size='sm'
        disabled={selectedCount === 0}
        aria-label={t('Copy selected links')}
        title={t('Copy selected links')}
        onClick={onCopySelected}
      >
        <Copy className='size-4' />
        <span className='hidden sm:inline'>
          {t('Copy selected links')} ({selectedCount})
        </span>
      </Button>
      <Button
        variant='outline'
        size='sm'
        disabled={cleanupPending}
        aria-label={t('Clear exhausted codes')}
        title={t('Clear exhausted codes')}
        onClick={onOpenCleanup}
      >
        <Trash2 className='size-4' />
        <span className='hidden sm:inline'>{t('Clear exhausted codes')}</span>
      </Button>
      <Button
        size='sm'
        aria-label={t('Create discount code')}
        title={t('Create discount code')}
        onClick={onCreate}
      >
        <Plus className='size-4' />
        <span className='hidden sm:inline'>{t('Create discount code')}</span>
      </Button>
    </div>
  )
}

type CleanupExhaustedCodesDialogProps = {
  open: boolean
  pending: boolean
  onOpenChange: (open: boolean) => void
  onConfirm: () => void
}

export function CleanupExhaustedCodesDialog({
  open,
  pending,
  onOpenChange,
  onConfirm,
}: CleanupExhaustedCodesDialogProps) {
  const { t } = useTranslation()
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Delete exhausted discount codes?')}
      desc={t(
        'This permanently removes every finite-use discount code whose usage limit has been reached. Partially used and unlimited codes are kept.'
      )}
      confirmText={t('Delete exhausted codes')}
      isLoading={pending}
      handleConfirm={onConfirm}
    />
  )
}
