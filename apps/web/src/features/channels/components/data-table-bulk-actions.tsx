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
import { useQueryClient } from '@tanstack/react-query'
import type { Table } from '@tanstack/react-table'
import {
  CircleAlert,
  CircleCheck,
  Power,
  PowerOff,
  Tag,
  Trash2,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTableBulkActions as BulkActionsToolbar } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  hasPermission,
} from '@/lib/admin-permissions'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import {
  handleBatchDelete,
  handleBatchDisable,
  handleBatchEnable,
  handleBatchSetTag,
} from '../lib'
import type { Channel } from '../types'

interface DataTableBulkActionsProps<TData> {
  table: Table<TData>
}

/** Which bulk action is awaiting its second confirmation. */
type PendingBulkAction = 'enable' | 'disable' | null

export function DataTableBulkActions<TData>({
  table,
}: DataTableBulkActionsProps<TData>) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [showTagDialog, setShowTagDialog] = useState(false)
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false)
  const [pendingAction, setPendingAction] = useState<PendingBulkAction>(null)
  const [tagValue, setTagValue] = useState('')
  const currentUser = useAuthStore((s) => s.auth.user)
  const canEditSensitive = hasPermission(
    currentUser,
    ADMIN_PERMISSION_RESOURCES.CHANNEL,
    ADMIN_PERMISSION_ACTIONS.SENSITIVE_WRITE
  )

  const selectedRows = table.getFilteredSelectedRowModel().rows
  const selectedIds = selectedRows.reduce<number[]>((ids, row) => {
    const id = (row.original as Channel).id

    if (typeof id === 'number') {
      ids.push(id)
    }

    return ids
  }, [])

  const handleClearSelection = () => {
    table.resetRowSelection()
  }

  // Both bulk status flips change live traffic routing, so they are gated
  // behind an explicit confirmation naming the exact channel count.
  const handleEnableAll = () => {
    handleBatchEnable(selectedIds, queryClient, () => {
      setPendingAction(null)
      handleClearSelection()
    })
  }

  const handleDisableAll = () => {
    handleBatchDisable(selectedIds, queryClient, () => {
      setPendingAction(null)
      handleClearSelection()
    })
  }

  const handleDeleteAll = () => {
    if (!canEditSensitive) return
    handleBatchDelete(selectedIds, queryClient, () => {
      setShowDeleteConfirm(false)
      handleClearSelection()
    })
  }

  const handleSetTag = () => {
    handleBatchSetTag(selectedIds, tagValue || null, queryClient, () => {
      setShowTagDialog(false)
      setTagValue('')
      handleClearSelection()
    })
  }

  const confirmPendingAction = () => {
    if (pendingAction === 'enable') {
      handleEnableAll()
    } else if (pendingAction === 'disable') {
      handleDisableAll()
    }
  }

  return (
    <>
      <BulkActionsToolbar table={table} entityName='channel'>
        <Button
          variant='outline'
          size='sm'
          onClick={() => setPendingAction('enable')}
          aria-label={t('Enable selected channels')}
          className='h-8 gap-1.5 px-2.5 max-lg:h-11 max-lg:px-3.5'
          title={t('Enable selected channels')}
        >
          <Power data-icon='inline-start' />
          <span className='max-lg:text-sm'>
            {t('Enable selected channels')}
          </span>
        </Button>

        <Button
          variant='outline'
          size='sm'
          onClick={() => setPendingAction('disable')}
          aria-label={t('Disable selected channels')}
          className='h-8 gap-1.5 px-2.5 max-lg:h-11 max-lg:px-3.5'
          title={t('Disable selected channels')}
        >
          <PowerOff data-icon='inline-start' />
          <span className='max-lg:text-sm'>
            {t('Disable selected channels')}
          </span>
        </Button>

        <Button
          variant='outline'
          size='sm'
          onClick={() => setShowTagDialog(true)}
          aria-label={t('Set tag for selected channels')}
          className='h-8 gap-1.5 px-2.5 max-lg:h-11 max-lg:px-3.5'
          title={t('Set tag for selected channels')}
        >
          <Tag data-icon='inline-start' />
          <span className='max-lg:text-sm'>
            {t('Set tag for selected channels')}
          </span>
        </Button>

        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='destructive'
                size='sm'
                onClick={() => {
                  if (!canEditSensitive) return
                  setShowDeleteConfirm(true)
                }}
                aria-disabled={!canEditSensitive}
                className={cn(
                  'h-8 gap-1.5 px-2.5 max-lg:h-11 max-lg:px-3.5',
                  !canEditSensitive && 'cursor-not-allowed opacity-50'
                )}
                aria-label={t('Delete selected channels')}
                title={
                  canEditSensitive
                    ? t('Delete selected channels')
                    : t('No permission to perform this action')
                }
              />
            }
          >
            <Trash2 data-icon='inline-start' />
            <span className='max-lg:text-sm'>
              {t('Delete selected channels')}
            </span>
          </TooltipTrigger>
          <TooltipContent>
            <p>
              {canEditSensitive
                ? t('Delete selected channels')
                : t('No permission to perform this action')}
            </p>
          </TooltipContent>
        </Tooltip>
      </BulkActionsToolbar>

      {/* Bulk status change confirmation */}
      <Dialog
        open={pendingAction !== null}
        onOpenChange={(open) => {
          if (!open) setPendingAction(null)
        }}
        title={
          pendingAction === 'enable'
            ? t('Enable selected channels?')
            : t('Disable selected channels?')
        }
        description={
          <>
            {pendingAction === 'enable'
              ? t('Are you sure you want to enable')
              : t('Are you sure you want to disable')}
            {selectedIds.length}{' '}
            {pendingAction === 'enable'
              ? t('channel(s)? New traffic can be routed to them immediately.')
              : t(
                  'channel(s)? Requests will stop being routed to them immediately.'
                )}
          </>
        }
        contentHeight='auto'
        footer={
          <>
            <Button variant='outline' onClick={() => setPendingAction(null)}>
              {t('Cancel')}
            </Button>
            <Button
              variant={pendingAction === 'enable' ? 'default' : 'destructive'}
              onClick={confirmPendingAction}
            >
              {pendingAction === 'enable' ? t('Enable') : t('Disable')}
            </Button>
          </>
        }
      >
        <div className='text-muted-foreground flex items-center gap-2 text-sm'>
          {pendingAction === 'enable' ? (
            <CircleCheck className='size-4 shrink-0' />
          ) : (
            <CircleAlert className='size-4 shrink-0' />
          )}
          <span>
            {pendingAction === 'enable'
              ? t('Enabled channels accept requests as soon as saved.')
              : t('Disabled channels reject new requests until re-enabled.')}
          </span>
        </div>
      </Dialog>

      {/* Set Tag Dialog */}
      <Dialog
        open={showTagDialog}
        onOpenChange={setShowTagDialog}
        title={t('Set Tag')}
        description={
          <>
            {t('Set a tag for')}
            {selectedIds.length}{' '}
            {t('selected channel(s). Leave empty to remove tag.')}
          </>
        }
        contentHeight='auto'
        bodyClassName='space-y-4'
        footer={
          <>
            <Button
              variant='outline'
              onClick={() => {
                setShowTagDialog(false)
                setTagValue('')
              }}
            >
              {t('Cancel')}
            </Button>
            <Button onClick={handleSetTag}>{t('Set Tag')}</Button>
          </>
        }
      >
        <div className='grid gap-4 py-4'>
          <div className='grid gap-2'>
            <Label htmlFor='tag'>{t('Tag')}</Label>
            <Input
              id='tag'
              placeholder={t('Enter tag name (optional)')}
              value={tagValue}
              onChange={(e) => setTagValue(e.target.value)}
            />
          </div>
        </div>
      </Dialog>

      {/* Delete Confirmation Dialog */}
      <Dialog
        open={showDeleteConfirm}
        onOpenChange={setShowDeleteConfirm}
        title={t('Delete Channels?')}
        description={
          <>
            {t('Are you sure you want to delete')}
            {selectedIds.length}{' '}
            {t('channel(s)? This action cannot be undone.')}
          </>
        }
        contentHeight='auto'
        footer={
          <>
            <Button
              variant='outline'
              onClick={() => setShowDeleteConfirm(false)}
            >
              {t('Cancel')}
            </Button>
            <Button
              variant='destructive'
              onClick={handleDeleteAll}
              disabled={!canEditSensitive}
            >
              {t('Delete')}
            </Button>
          </>
        }
      >
        {' '}
      </Dialog>
    </>
  )
}
