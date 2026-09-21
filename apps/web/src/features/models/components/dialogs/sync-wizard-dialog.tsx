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
import { Loader2, RefreshCw } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { useIsMobile } from '@/hooks/use-mobile'

import { syncUpstream, previewUpstreamDiff } from '../../api'
import { getSyncLocaleOptions } from '../../constants'
import { modelsQueryKeys, vendorsQueryKeys } from '../../lib'
import type { SyncLocale } from '../../types'
import { useModels } from '../models-provider'

type SyncWizardDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function SyncWizardDialog({
  open,
  onOpenChange,
}: SyncWizardDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const {
    setOpen,
    setUpstreamConflicts,
    setSyncWizardOptions,
    syncWizardOptions,
  } = useModels()
  const isMobile = useIsMobile()
  const [locale, setLocale] = useState<SyncLocale>('zh')
  const [isSyncing, setIsSyncing] = useState(false)

  const SYNC_LOCALE_OPTIONS = getSyncLocaleOptions(t)

  useEffect(() => {
    if (open) setLocale(syncWizardOptions.locale || 'zh')
  }, [open, syncWizardOptions.locale])

  const handleSync = async () => {
    setIsSyncing(true)
    try {
      setSyncWizardOptions({ locale })
      const previewRes = await previewUpstreamDiff({ locale })

      if (!previewRes.success) {
        throw new Error(previewRes.message || 'Failed to preview upstream diff')
      }

      const conflicts = previewRes.data?.conflicts || []

      if (conflicts.length > 0) {
        toast.warning(
          `Found ${conflicts.length} conflict${conflicts.length > 1 ? 's' : ''}. Please resolve them first.`
        )
        setUpstreamConflicts(conflicts)
        setOpen('upstream-conflict')
        return
      }

      // No conflicts, proceed with sync
      const response = await syncUpstream({ locale })

      if (response.success) {
        const { created_models, created_vendors, updated_models } =
          response.data || {}
        toast.success(
          `Sync completed! Created ${created_models || 0} models, updated ${updated_models || 0}, and added ${created_vendors || 0} vendors.`
        )
        queryClient.invalidateQueries({ queryKey: modelsQueryKeys.lists() })
        queryClient.invalidateQueries({ queryKey: vendorsQueryKeys.lists() })
        onOpenChange(false)
      } else {
        toast.error(response.message || 'Sync failed')
      }
    } catch (error: unknown) {
      toast.error((error as Error)?.message || 'Sync failed')
    } finally {
      setIsSyncing(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Sync Upstream Models')}
      description={t('Synchronize models and vendors from an upstream source')}
      initialFocus={!isMobile}
      contentHeight='auto'
      bodyClassName='flex flex-col gap-6'
      footer={
        <>
          <Button
            variant='outline'
            onClick={() => onOpenChange(false)}
            disabled={isSyncing}
          >
            {t('Cancel')}
          </Button>
          <Button onClick={handleSync} disabled={isSyncing}>
            {isSyncing && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
            <RefreshCw className='mr-2 h-4 w-4' />
            {isSyncing ? t('Syncing...') : t('Sync Now')}
          </Button>
        </>
      }
    >
      <div className='space-y-2'>
        <Label className='text-base'>{t('Select Language')}</Label>
        <RadioGroup
          value={locale}
          onValueChange={(v) => setLocale(v as SyncLocale)}
          className='grid gap-3 sm:grid-cols-3'
        >
          {SYNC_LOCALE_OPTIONS.map((option) => (
            <div
              key={option.value}
              className='flex items-center space-x-2 rounded-lg border p-3'
            >
              <RadioGroupItem
                value={option.value}
                id={`locale-${option.value}`}
              />
              <Label
                htmlFor={`locale-${option.value}`}
                className='cursor-pointer font-normal'
              >
                {option.label}
              </Label>
            </div>
          ))}
        </RadioGroup>
      </div>
    </Dialog>
  )
}
