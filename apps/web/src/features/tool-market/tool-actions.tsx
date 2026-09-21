/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { useMutation, useQueryClient } from '@tanstack/react-query'
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
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'

import {
  marketAPI,
  marketQuota,
  type CallResponse,
  type Grant,
  type MarketTool,
} from './api'
import { marketStatus, marketPermissionList } from './copy'
import { creditAmount } from './money'

export function GrantDialog({
  tool,
  endpoint,
  clientID,
  units,
  onClose,
}: {
  tool: MarketTool
  endpoint: string
  clientID: string
  units: number
  onClose: () => void
}) {
  const { t } = useTranslation()
  const cache = useQueryClient()
  const [total, setTotal] = useState(String(tool.price_quota / units))
  const [count, setCount] = useState('1')
  const [hours, setHours] = useState('1')
  const grant = useMutation({
    retry: false,
    mutationFn: () => {
      const calls = Number(count),
        duration = Number(hours)
      if (
        !Number.isSafeInteger(calls) ||
        calls < 1 ||
        calls > 1000000 ||
        !Number.isSafeInteger(duration) ||
        duration < 1 ||
        duration > 720
      ) {
        throw new Error('Invalid limit')
      }
      return marketAPI.grant({
        client_id: clientID,
        tool_id: tool.tool_id,
        version_id: tool.version_id,
        max_price_quota: tool.price_quota,
        max_total_quota: marketQuota(total, units),
        max_calls: calls,
        expires_at: Math.floor(Date.now() / 1000) + duration * 3600,
      })
    },
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: ['tool-market'] })
      onClose()
    },
  })
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !grant.isPending) onClose()
      }}
    >
      <DialogContent className='max-h-[90dvh] overflow-auto sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>{t('Authorize tool calls')}</DialogTitle>
          <DialogDescription>
            {t(
              'This grants the selected client permission to use this exact tool version within these limits.'
            )}
          </DialogDescription>
        </DialogHeader>
        <dl className='space-y-2 text-sm'>
          <div>
            <dt className='text-muted-foreground'>{t('Tool')}</dt>
            <dd className='font-medium break-all'>{tool.name}</dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>{t('Client ID')}</dt>
            <dd className='break-all'>{clientID}</dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>{t('Data recipient')}</dt>
            <dd className='break-all'>{endpoint}</dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>
              {t('Declared permissions')}
            </dt>
            <dd className='break-words'>
              {marketPermissionList(tool.permissions, t)}
            </dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>{t('Single-call limit')}</dt>
            <dd>
              {t('{{amount}} credits', {
                amount: creditAmount(tool.price_quota, units),
              })}
            </dd>
          </div>
        </dl>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            grant.mutate()
          }}
        >
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor='grant-total'>
                {t('Total spending limit')}
              </FieldLabel>
              <Input
                id='grant-total'
                inputMode='decimal'
                value={total}
                onChange={(e) => setTotal(e.target.value)}
                required
              />
            </Field>
            <Field>
              <FieldLabel htmlFor='grant-calls'>
                {t('Maximum successful calls')}
              </FieldLabel>
              <Input
                id='grant-calls'
                type='number'
                min={1}
                max={1000000}
                value={count}
                onChange={(e) => setCount(e.target.value)}
                required
              />
            </Field>
            <Field>
              <FieldLabel htmlFor='grant-hours'>
                {t('Valid for hours')}
              </FieldLabel>
              <Input
                id='grant-hours'
                type='number'
                min={1}
                max={720}
                value={hours}
                onChange={(e) => setHours(e.target.value)}
                required
              />
            </Field>
            {grant.isError && (
              <p role='alert' className='text-destructive text-sm'>
                {t(
                  'Authorization failed. Check the limits and refresh the tool version.'
                )}
              </p>
            )}
            <Button type='submit' disabled={grant.isPending}>
              {t('Confirm authorization')}
            </Button>
          </FieldGroup>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function CallResult({
  response,
  units,
}: {
  response: CallResponse
  units: number
}) {
  const { t } = useTranslation()
  return (
    <div className='space-y-3 text-sm' aria-live='polite'>
      <dl className='grid grid-cols-2 gap-3'>
        <div>
          <dt className='text-muted-foreground'>{t('Execution status')}</dt>
          <dd>{marketStatus(response.call.execution_status, t)}</dd>
        </div>
        <div>
          <dt className='text-muted-foreground'>{t('Billing status')}</dt>
          <dd>{marketStatus(response.call.settlement_status, t)}</dd>
        </div>
        <div>
          <dt className='text-muted-foreground'>
            {response.call.settlement_status === 'held'
              ? t('Reserved')
              : t('Amount')}
          </dt>
          <dd>
            {t('{{amount}} credits', {
              amount: creditAmount(
                response.call.settlement_status === 'released'
                  ? 0
                  : response.call.price_quota,
                units
              ),
            })}
          </dd>
        </div>
        <div>
          <dt className='text-muted-foreground'>{t('Client ID')}</dt>
          <dd className='break-all'>{response.call.client_id}</dd>
        </div>
        <div className='col-span-2'>
          <dt className='text-muted-foreground'>{t('Request ID')}</dt>
          <dd className='font-mono text-xs break-all'>{response.call.id}</dd>
        </div>
      </dl>
      {response.call.settlement_status === 'held' && (
        <p>
          {t(
            'The result is not settled. {{amount}} credits remain reserved until {{time}}. Check this request instead of starting it again.',
            {
              amount: creditAmount(response.call.price_quota, units),
              time: new Date(response.call.resolve_by * 1000).toLocaleString(),
            }
          )}
        </p>
      )}
      {response.result_expired && (
        <p>
          {t(
            'The saved result has expired. The call and transfer records are retained.'
          )}
        </p>
      )}
      {response.result !== undefined && (
        <pre className='bg-muted max-h-64 overflow-auto rounded-md p-3 text-xs break-all whitespace-pre-wrap'>
          {JSON.stringify(response.result, null, 2)}
        </pre>
      )}
    </div>
  )
}

