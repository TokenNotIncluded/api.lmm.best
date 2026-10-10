/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQueryClient } from '@tanstack/react-query'
import { Check, CircleAlert, Loader2, ShieldCheck } from 'lucide-react'
import { useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import { api } from '@/lib/api'

import { AssistantUIPreferencesCard } from './assistant-ui-preferences-card'
import {
  type AssistantWorkspaceAction,
  type AssistantWorkspaceConfirmationAction,
  workspaceActionTitle,
} from './assistant-workspace-contract'

export function AssistantWorkspaceActionCard({
  action,
  disabled = false,
}: {
  action: AssistantWorkspaceAction
  disabled?: boolean
}) {
  if (!action.requires_confirmation) {
    return <AssistantUIPreferencesCard key={action.action_id} action={action} disabled={disabled} />
  }
  return <AssistantWorkspaceConfirmationCard action={action} disabled={disabled} />
}

function AssistantWorkspaceConfirmationCard({
  action,
  disabled = false,
}: {
  action: AssistantWorkspaceConfirmationAction
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const wallet = useWalletCurrency()
  const id = useId()
  const submitted = useRef(false)
  const [state, setState] = useState<
    'ready' | 'sending' | 'completed' | 'failed' | 'cancelled'
  >('ready')
  const [receipt, setReceipt] = useState('')
  const [maxPrice, setMaxPrice] = useState(0)
  const [maxTotal, setMaxTotal] = useState(0)
  const [calls, setCalls] = useState(1)
  const [minutes, setMinutes] = useState(10)
  const [inputError, setInputError] = useState(false)
  const market =
    action.tool === 'connect_market_tool' ? action.preview : undefined
  const busy = state !== 'ready' || disabled
  const invalidLimit =
    !Number.isSafeInteger(maxPrice) ||
    maxPrice < 0 ||
    !Number.isSafeInteger(maxTotal) ||
    maxTotal < maxPrice ||
    !Number.isInteger(calls) ||
    calls < 1 ||
    calls > 100 ||
    !Number.isInteger(minutes) ||
    minutes < 1 ||
    minutes > 1440

  async function confirm() {
    if (submitted.current || busy || (market && invalidLimit)) return
    submitted.current = true
    setState('sending')
    try {
      const { data } = await api.post<{
        success: boolean
        data?: { issue_id?: number; id?: number }
      }>(
        action.tool === 'send_invitation'
          ? '/api/assistant/workspace/invitation/confirm'
          : '/api/assistant/workspace/confirm',
        {
          confirmation_token: action.confirmation_token,
          confirmed: true,
          ...(market
            ? {
                grant_limits: {
                  max_price_quota: maxPrice,
                  max_total_quota: maxTotal,
                  max_calls: calls,
                  lifetime_seconds: minutes * 60,
                },
              }
            : {}),
        }
      )
      if (!data.success) throw new Error('workspace action failed')
      const issueID =
        action.tool === 'create_site_issue'
          ? data.data?.issue_id
          : action.tool === 'update_site_issue'
            ? data.data?.id
            : undefined
      setReceipt(issueID ? t('Issue #{{id}}', { id: issueID }) : '')
      setState('completed')
      if (action.tool === 'set_overview_greeting') {
        void client.invalidateQueries({ queryKey: ['overview-greeting'] })
      }
    } catch {
      // Do not retry an uncertain write. A new preview is required, including
      // after an SMTP timeout; the confirmation is single-use on the server.
      setState('failed')
    }
  }

  return (
    <section
      className='bg-card w-full min-w-0 overflow-hidden rounded-2xl border shadow-sm'
      aria-labelledby={`${id}-title`}
    >
      <header className='flex items-center gap-3 border-b px-5 py-4'>
        <ShieldCheck
          className='text-primary size-5 shrink-0'
          aria-hidden='true'
        />
        <div>
          <p className='text-muted-foreground text-[11px] font-medium tracking-widest uppercase'>
            {t('Review before applying')}
          </p>
          <h3 id={`${id}-title`} className='font-medium'>
            {t(workspaceActionTitle(action.tool))}
          </h3>
        </div>
      </header>
      <div className='space-y-4 px-5 py-5 text-sm'>
        {action.tool === 'set_overview_greeting' && (
          <>
            <p className='text-muted-foreground'>
              {t('Language')}: <strong>{action.preview.language}</strong>
            </p>
            <blockquote className='bg-muted/40 rounded-xl px-4 py-5 text-lg break-words whitespace-pre-wrap'>
              {action.preview.template || t('Use the default greeting')}
            </blockquote>
          </>
        )}
        {action.tool === 'send_invitation' && (
          <>
            <p>
              {t('Recipient')}:{' '}
              <strong className='break-all'>{action.preview.email}</strong>
            </p>
            <p className='text-muted-foreground'>
              {t(
                'This sends one invitation with your referral link. The recipient is not enrolled automatically.'
              )}
            </p>
          </>
        )}
        {action.tool === 'create_site_issue' && (
          <>
            <h4 className='text-base font-medium break-words'>
              {action.preview.title}
            </h4>
            <p className='text-muted-foreground'>
              {t(issueKindLabel(action.preview.kind))} ·{' '}
              {t(
                action.preview.visibility === 'admin'
                  ? 'Administrators only'
                  : 'Reporter and administrators'
              )}
            </p>
            <p className='max-h-64 overflow-y-auto break-words whitespace-pre-wrap'>
              {action.preview.body}
            </p>
          </>
        )}
        {action.tool === 'update_site_issue' && (
          <>
            <p className='font-medium'>
              {t('Issue #{{id}}', { id: action.preview.issue_id })}
            </p>
            <p>
              {t('Status')}: {t(issueStatusLabel(action.preview.status))}
            </p>
            <p>
              {t(
                action.preview.visibility === 'admin'
                  ? 'Administrators only'
                  : 'Reporter and administrators'
              )}
            </p>
            <p className='break-words whitespace-pre-wrap'>
              {action.preview.note}
            </p>
          </>
        )}
        {market && (
          <>
            <div>
              <h4 className='font-medium break-words'>
                {market.service_name} / {market.tool_name}
              </h4>
              <p className='text-muted-foreground mt-1 break-words'>
                {market.description}
              </p>
            </div>
            <div className='bg-muted/40 space-y-2 rounded-xl p-3'>
              <p>
                {t(
                  market.billing_mode
                    ? 'Maximum charge per call'
                    : 'Listed price'
                )}
                : <strong>{wallet.formatQuota(market.price_quota)}</strong>
              </p>
              <p>
                {t('Billing mode')}:{' '}
                {t(market.billing_mode ? 'Metered usage' : 'Fixed price')}
              </p>
              {market.billing_mode && (
                <p className='text-muted-foreground'>
                  {t(
                    'The maximum amount is reserved first. Actual usage determines the final charge within this limit.'
                  )}
                </p>
              )}
              {market.billing_mode === 'input_tokens' && (
                <p>
                  {t('Input token rate')}:{' '}
                  {wallet.formatQuota(market.input_token_price_quota ?? 0)} /
                  1,000,000 · {t('Maximum input tokens')}:{' '}
                  {market.max_input_tokens}
                </p>
              )}
              {market.billing_rules?.map((rule) => (
                <p key={rule.metric} className='font-mono text-xs break-words'>
                  {rule.metric}: {t('Rate')}{' '}
                  {wallet.formatQuota(rule.rate_quota)} /{' '}
                  {marketMetricScale(rule.metric).toLocaleString()} ·{' '}
                  {t('Maximum quantity')}: {rule.max_quantity.toLocaleString()}
                </p>
              ))}
              {market.permissions.length > 0 && (
                <p className='break-words'>
                  {t('Permissions')}: {market.permissions.join(', ')}
                </p>
              )}
            </div>
            <p className='text-muted-foreground'>
              {t(
                'Set your own limits. Access is for this tool version only. Authorizing access does not run the tool or make a purchase.'
              )}
            </p>
            <div className='grid gap-3 sm:grid-cols-2'>
              {(
                [
                  ['Maximum price per call', maxPrice, setMaxPrice],
                  ['Total spending limit', maxTotal, setMaxTotal],
                ] as const
              ).map(([label, value, update]) => (
                <label key={label} className='space-y-2'>
                  <span className='block'>
                    {t(label)} ({wallet.label})
                  </span>
                  <Input
                    key={`${wallet.currency}:${wallet.quotaToInput(1)}`}
                    type='number'
                    min={0}
                    step='any'
                    disabled={busy}
                    defaultValue={wallet.quotaToInput(value)}
                    onChange={(event) => {
                      const amount = Number(event.target.value)
                      let quota = Number.NaN
                      try {
                        quota = wallet.amountToQuota(amount)
                      } catch {
                        /* Invalid input cannot grant access. */
                      }
                      const valid =
                        event.target.value.trim() !== '' &&
                        Number.isFinite(amount) &&
                        amount >= 0 &&
                        Number.isSafeInteger(quota) &&
                        quota >= 0
                      update(valid ? quota : Number.NaN)
                      setInputError(!valid)
                    }}
                  />
                </label>
              ))}
              <label className='space-y-2'>
                <span className='block'>{t('Maximum calls')}</span>
                <Input
                  type='number'
                  min={1}
                  max={100}
                  step={1}
                  disabled={busy}
                  value={calls}
                  onChange={(event) => setCalls(Number(event.target.value))}
                />
              </label>
              <label className='space-y-2'>
                <span className='block'>{t('Expires after (minutes)')}</span>
                <Input
                  type='number'
                  min={1}
                  max={1440}
                  step={1}
                  disabled={busy}
                  value={minutes}
                  onChange={(event) => setMinutes(Number(event.target.value))}
                />
              </label>
            </div>
            {(invalidLimit || inputError) && (
              <p role='alert' className='text-destructive'>
                {t(
                  'Enter valid limits. Total spending must cover the per-call limit.'
                )}
              </p>
            )}
          </>
        )}
        {state === 'ready' && (
          <p className='text-muted-foreground text-xs'>
            {t(
              'This preview expires after ten minutes. Current permissions are checked again when you confirm.'
            )}
          </p>
        )}
        {state === 'completed' && (
          <p role='status' className='flex items-center gap-2'>
            <Check className='size-4' />
            {t(
              action.tool === 'send_invitation'
                ? 'Invitation accepted by the mail service. Delivery is not confirmed.'
                : action.tool === 'connect_market_tool'
                  ? 'Tool access authorized. No tool has been called.'
                  : 'Change applied.'
            )}
            {receipt && ` · ${receipt}`}
          </p>
        )}
        {state === 'failed' && (
          <p role='alert' className='text-destructive flex items-start gap-2'>
            <CircleAlert className='mt-0.5 size-4 shrink-0' />
            {t(
              'The result could not be confirmed. Check the current state before preparing a new preview. Do not resend automatically.'
            )}
          </p>
        )}
        {state === 'cancelled' && (
          <p role='status'>{t('Cancelled. No request was submitted.')}</p>
        )}
      </div>
      {(state === 'ready' || state === 'sending') && (
        <footer className='flex flex-wrap justify-end gap-2 border-t px-5 py-3'>
          <Button
            type='button'
            variant='ghost'
            disabled={busy}
            onClick={() => setState('cancelled')}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            disabled={busy || (Boolean(market) && invalidLimit)}
            onClick={() => void confirm()}
          >
            {state === 'sending' && <Loader2 className='size-4 animate-spin' />}
            {t(
              action.tool === 'send_invitation'
                ? 'Confirm and send invitation'
                : action.tool === 'connect_market_tool'
                  ? 'Authorize tool access'
                  : 'Confirm change'
            )}
          </Button>
        </footer>
      )}
    </section>
  )
}

function issueKindLabel(kind: string) {
  return (
    (
      {
        bug: 'Bug report',
        security: 'Security vulnerability',
        experience: 'Experience improvement',
        feature: 'Feature request',
      } as Record<string, string>
    )[kind] ?? kind
  )
}
function issueStatusLabel(status: string) {
  return (
    (
      {
        open: 'Open',
        triaged: 'Triaged',
        in_progress: 'In progress',
        resolved: 'Resolved',
        declined: 'Declined',
      } as Record<string, string>
    )[status] ?? status
  )
}

function marketMetricScale(metric: string): number {
  return (
    (
      {
        input_tokens: 1000000,
        output_tokens: 1000000,
        input_characters: 1000,
        output_characters: 1000,
        images: 1,
        audio_milliseconds: 1000,
        video_milliseconds: 1000,
        cpu_core_milliseconds: 1000,
        memory_mib_seconds: 1024,
        gpu_milliseconds: 1000,
        vm_milliseconds: 1000,
        storage_mib_seconds: 3686400,
      } as Record<string, number>
    )[metric] ?? 1
  )
}
