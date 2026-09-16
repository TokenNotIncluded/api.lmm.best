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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useFormContext } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { api } from '@/lib/api'

import type { AssistantSettingsFormValues } from './assistant-settings-schema'

type RiskEvent = {
  id: number
  user_id: number
  action: string
  created_at: number
  policy_version: string
  evidence: string
}
type Envelope<T> = { success: boolean; message?: string; data?: T }

export function AssistantL1ReviewSettings(_props: {
  groups: string[]
  groupsLoading: boolean
  getModels: (group: string) => Promise<string[]>
}) {
  const { t } = useTranslation()
  const form = useFormContext<AssistantSettingsFormValues>()
  const queryClient = useQueryClient()
  const [before, setBefore] = useState<number>()
  const [busyUser, setBusyUser] = useState<number>()
  const events = useQuery({
    queryKey: ['assistant-registration-events', before],
    queryFn: async () => {
      const { data } = await api.get<Envelope<RiskEvent[]>>(
        '/api/assistant/admin/registration-events',
        { params: { before }, skipBusinessError: true }
      )
      if (!data.success || !data.data)
        throw new Error(data.message ?? 'Unable to load risk inbox')
      return data.data
    },
    retry: false,
    staleTime: 15_000,
  })
  async function release(userID: number) {
    if (
      !window.confirm(
        t('Restore this account after reviewing the recorded evidence?')
      )
    )
      return
    setBusyUser(userID)
    try {
      const { data } = await api.post<Envelope<RiskEvent>>(
        `/api/assistant/admin/registration-events/${userID}/release`,
        { confirmed: true }
      )
      if (!data.success || data.data?.action !== 'released')
        throw new Error('No release receipt')
      toast.success(t('Account restored'))
      await queryClient.invalidateQueries({
        queryKey: ['assistant-registration-events'],
      })
    } catch {
      toast.error(t('Account could not be restored. Check its current state.'))
    } finally {
      setBusyUser(undefined)
    }
  }
  return (
    <section
      className='grid gap-5 border-t pt-6'
      data-testid='assistant-registration-guard-settings'
    >
      <div>
        <h3 className='text-sm font-medium'>{t('Registration protection')}</h3>
        <p className='text-muted-foreground mt-1 text-sm leading-6'>
          {t(
            'The built-in assistant handles L0 verification using server-checked tools. No separate review model or recommendation-letter queue is required.'
          )}
        </p>
      </div>
      <div className='bg-muted/30 grid gap-4 rounded-xl border p-4 sm:grid-cols-2'>
        <FormField
          control={form.control}
          name='AssistantRegistrationAutoSuspendEnabled'
          render={({ field }) => (
            <FormItem>
              <div className='flex items-center justify-between gap-3'>
                <FormLabel>
                  {t('Allow evidence-gated L0 suspensions')}
                </FormLabel>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </div>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='AssistantRegistrationDailySuspendCap'
          render={({ field }) => (
            <FormItem>
              <FormLabel>
                {t('Maximum automatic suspensions per UTC day')}
              </FormLabel>
              <FormControl>
                <Input
                  type='number'
                  min={0}
                  max={5}
                  step={1}
                  value={field.value}
                  onChange={(event) =>
                    field.onChange(
                      event.target.value === '' ? 0 : Number(event.target.value)
                    )
                  }
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
      </div>
      <p className='text-muted-foreground text-xs leading-5'>
        {t(
          'Suspension requires a matching multi-message campaign, linked network peers and a consumed reward identity together. The cap applies across all server instances. Disabling suspension still preserves verification holds and alerts. These settings take effect only after saving.'
        )}
      </p>
      <div className='flex items-center justify-between gap-3'>
        <h4 className='text-sm font-medium'>{t('Registration risk inbox')}</h4>
        <Button
          type='button'
          size='sm'
          variant='outline'
          disabled={events.isFetching}
          onClick={() => void events.refetch()}
        >
          {t('Refresh')}
        </Button>
      </div>
      {events.isPending ? (
        <p role='status' className='text-muted-foreground text-sm'>
          {t('Loading recorded actions…')}
        </p>
      ) : events.isError ? (
        <p role='alert' className='text-sm'>
          {t('Unable to load risk inbox')}
        </p>
      ) : events.data?.length === 0 ? (
        <p className='text-muted-foreground rounded-xl border border-dashed p-5 text-sm'>
          {t('No recorded registration alerts in this page.')}
        </p>
      ) : (
        <div className='grid gap-2'>
          {events.data?.map((event) => (
            <article
              key={event.id}
              className='grid gap-2 rounded-xl border p-3'
            >
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <p className='text-sm font-medium'>
                  {t('User')} #{event.user_id}{' '}
                  <span className='bg-muted ml-2 rounded px-2 py-0.5 text-xs'>
                    {t(event.action)}
                  </span>
                </p>
                <time
                  className='text-muted-foreground text-xs'
                  dateTime={new Date(event.created_at * 1000).toISOString()}
                >
                  {new Date(event.created_at * 1000).toLocaleString()}
                </time>
              </div>
              <details className='text-muted-foreground text-xs'>
                <summary className='cursor-pointer'>
                  {t('Server evidence and policy version')}
                </summary>
                <pre className='bg-muted mt-2 overflow-auto rounded p-2 break-all whitespace-pre-wrap'>
                  {event.policy_version}
                  {'\n'}
                  {event.evidence}
                </pre>
              </details>
              {event.action === 'suspend' ? (
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  className='justify-self-start'
                  disabled={busyUser !== undefined}
                  onClick={() => void release(event.user_id)}
                >
                  {t('Review and restore account')}
                </Button>
              ) : null}
            </article>
          ))}
        </div>
      )}
      <div className='flex gap-2'>
        <Button
          type='button'
          size='sm'
          variant='ghost'
          disabled={before === undefined || events.isFetching}
          onClick={() => setBefore(undefined)}
        >
          {t('Latest')}
        </Button>
        <Button
          type='button'
          size='sm'
          variant='ghost'
          disabled={events.isFetching || events.data?.length !== 50}
          onClick={() => setBefore(events.data?.at(-1)?.id)}
        >
          {t('Older records')}
        </Button>
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'A record here confirms an in-site alert, not delivery of an email. Raw cross-account conversations, IP addresses and email addresses are not shown.'
        )}
      </p>
    </section>
  )
}
