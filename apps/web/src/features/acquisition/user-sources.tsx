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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { hasPermission } from '@/lib/admin-permissions'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { SourceAccountExport } from './account-export'
import { SELF_SOURCE_LABELS } from './self-source-labels'
import { SourceCorrections } from './source-corrections'

function sourceLabel(source: string | undefined, t: (key: string) => string) {
  if (!source || source === 'unknown') return t('Direct / unknown source')
  if (source === 'historical_unrecorded') {
    return t('Historical source not recorded')
  }
  return source
}
async function read<T>(path: string): Promise<T> {
  const response = await api.get(path, {
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  if (!response.data.success) throw new Error('Unable to load source records')
  return response.data.data
}
type UserPage = {
  total: number
  items: {
    user_id: number
    registered_at: number
    source: string
    evidence: string
    first_success_at: number
  }[]
}
type Detail = {
  self_reported?: { source: string; detail: string; updated_at: number } | null
  user_id: number
  registered_at: number
  historical: boolean
  first_success_at: number
  attribution: null | {
    first_source: string
    first_observed_at: number
    registration_source: string
    registration_campaign: string
    registration_inferred: boolean
    registration_visit_id: number
    lookback_days: number
  }
  recent: {
    id: number
    source: string
    evidence: string
    created_at: number
    referrer_host: string
    landing: string
  }[]
}
export function SourceUsers({
  source,
  from,
  to,
}: {
  source: string
  from: number
  to: number
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const allowed = hasPermission(user, 'acquisition', 'details')
  const [page, setPage] = useState(1)
  const [selected, setSelected] = useState<number | null>(null)
  const query = useQuery({
    queryKey: ['acquisition-users', user?.id, source, from, to, page],
    queryFn: () =>
      read<UserPage>(
        `/api/admin/acquisition/users?source=${encodeURIComponent(source)}&from=${from}&to=${to}&page=${page}`
      ),
    enabled: allowed,
    retry: false,
  })
  if (!allowed) return null
  return (
    <section className='space-y-4'>
      <h2 className='text-lg font-semibold'>
        {t('Channel accounts')}: {sourceLabel(source, t)}
      </h2>
      <SourceAccountExport source={source} from={from} to={to} />
      {query.isPending ? (
        <p>{t('Loading')}</p>
      ) : query.isError ? (
        <Button type='button' onClick={() => void query.refetch()}>
          {t('Reload source records')}
        </Button>
      ) : (
        <>
          <p>
            {t('Total')}: {query.data.total}
          </p>
          {query.data.items.length === 0 ? (
            <p>{t('No accounts match this source and registration period.')}</p>
          ) : (
            <ul className='divide-y'>
              {query.data.items.map((item) => (
                <li
                  className='flex flex-wrap items-center justify-between gap-3 py-3'
                  key={item.user_id}
                >
                  <div>
                    <p>
                      {t('Account')} #{item.user_id}
                    </p>
                    <p className='text-muted-foreground text-xs'>
                      {new Date(item.registered_at * 1000).toLocaleString()} ·{' '}
                      {t(
                        item.first_success_at
                          ? 'Successful API response observed'
                          : 'No successful API response observed yet'
                      )}
                    </p>
                  </div>
                  <Button
                    type='button'
                    variant='outline'
                    onClick={() =>
                      setSelected(
                        selected === item.user_id ? null : item.user_id
                      )
                    }
                  >
                    {t('Source and conversion')}
                  </Button>
                </li>
              ))}
            </ul>
          )}
          <div className='flex gap-2'>
            <Button
              type='button'
              variant='outline'
              disabled={page === 1}
              onClick={() => setPage(page - 1)}
            >
              {t('Previous')}
            </Button>
            <span className='self-center'>{page}</span>
            <Button
              type='button'
              variant='outline'
              disabled={page * 50 >= query.data.total}
              onClick={() => setPage(page + 1)}
            >
              {t('Next')}
            </Button>
          </div>
        </>
      )}
      {selected !== null && <UserSourceDetails userID={selected} />}
    </section>
  )
}
export function UserSourceDetails({ userID }: { userID: number }) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const allowed = hasPermission(user, 'acquisition', 'details')
  const query = useQuery({
    queryKey: ['acquisition-user-detail', user?.id, userID],
    queryFn: () => read<Detail>(`/api/admin/acquisition/users/${userID}`),
    enabled: allowed,
    retry: false,
  })
  if (!allowed) return null
  const data = query.data
  return (
    <section
      className='space-y-3 border-t pt-4'
      aria-label={t('Source and conversion')}
    >
      <h3 className='font-semibold'>
        {t('Source and conversion')} · #{userID}
      </h3>
      {query.isPending ? (
        <p>{t('Loading')}</p>
      ) : query.isError || !data ? (
        <Button type='button' onClick={() => void query.refetch()}>
          {t('Reload source records')}
        </Button>
      ) : (
        <>
          {data.self_reported && (
            <div className='rounded-md border p-3 text-sm'>
              <p className='font-medium'>{t('Self-reported source')}</p>
              <p>
                {t(
                  SELF_SOURCE_LABELS[
                    data.self_reported.source as keyof typeof SELF_SOURCE_LABELS
                  ] || 'Other'
                )}
                {data.self_reported.detail
                  ? ` · ${data.self_reported.detail}`
                  : ''}
              </p>
              <p className='text-muted-foreground'>
                {new Date(
                  data.self_reported.updated_at * 1000
                ).toLocaleString()}
              </p>
            </div>
          )}
          <dl className='grid gap-3 text-sm sm:grid-cols-2'>
            <div>
              <dt className='text-muted-foreground'>{t('Registered')}</dt>
              <dd>
                {data.registered_at
                  ? new Date(data.registered_at * 1000).toLocaleString()
                  : t('Not available')}
              </dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>
                {t('First observed source')}
              </dt>
              <dd>
                {sourceLabel(
                  data.attribution?.first_source ||
                    (data.historical ? 'historical_unrecorded' : 'unknown'),
                  t
                )}
                {!!data.attribution?.first_observed_at && (
                  <p className='text-muted-foreground text-xs'>
                    {new Date(
                      data.attribution.first_observed_at * 1000
                    ).toLocaleString()}
                  </p>
                )}
              </dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>
                {t('Registration source')}
              </dt>
              <dd>
                {sourceLabel(
                  data.attribution?.registration_source ||
                    (data.historical ? 'historical_unrecorded' : 'unknown'),
                  t
                )}
              </dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>
                {t('First observed successful API response')}
              </dt>
              <dd>
                {data.first_success_at
                  ? new Date(data.first_success_at * 1000).toLocaleString()
                  : t('Not available')}
              </dd>
            </div>
          </dl>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Only qualifying API success records are shown here. Missing records do not prove that an account never used the API.'
            )}
          </p>
          {data.attribution && data.attribution.registration_visit_id > 0 && (
            <p className='text-muted-foreground text-xs'>
              {t('Attribution lookback: {{days}} days', {
                days: data.attribution.lookback_days,
              })}{' '}
              ·{' '}
              {t(
                data.attribution.registration_inferred
                  ? 'Attributed from an earlier source observation'
                  : 'Source observed in the current visit'
              )}
            </p>
          )}
          <a
            className='text-sm underline'
            href={`/operations/sources?source=${encodeURIComponent(data.attribution?.registration_source || 'unknown')}`}
          >
            {t('Return to channel report')}
          </a>
          <SourceCorrections key={userID} userID={userID} />
          <h4 className='font-medium'>{t('Recent source observations')}</h4>
          {data.recent.length === 0 ? (
            <p className='text-muted-foreground text-sm'>
              {t(
                'No retained source observations are available for this account.'
              )}
            </p>
          ) : (
            <ol className='space-y-3 text-sm'>
              {data.recent.map((visit) => (
                <li key={visit.id} className='border-b pb-3'>
                  <p>
                    {new Date(visit.created_at * 1000).toLocaleString()} ·{' '}
                    {visit.source === 'unknown'
                      ? t('Direct / unknown source')
                      : visit.source}
                  </p>
                  <p className='text-muted-foreground'>
                    {visit.landing} ·{' '}
                    {visit.referrer_host || t('No identifiable source')}
                  </p>
                </li>
              ))}
            </ol>
          )}
        </>
      )}
    </section>
  )
}
