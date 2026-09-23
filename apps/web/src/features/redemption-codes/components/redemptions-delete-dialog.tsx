/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { TriangleAlert } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Spinner } from '@/components/ui/spinner'

import { deleteRedemption } from '../api'
import { ERROR_MESSAGES, SUCCESS_MESSAGES } from '../constants'
import { useRedemptions } from './redemptions-provider'

export function RedemptionsDeleteDialog() {
  const { t } = useTranslation()
  const { open, setOpen, currentRow, triggerRefresh } = useRedemptions()
  const [isDeleting, setIsDeleting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const handleDelete = async () => {
    if (!currentRow) return

    setIsDeleting(true)
    setError(null)
    try {
      const result = await deleteRedemption(currentRow.id)
      if (result.success) {
        toast.success(t(SUCCESS_MESSAGES.REDEMPTION_DELETED))
        setOpen(null)
        triggerRefresh()
        return
      }
      // Surface the failure in the dialog instead of silently closing: a
      // swallowed error leaves the operator thinking the code was deleted.
      setError(result.message || t(ERROR_MESSAGES.DELETE_FAILED))
    } catch {
      setError(t(ERROR_MESSAGES.UNEXPECTED))
    } finally {
      setIsDeleting(false)
    }
  }

  return (
    <AlertDialog
      open={open === 'delete'}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) {
          setError(null)
          setOpen(null)
        }
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t('Are you sure?')}</AlertDialogTitle>
          <AlertDialogDescription>
            {t('This will permanently delete redemption code')}{' '}
            <span className='font-semibold'>{currentRow?.name}</span>
            {t('. This action cannot be undone.')}
          </AlertDialogDescription>
        </AlertDialogHeader>

        {error && (
          <div
            role='alert'
            className='border-destructive/30 bg-destructive/5 text-destructive flex items-start gap-2 rounded-lg border p-2.5 text-sm'
          >
            <TriangleAlert
              className='mt-0.5 size-4 shrink-0'
              aria-hidden='true'
            />
            <span className='min-w-0'>{error}</span>
          </div>
        )}

        <AlertDialogFooter>
          <AlertDialogCancel disabled={isDeleting}>
            {t('Cancel')}
          </AlertDialogCancel>
          <AlertDialogAction
            onClick={handleDelete}
            disabled={isDeleting}
            variant='destructive'
          >
            {isDeleting && <Spinner className='size-4' />}
            {isDeleting
              ? t('Deleting...')
              : error
                ? t('Try again')
                : t('Delete')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
