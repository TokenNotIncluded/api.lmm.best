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
import { Link } from '@tanstack/react-router'
import { ExternalLink, Paintbrush } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button, buttonVariants } from '@/components/ui/button'
import { cn } from '@/lib/utils'

import { prepareDrawingApiKey } from '../api'
import { useApiKeys } from './api-keys-provider'

export function AutomaticApiKeyActions() {
  const { t } = useTranslation()
  const { triggerRefresh } = useApiKeys()
  const [pending, setPending] = useState(false)

  const prepareKey = async () => {
    if (pending) return
    setPending(true)
    try {
      const result = await prepareDrawingApiKey()
      if (!result.success || !result.data) {
        toast.error(result.message || t('Unable to prepare drawing API key'))
        return
      }
      triggerRefresh()
      toast.success(
        result.data.created
          ? t('Drawing MCP API key created')
          : t('Existing drawing API key selected')
      )
    } catch {
      toast.error(t('Unable to prepare drawing API key'))
    } finally {
      setPending(false)
    }
  }

  return (
    <div className='border-border flex flex-col gap-3 border-y py-3 sm:flex-row sm:items-center sm:justify-between'>
      <div className='min-w-0'>
        <p className='flex items-center gap-2 text-sm font-medium'>
          <Paintbrush className='size-4' aria-hidden='true' />
          {t('Drawing MCP')}
        </p>
        <p className='text-muted-foreground mt-1 text-xs leading-5'>
          {t(
            'Prepare an image-2 API key here, then choose it in Drawing MCP settings.'
          )}
        </p>
      </div>
      <div className='flex shrink-0 flex-wrap gap-2'>
        <Button
          type='button'
          size='sm'
          disabled={pending}
          onClick={() => void prepareKey()}
        >
          <Paintbrush data-icon='inline-start' aria-hidden='true' />
          {pending ? t('Preparing...') : t('Prepare API key')}
        </Button>
        <Link
          to='/drawing'
          className={cn(buttonVariants({ size: 'sm', variant: 'outline' }))}
        >
          {t('Open Drawing MCP settings')}
          <ExternalLink data-icon='inline-end' aria-hidden='true' />
        </Link>
      </div>
    </div>
  )
}