export function CallDialog({
  tool,
  grant,
  endpoint,
  units,
  onClose,
}: {
  tool: MarketTool
  grant: Grant
  endpoint: string
  units: number
  onClose: () => void
}) {
  const { t } = useTranslation()
  const cache = useQueryClient()
  const [args, setArgs] = useState('{}')
  const [requestID] = useState(() => crypto.randomUUID())
  const [submitted, setSubmitted] = useState<string | null>(null)
  const [response, setResponse] = useState<CallResponse | null>(null)
  const run = useMutation({
    retry: false,
    mutationFn: async () => {
      const raw = submitted ?? args
      const parsed = JSON.parse(raw) as unknown
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
        throw new Error('Arguments must be an object')
      }
      setSubmitted(raw)
      return marketAPI.invoke({
        tool_id: tool.tool_id,
        version_id: tool.version_id,
        grant_id: grant.id,
        request_id: requestID,
        arguments: parsed,
      })
    },
    onSuccess: (result) => {
      setResponse(result)
      void cache.invalidateQueries({ queryKey: ['tool-market'] })
    },
  })
  const refresh = useMutation({
    mutationFn: () => {
      if (!response) throw new Error('Missing call')
      return marketAPI.result(response.call.id)
    },
    onSuccess: setResponse,
  })
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !run.isPending) onClose()
      }}
    >
      <DialogContent className='max-h-[90dvh] overflow-auto sm:max-w-xl'>
        <DialogHeader>
          <DialogTitle>{tool.name}</DialogTitle>
          <DialogDescription>
            {t(
              'Only the arguments below are sent to this provider. Review them before running the tool.'
            )}
          </DialogDescription>
        </DialogHeader>
        <p className='text-muted-foreground text-sm break-all'>{endpoint}</p>
        {!response && (
          <dl className='grid grid-cols-2 gap-3 text-sm'>
            <div className='col-span-2'>
              <dt className='text-muted-foreground'>{t('Client ID')}</dt>
              <dd className='break-all'>{grant.client_id}</dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>
                {t('Single-call limit')}
              </dt>
              <dd>
                {t('{{amount}} credits', {
                  amount: creditAmount(grant.max_price_quota, units),
                })}
              </dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>
                {t('Remaining spending limit')}
              </dt>
              <dd>
                {t('{{amount}} credits', {
                  amount: creditAmount(
                    Math.max(
                      0,
                      grant.max_total_quota -
                        grant.spent_quota -
                        grant.reserved_quota
                    ),
                    units
                  ),
                })}
              </dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>
                {t('Remaining successful calls')}
              </dt>
              <dd>
                {Math.max(
                  0,
                  grant.max_calls -
                    grant.successful_calls -
                    (grant.reserved_calls ?? 0)
                )}
              </dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>{t('Expires at')}</dt>
              <dd>{new Date(grant.expires_at * 1000).toLocaleString()}</dd>
            </div>
          </dl>
        )}
        <details className='text-sm'>
          <summary className='cursor-pointer'>{t('Parameter schema')}</summary>
          <pre className='bg-muted mt-2 max-h-40 overflow-auto p-3 text-xs'>
            {JSON.stringify(JSON.parse(tool.input_schema), null, 2)}
          </pre>
        </details>
        <Field>
          <FieldLabel htmlFor='call-args'>{t('Arguments (JSON)')}</FieldLabel>
          <Textarea
            id='call-args'
            className='min-h-32 font-mono text-xs'
            value={args}
            disabled={submitted !== null || run.isPending}
            onChange={(e) => setArgs(e.target.value)}
          />
          <FieldDescription>
            {t(
              'Successful calls are charged once. Repeated delivery of this request does not charge again.'
            )}
          </FieldDescription>
        </Field>
        {run.isError && (
          <p role='alert' className='text-destructive text-sm'>
            {t(
              'The call could not be completed. Check your grant, budget, balance and arguments. A retry uses the same request ID.'
            )}
          </p>
        )}
        {!response && (
          <Button disabled={run.isPending} onClick={() => run.mutate()}>
            {run.isPending
              ? t('Running…')
              : submitted
                ? t('Retry same request')
                : t('Run for up to {{amount}} credits', {
                    amount: creditAmount(tool.price_quota, units),
                  })}
          </Button>
        )}
        {response && (
          <>
            <CallResult response={response} units={units} />
            <Button
              variant='outline'
              disabled={refresh.isPending}
              onClick={() => refresh.mutate()}
            >
              {t('Refresh result')}
            </Button>
          </>
        )}
        {refresh.isError && (
          <p role='alert' className='text-destructive text-sm'>
            {t('Could not refresh. Try again.')}
          </p>
        )}
      </DialogContent>
    </Dialog>
  )
}
