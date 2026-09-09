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
import { CalendarClock, Headphones } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'

import type {
  AssistantSupportInput,
  AssistantSupportRequest,
} from './assistant-support-api'

export function AssistantSupportControls(props: {
  request: AssistantSupportRequest | null
  eligible: boolean
  busy: boolean
  error: boolean
  onCreate: (input: AssistantSupportInput) => Promise<boolean>
  onClose: () => void
  onRetry: () => void
}) {
  const { t } = useTranslation()
  const [booking, setBooking] = useState(false)
  const [topic, setTopic] = useState('')
  const [time, setTime] = useState('')
  const [invalidTime, setInvalidTime] = useState(false)
  const request = props.request
  const active = request?.status === 'pending' || request?.status === 'accepted'
  const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone
  return (
    <div className='mb-3 grid gap-2' data-testid='assistant-human-support'>
      {active ? (
        <div
          className='border-border/70 bg-muted/40 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-xl border px-3 py-2.5'
          role='status'
        >
          <Headphones className='size-4 shrink-0' aria-hidden='true' />
          <div className='min-w-0 flex-1 text-xs leading-5'>
            <p className='font-medium'>
              {request.status === 'accepted'
                ? `${t('Human support connected')} · ${request.assigned_admin_name || t('Administrator')}`
                : request.kind === 'appointment'
                  ? t('Technical support appointment requested')
                  : t('Waiting for an administrator to join')}
            </p>
            <p className='text-muted-foreground'>
              {request.status === 'pending' && request.kind === 'appointment'
                ? `${new Date(request.scheduled_at * 1000).toLocaleString()} · ${t('AI remains available until an administrator joins.')}`
                : t(
                    'Messages stay in this conversation. AI replies are paused.'
                  )}
            </p>
          </div>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            disabled={props.busy}
            onClick={props.onClose}
          >
            {t(
              request.status === 'accepted'
                ? 'End human support'
                : 'Cancel request'
            )}
          </Button>
        </div>
      ) : request ? (
        <p className='text-muted-foreground text-xs' role='status'>
          {t(
            'Human support ended. You can continue with AI or request support again.'
          )}
        </p>
      ) : null}
      <div className='flex flex-wrap gap-2'>
        {!active ||
        (request?.kind === 'appointment' && request.status === 'pending') ? (
          <Button
            type='button'
            size='sm'
            variant='outline'
            disabled={props.busy}
            onClick={() => void props.onCreate({ kind: 'handoff' })}
          >
            <Headphones className='size-4' aria-hidden='true' />
            {t('Transfer to human')}
          </Button>
        ) : null}
        {props.eligible && !active ? (
          <Button
            type='button'
            size='sm'
            variant='ghost'
            disabled={props.busy}
            onClick={() => setBooking(true)}
          >
            <CalendarClock className='size-4' aria-hidden='true' />
            {t('Book technical support')}
          </Button>
        ) : null}
        {props.error ? (
          <Button
            type='button'
            variant='ghost'
            size='sm'
            onClick={props.onRetry}
          >
            {t('Refresh support status')}
          </Button>
        ) : null}
      </div>
      <Dialog open={booking} onOpenChange={setBooking}>
        <DialogContent className='sm:max-w-md'>
          <DialogHeader>
            <DialogTitle>{t('Book technical support')}</DialogTitle>
            <DialogDescription>
              {t(
                'Tell us the issue and your preferred time. An administrator will accept the request and join this conversation.'
              )}
            </DialogDescription>
          </DialogHeader>
          <form
            className='grid gap-4'
            onSubmit={async (event) => {
              event.preventDefault()
              const scheduled = Math.floor(new Date(time).getTime() / 1000)
              if (
                !Number.isFinite(scheduled) ||
                scheduled <= Date.now() / 1000 ||
                scheduled > Date.now() / 1000 + 90 * 86400
              ) {
                setInvalidTime(true)
                return
              }
              setInvalidTime(false)
              if (
                await props.onCreate({
                  kind: 'appointment',
                  topic,
                  preferred_time: `${time} (${timezone})`,
                  scheduled_at: scheduled,
                })
              ) {
                setBooking(false)
              }
            }}
          >
            <div className='grid gap-2'>
              <Label htmlFor='assistant-support-topic'>
                {t('What do you need help with?')}
              </Label>
              <Textarea
                id='assistant-support-topic'
                required
                maxLength={1000}
                value={topic}
                onChange={(event) => setTopic(event.target.value)}
              />
            </div>
            <div className='grid gap-2'>
              <Label htmlFor='assistant-support-time'>
                {t('Preferred time')} · {timezone}
              </Label>
              <Input
                id='assistant-support-time'
                required
                type='datetime-local'
                value={time}
                onChange={(event) => setTime(event.target.value)}
              />
              {invalidTime ? (
                <p className='text-destructive text-xs' role='alert'>
                  {t('Choose a future time within the next 90 days.')}
                </p>
              ) : null}
            </div>
            <Button type='submit' disabled={props.busy}>
              {t(props.busy ? 'Submitting...' : 'Submit appointment')}
            </Button>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
