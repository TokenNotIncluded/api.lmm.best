/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import { analyzeInventoryItems } from './inventory-import-analysis'

export function StoreInventoryImportPreview({
  items,
  target,
  disabled,
  onRemoveDuplicates,
}: {
  items: readonly string[]
  target: string
  disabled: boolean
  onRemoveDuplicates: (items: string[]) => void
}) {
  const { t } = useTranslation()
  const { count, repeated, unique } = analyzeInventoryItems(items)
  return (
    <div className='bg-muted/40 space-y-2 rounded-md p-3 text-sm'>
      <p className='font-medium break-words'>
        {t('Import into variant: {{variant}}', { variant: target })}
      </p>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <p className='text-muted-foreground text-xs' aria-live='polite'>
          {t('{{count}} items ready to import', { count })}
          {repeated > 0 && (
            <>
              {' '}
              ·{' '}
              {t('{{count}} exact duplicates in this import', {
                count: repeated,
              })}
            </>
          )}
        </p>
        {repeated > 0 && (
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={disabled}
            onClick={() => onRemoveDuplicates(unique)}
          >
            {t('Remove exact duplicates')}
          </Button>
        )}
      </div>
    </div>
  )
}
