/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'

import {
  marketAPI,
  marketQuota,
  MarketAPIError,
  type CallResponse,
  type Grant,
  type MarketTool,
} from './api'
import {
  callCanBeEdited,
  callConfirmation,
  canStartAnotherCall,
  marketErrorKey,
} from './call-utils'
import { marketStatus, marketPermissionList } from './copy'
import { creditAmount } from './money'
import {
  drawingResultImages,
  resultImage,
  type ResultImage,
} from './result-images'
import { ToolArgumentsForm } from './schema-form'
import {
  argumentIssue,
  initialArguments,
  schemaObject,
} from './schema-form-utils'

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
    mutationFn: async () => {
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
      const authorization = await marketAPI.grant({
        client_id: clientID,
        tool_id: tool.tool_id,
        version_id: tool.version_id,
        max_price_quota: tool.price_quota,
        max_total_quota: marketQuota(total, units),
        max_calls: calls,
        expires_at: Math.floor(Date.now() / 1000) + duration * 3600,
      })
      try {
        await marketAPI.install(
          {
            client_id: clientID,
            tool_id: tool.tool_id,
            version_id: tool.version_id,
          },
          true
        )
      } catch {
        throw new MarketAPIError('TOOL_MARKET_LOAD_INCOMPLETE')
      }
      return authorization
    },
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: ['tool-market'] })
      onClose()
    },
    onSettled: () =>
      void cache.invalidateQueries({ queryKey: ['tool-market'] }),
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
          <DialogTitle>{t('Add and authorize tool')}</DialogTitle>
          <DialogDescription>
            {t(
              'This loads the tool for the selected client and authorizes this exact version within the limits below.'
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
            <dd className='break-all'>{endpoint || t('Platform builtin')}</dd>
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
        {tool.billing_mode === 'input_tokens' && (
          <p className='text-sm'>
            {t('{{amount}} credits per million input tokens', {
              amount: creditAmount(tool.input_token_price_quota ?? 0, units),
            })}
          </p>
        )}
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
                {grant.error instanceof MarketAPIError &&
                grant.error.code === 'TOOL_MARKET_LOAD_INCOMPLETE'
                  ? t(
                      'Authorization was saved, but loading failed. Refresh access and load this tool.'
                    )
                  : grant.error instanceof MarketAPIError
                    ? t(marketErrorKey(grant.error))
                    : t(
                        'Authorization failed. Check the limits and refresh the tool version.'
                      )}
              </p>
            )}
            <Button type='submit' disabled={grant.isPending}>
              {t('Add and authorize tool')}
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
    <div className='min-w-0 space-y-3 text-sm' aria-live='polite'>
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
      {response.error_code && (
        <p role='status'>
          {response.error_code === 'TOOL_MARKET_RESULT_UNKNOWN'
            ? t(
                'The result is unknown. Check this call instead of starting it again.'
              )
            : response.error_code === 'TOOL_MARKET_SETTLEMENT_PENDING'
              ? t(
                  'The result was received. Settlement is still being confirmed.'
                )
              : t('The tool returned an invalid or unsuccessful result.')}
        </p>
      )}
      {response.result !== undefined && (
        <MCPResultView value={response.result} />
      )}
    </div>
  )
}

function ImageResult({ image }: { image: ResultImage }) {
  const { t } = useTranslation()
  const [url, setURL] = useState('')
  useEffect(() => {
    let blobURL = ''
    try {
      const decoded = atob(image.data)
      const bytes = new Uint8Array(decoded.length)
      for (let index = 0; index < decoded.length; index++) {
        bytes[index] = decoded.charCodeAt(index)
      }
      blobURL = URL.createObjectURL(new Blob([bytes], { type: image.mimeType }))
      setURL(blobURL)
    } catch {
      setURL('')
    }
    return () => {
      if (blobURL) URL.revokeObjectURL(blobURL)
    }
  }, [image.data, image.mimeType])
  if (!url) return null
  return (
    <div className='space-y-2'>
      <img
        src={url}
        alt={t('Image result')}
        className='max-h-96 max-w-full rounded-md'
      />
      <a
        href={url}
        download={`image.${image.mimeType.split('/')[1]}`}
        className='text-primary underline'
      >
        {t('Download')}
      </a>
    </div>
  )
}

