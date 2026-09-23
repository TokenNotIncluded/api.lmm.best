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
import {
  ExternalLink,
  Gift,
  KeyRound,
  MessageCircle,
  Paintbrush,
} from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button, buttonVariants } from '@/components/ui/button'
import { requestAssistantOpen } from '@/features/assistant/assistant-events'
import { useStatus } from '@/hooks/use-status'
import { ROLE } from '@/lib/roles'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { ensureAssistantRuntimeApiKey, prepareDrawingApiKey } from '../api'
import { useApiKeys } from './api-keys-provider'

export function AutomaticApiKeyActions() {
  const { t } = useTranslation()
  const { triggerRefresh } = useApiKeys()
  const user = useAuthStore((state) => state.auth.user)
  const { status, capabilitiesReady } = useStatus()
  const canCreateRedPackets = (user?.role ?? 0) >= ROLE.ADMIN
  const rootUserID =
    (user?.role ?? 0) >= ROLE.SUPER_ADMIN ? user?.id : undefined
  const [pending, setPending] = useState(false)
  const [runtimeKeyAttempt, setRuntimeKeyAttempt] = useState(0)
  const [runtimeKeyState, setRuntimeKeyState] = useState<
    'idle' | 'pending' | 'ready' | 'error'
  >('idle')

  useEffect(() => {
    if (
      !rootUserID ||
      !capabilitiesReady ||
      status?.assistant?.enabled === false
    )
      return
    let active = true
    setRuntimeKeyState('pending')
    void ensureAssistantRuntimeApiKey()
      .then((result) => {
        if (!active) return
        if (!result.success || !result.data) {
          setRuntimeKeyState('error')
          return
        }
        setRuntimeKeyState('ready')
        triggerRefresh()
      })
      .catch(() => {
        if (active) setRuntimeKeyState('error')
      })
    return () => {
      active = false
    }
  }, [
    rootUserID,
    capabilitiesReady,
    status?.assistant?.enabled,
    runtimeKeyAttempt,
    triggerRefresh,
  ])

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
    <div className='border-border divide-border divide-y border-y'>
      <div className='flex flex-col gap-3 py-3 sm:flex-row sm:items-center sm:justify-between'>
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
      {rootUserID ? (
        <div className='flex flex-col gap-3 py-3 sm:flex-row sm:items-center sm:justify-between'>
          <div className='min-w-0'>
            <p className='flex items-center gap-2 text-sm font-medium'>
              <KeyRound className='size-4' aria-hidden='true' />
              {t('AI assistant runtime')}
            </p>
            <p className='text-muted-foreground mt-1 text-xs leading-5'>
              {t(
                'The assistant uses an internal key owned by this super administrator. It is created automatically and appears below, but cannot be used for ordinary API calls.'
              )}
            </p>
          </div>
          {status?.assistant?.enabled === false ? (
            <span className='text-muted-foreground text-xs'>
              {t('AI assistant is disabled')}
            </span>
          ) : runtimeKeyState === 'pending' ? (
            <span className='text-muted-foreground text-xs' role='status'>
              {t('Preparing...')}
            </span>
          ) : runtimeKeyState === 'error' ? (
            <div className='flex flex-wrap items-center gap-2' role='alert'>
              <span className='text-destructive text-xs'>
                {t('Unable to prepare assistant runtime key')}
              </span>
              <Button
                type='button'
                size='sm'
                variant='outline'
                onClick={() => setRuntimeKeyAttempt((attempt) => attempt + 1)}
              >
                {t('Retry')}
              </Button>
            </div>
          ) : null}
        </div>
      ) : null}
      <div className='flex flex-col gap-3 py-3 sm:flex-row sm:items-center sm:justify-between'>
        <div className='min-w-0'>
          <p className='flex items-center gap-2 text-sm font-medium'>
            <MessageCircle className='size-4' aria-hidden='true' />
            {t('AI assistant')}
          </p>
          <p className='text-muted-foreground mt-1 text-xs leading-5'>
            {t(
              'The assistant runtime key belongs to the super administrator. Keys the assistant creates for your client appear in your account after you confirm them.'
            )}
          </p>
        </div>
        <Button
          type='button'
          size='sm'
          variant='outline'
          className='self-start sm:shrink-0'
          onClick={() => requestAssistantOpen('api-key')}
        >
          <MessageCircle data-icon='inline-start' aria-hidden='true' />
          {t('Ask assistant to create a key')}
        </Button>
      </div>
      {canCreateRedPackets ? (
        <div className='flex flex-col gap-3 py-3 sm:flex-row sm:items-center sm:justify-between'>
          <div className='min-w-0'>
            <p className='flex items-center gap-2 text-sm font-medium'>
              <Gift className='size-4' aria-hidden='true' />
              {t('Red packet cover')}
            </p>
            <p className='text-muted-foreground mt-1 text-xs leading-5'>
              {t(
                'Generating a red packet cover creates an API key for the selected group on first use. The key then appears below.'
              )}
            </p>
          </div>
          <Link
            to='/red-packets'
            className={cn(
              buttonVariants({ size: 'sm', variant: 'outline' }),
              'self-start sm:shrink-0'
            )}
          >
            {t('Open Red Packets')}
            <ExternalLink data-icon='inline-end' aria-hidden='true' />
          </Link>
        </div>
      ) : null}
    </div>
  )
}
