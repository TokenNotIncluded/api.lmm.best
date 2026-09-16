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
/*
Copyright (C) 2026 LIghtJUNction
*/
import { useQuery } from '@tanstack/react-query'
import { ShieldCheck, RefreshCw } from 'lucide-react'
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import {
  registrationState,
  registrationStateCopy,
} from './assistant-registration-state'

export function AssistantRegistrationStatus({
  compact = false,
  onContinueSetup,
  onApproved,
}: {
  compact?: boolean
  onContinueSetup?: () => void
  onApproved?: () => void
}) {
  const { t } = useTranslation()
  const userID = useAuthStore((state) => state.auth.user?.id)
  const query = useQuery({
    queryKey: ['assistant-registration-state', userID],
    queryFn: async () => {
      const { data } = await api.get<{
        success: boolean
        data?: { state?: unknown }
      }>('/api/assistant/registration-check', {
        disableDuplicate: true,
        skipBusinessError: true,
        skipErrorHandler: true,
      })
      if (!data.success || !data.data) {
        throw new Error('Registration status is unavailable')
      }
      return registrationState(data.data.state)
    },
    enabled: Boolean(userID),
    retry: false,
    staleTime: 5_000,
    refetchInterval: (q) =>
      q.state.data === 'active' || q.state.error ? false : 15_000,
    refetchIntervalInBackground: false,
  })
  useEffect(() => {
    if (userID && query.data === 'active' && !query.isError) {
      onApproved?.()
    }
  }, [onApproved, query.data, query.isError, userID])
  if (!userID || (compact && query.data === 'active')) return null
  const copy = registrationStateCopy(query.data ?? 'context_needed')
  return (
    <section
      className='border-border bg-muted/30 grid gap-2 rounded-xl border p-3 text-sm'
      aria-label={t('Registration verification')}
      data-testid='assistant-registration-status'
    >
      <div className='flex items-start gap-2'>
        <ShieldCheck
          className='text-muted-foreground mt-0.5 size-4 shrink-0'
          aria-hidden='true'
        />
        <div className='min-w-0 flex-1' role='status' aria-live='polite'>
          <p className='font-medium'>
            {t(
              query.isError
                ? 'Registration status is unavailable'
                : query.isPending
                  ? 'Checking registration status'
                  : copy.title
            )}
          </p>
          {!compact || query.data === 'held' ? (
            <p className='text-muted-foreground mt-1 text-xs leading-5'>
              {t(
                query.isError
                  ? 'Keep chatting or retry. Access has not been granted by this message.'
                  : copy.detail
              )}
            </p>
          ) : null}
        </div>
        <Button
          type='button'
          size='icon'
          variant='ghost'
          aria-label={t('Refresh registration status')}
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          <RefreshCw className='size-4' aria-hidden='true' />
        </Button>
      </div>
      {!compact ? (
        <details className='text-muted-foreground text-xs leading-5'>
          <summary className='cursor-pointer rounded focus-visible:outline focus-visible:outline-2'>
            {t('How verification works')}
          </summary>
          <p className='pt-2'>
            {t(
              'We compare de-identified registration and conversation patterns to prevent duplicate rewards. Other users never receive your conversation text. QQ email, a random nickname, typos, changing topics or using AI are not sufficient reasons to suspend an account.'
            )}
          </p>
        </details>
      ) : null}
      {query.data === 'active' && onContinueSetup ? (
        <Button type='button' size='sm' onClick={onContinueSetup}>
          {t('Continue setup')}
        </Button>
      ) : null}
    </section>
  )
}