function MCPResultView({ value }: { value: unknown }) {
  const { t } = useTranslation()
  if (!schemaObject(value)) {
    return (
      <pre className='bg-muted max-h-64 overflow-auto rounded-md p-3 text-xs whitespace-pre-wrap'>
        {JSON.stringify(value, null, 2)}
      </pre>
    )
  }
  const content = Array.isArray(value.content) ? value.content : []
  const drawing = drawingResultImages(
    value.structuredContent,
    t('Image result')
  )
  return (
    <div className='min-w-0 space-y-3'>
      {content.map((item: unknown, index: number) => {
        if (!schemaObject(item)) return null
        if (item.type === 'text' && typeof item.text === 'string') {
          return (
            <p
              key={index}
              className='[overflow-wrap:anywhere] whitespace-pre-wrap'
            >
              {item.text}
            </p>
          )
        }
        if (item.type === 'image') {
          const image = resultImage(item.data, item.mimeType)
          return image ? <ImageResult key={index} image={image} /> : null
        }
        if (
          item.type === 'audio' &&
          typeof item.data === 'string' &&
          item.data.length <= 3 * 1024 * 1024 &&
          /^[A-Za-z0-9+/=\r\n]+$/.test(item.data) &&
          typeof item.mimeType === 'string' &&
          /^audio\/(wav|mpeg|ogg|webm|flac)$/.test(item.mimeType)
        ) {
          return (
            <audio
              key={index}
              controls
              aria-label={t('Audio result')}
              src={`data:${item.mimeType};base64,${item.data}`}
            />
          )
        }
        if (
          item.type === 'resource' &&
          schemaObject(item.resource) &&
          typeof item.resource.text === 'string'
        ) {
          return (
            <pre
              key={index}
              className='bg-muted max-h-64 overflow-auto rounded-md p-3 text-xs whitespace-pre-wrap'
            >
              {item.resource.text}
            </pre>
          )
        }
        return (
          <pre
            key={index}
            className='bg-muted max-h-64 overflow-auto rounded-md p-3 text-xs whitespace-pre-wrap'
          >
            {JSON.stringify(item, null, 2)}
          </pre>
        )
      })}
      {drawing.images.map((image, index) => (
        <ImageResult key={`drawing-${index}`} image={image} />
      ))}
      {value.structuredContent !== undefined && (
        <pre className='bg-muted max-h-64 overflow-auto rounded-md p-3 text-xs whitespace-pre-wrap'>
          {JSON.stringify(drawing.metadata, null, 2)}
        </pre>
      )}
      {!content.length &&
        value.structuredContent === undefined &&
        !value.inputRequests && (
          <pre className='bg-muted max-h-64 overflow-auto rounded-md p-3 text-xs whitespace-pre-wrap'>
            {JSON.stringify(value, null, 2)}
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
  const [args, setArgs] = useState(() => initialArguments(tool.input_schema))
  const [requestID, setRequestID] = useState(() => crypto.randomUUID())
  const [submitted, setSubmitted] = useState<string | null>(null)
  const [response, setResponse] = useState<CallResponse | null>(null)
  const [confirmed, setConfirmed] = useState(false)
  const [validation, setValidation] = useState<string | null>(null)
  const confirmation = callConfirmation(response)
  const run = useMutation({
    retry: false,
    mutationFn: async (action?: 'accept' | 'cancel') => {
      const raw = submitted ?? args
      const issue = argumentIssue(raw, tool.input_schema)
      if (issue) {
        setValidation(issue)
        throw new MarketAPIError('TOOL_MARKET_ARGUMENTS')
      }
      setValidation(null)
      if (action && !confirmation) {
        throw new MarketAPIError('TOOL_MARKET_INVALID_INPUT')
      }
      setSubmitted(raw)
      return marketAPI.invoke({
        tool_id: tool.tool_id,
        version_id: tool.version_id,
        grant_id: grant.id,
        request_id: requestID,
        arguments: JSON.parse(raw) as unknown,
        arguments_json: raw,
        ...(action && confirmation
          ? {
              request_state: confirmation.requestState,
              input_responses: {
                confirmation: {
                  action,
                  ...(action === 'accept'
                    ? { content: { confirmed: true as const } }
                    : {}),
                },
              },
            }
          : {}),
      })
    },
    onSuccess: (result) => {
      setResponse(result)
      setConfirmed(false)
      void cache.invalidateQueries({ queryKey: ['tool-market'] })
    },
    onError: (error) => {
      // Only definite pre-execution rejections make the original input editable.
      // A network/server error retains its immutable payload and request ID.
      if (!response && callCanBeEdited(error)) {
        setSubmitted(null)
        setRequestID(crypto.randomUUID())
      }
    },
  })
  const refresh = useMutation({
    mutationFn: () => {
      if (!response) throw new Error('Missing call')
      return marketAPI.result(response.call.id)
    },
    onSuccess: (result) => {
      setResponse(result)
      setConfirmed(false)
    },
  })
  const newCall = () => {
    if (!canStartAnotherCall(response)) return
    setSubmitted(null)
    setResponse(null)
    setRequestID(crypto.randomUUID())
    setConfirmed(false)
    setValidation(null)
    run.reset()
    refresh.reset()
  }
  const inputError =
    validation?.startsWith('Missing parameter: ') ||
    validation?.startsWith('Invalid parameter: ')
      ? t('Check parameter {{name}}.', {
          name: validation.slice(validation.indexOf(': ') + 2),
        })
      : validation
        ? t(validation)
        : null
  const uncertainRequest =
    submitted !== null &&
    !response &&
    run.isError &&
    !callCanBeEdited(run.error)
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !run.isPending && !confirmation && !uncertainRequest) {
          onClose()
        }
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
        <p className='text-muted-foreground text-sm break-all'>
          {endpoint || t('Platform builtin')}
        </p>
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
        <ToolArgumentsForm
          schema={tool.input_schema}
          value={args}
          onChange={(value) => {
            setArgs(value)
            setValidation(null)
            run.reset()
          }}
          disabled={submitted !== null || run.isPending}
        />
        <p className='text-muted-foreground text-xs'>
          {t(
            'Successful calls are charged once. Repeated delivery of this request does not charge again.'
          )}
        </p>
        {run.isError && (
          <p role='alert' className='text-destructive text-sm'>
            {inputError || t(marketErrorKey(run.error))}
          </p>
        )}
        {!response && (
          <Button
            disabled={run.isPending}
            onClick={() => run.mutate(undefined)}
          >
            {run.isPending
              ? t('Running…')
              : submitted
                ? t('Retry same request')
                : tool.price_quota === 0
                  ? t('Run free tool')
                  : t('Run for up to {{amount}} credits', {
                      amount: creditAmount(tool.price_quota, units),
                    })}
          </Button>
        )}
        {response && (
          <>
            <CallResult response={response} units={units} />
            {confirmation && (
              <section className='space-y-3 rounded-lg border p-4'>
                <h4 className='font-medium'>{t('Confirm tool action')}</h4>
                <p className='text-sm whitespace-pre-wrap'>
                  {confirmation.message}
                </p>
                <label className='flex items-start gap-3 text-sm'>
                  <Checkbox
                    checked={confirmed}
                    disabled={run.isPending}
                    onCheckedChange={(value) => setConfirmed(value === true)}
                  />
                  {t('I reviewed this action and its charges.')}
                </label>
                <div className='flex flex-wrap gap-2'>
                  <Button
                    disabled={!confirmed || run.isPending}
                    onClick={() => run.mutate('accept')}
                  >
                    {t('Confirm and continue')}
                  </Button>
                  <Button
                    variant='outline'
                    disabled={run.isPending}
                    onClick={() => run.mutate('cancel')}
                  >
                    {t('Cancel action')}
                  </Button>
                </div>
              </section>
            )}
            <div className='flex flex-wrap gap-2'>
              <Button
                variant='outline'
                disabled={refresh.isPending || run.isPending}
                onClick={() => refresh.mutate()}
              >
                {t('Refresh result')}
              </Button>
              {canStartAnotherCall(response) && (
                <Button
                  variant='outline'
                  disabled={run.isPending}
                  onClick={newCall}
                >
                  {t('Start another call')}
                </Button>
              )}
            </div>
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
