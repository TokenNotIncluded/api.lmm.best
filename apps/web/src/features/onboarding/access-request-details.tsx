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
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { formatDateTimeObject } from '@/lib/time'
import { useAuthStore } from '@/stores/auth-store'

import {
  developerAccessRequestQueryKey,
  getDeveloperAccessRequest,
} from './api'

export function AccessRequestDetails({ inline = false }: { inline?: boolean }) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const queryKey = developerAccessRequestQueryKey(user?.id ?? 0)
  const query = useQuery({
    queryKey,
    queryFn: async () => {
      const request = await getDeveloperAccessRequest()
      return request ? { ...request } : null
    },
    enabled: !!user,
    staleTime: 15_000,
    retry: false,
  })
  if (!user) return null
  const request = query.data
  if (query.isPending) {
    return <p className='text-muted-foreground text-sm'>{t('Loading')}</p>
  }
  if (query.isError) {
    return (
      <div className='space-y-2'>
        <p role='alert'>{t('Unable to load access status')}</p>
        <Button
          type='button'
          variant='outline'
          onClick={() => void query.refetch()}
          disabled={query.isFetching}
        >
          {t('Retry')}
        </Button>
      </div>
    )
  }
  const Container = inline ? 'section' : 'details'
  return (
    <Container
      className='space-y-3 text-sm'
      data-testid='access-request-history'
    >
      {!inline && (
        <summary className='cursor-pointer'>
          {t('View access request status')}
        </summary>
      )}
      {inline && request?.status === 'pending' && (
        <p data-testid='l0-pending-request'>
          {t(
            request.ai_recommendation
              ? 'AI recommendation submitted'
              : 'Access request submitted'
          )}
        </p>
      )}
      {request ? (
        <dl className='space-y-3'>
          <div>
            <dt className='text-muted-foreground'>{t('Status')}</dt>
            <dd>
              {t(
                request.status === 'pending'
                  ? 'Pending review'
                  : request.status === 'approved'
                    ? 'Access request approved'
                    : 'Access request rejected'
              )}
            </dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>
              {t('Original request submitted')}
            </dt>
            <dd>{formatDateTimeObject(new Date(request.created_at * 1000))}</dd>
          </div>
          {request.reviewed_at > 0 && (
            <div>
              <dt className='text-muted-foreground'>{t('Review completed')}</dt>
              <dd>
                {formatDateTimeObject(new Date(request.reviewed_at * 1000))}
              </dd>
            </div>
          )}
          <div>
            <dt className='text-muted-foreground'>{t('Reason')}</dt>
            <dd className='break-words whitespace-pre-wrap'>
              {request.reason}
            </dd>
          </div>
          {request.ai_recommendation && (
            <div>
              <dt className='text-muted-foreground'>
                {t('AI recommendation')}
              </dt>
              <dd className='break-words whitespace-pre-wrap'>
                {request.ai_recommendation}
              </dd>
            </div>
          )}
          {request.admin_note && (
            <div>
              <dt className='text-muted-foreground'>
                {t('Administrator note')}
              </dt>
              <dd className='break-words whitespace-pre-wrap'>
                {request.admin_note}
              </dd>
            </div>
          )}
        </dl>
      ) : (
        <p>{t('Not requested')}</p>
      )}
    </Container>
  )
}
