/*
Copyright (C) 2026 LIghtJUNction
*/
import { CheckmarkCircle02Icon, Shield02Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Spinner } from '@/components/ui/spinner'

import {
  submitAssistantAdminChange,
  type AssistantAdminChangeAction,
  type AssistantAdminChangeResult,
} from './api'

function displayValue(value: unknown): string {
  if (typeof value === 'string') return value || '—'
  if (value === null || value === undefined) return '—'
  if (typeof value === 'object') {
    try {
      return JSON.stringify(value)
    } catch {
      return '—'
    }
  }
  return String(value)
}

export function AssistantAdminChangeTool(props: {
  action: AssistantAdminChangeAction
  onApplied?: () => void
}) {
  const { t } = useTranslation()
  const [applying, setApplying] = useState(false)
  const [result, setResult] = useState<AssistantAdminChangeResult | null>(null)

  const apply = async () => {
    if (applying || result) return
    setApplying(true)
    try {
      const saved = await submitAssistantAdminChange(
        props.action.confirmation_token
      )
      setResult(saved)
      if (saved.applied) props.onApplied?.()
      if (saved.warnings?.length) {
        toast.warning(saved.warnings.join('；'))
      } else if (saved.applied) {
        toast.success(t('Administrator change applied'))
      }
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t('Unable to apply the administrator change')
      )
    } finally {
      setApplying(false)
    }
  }

  if (result) {
    const hasWarnings = !result.applied || Boolean(result.warnings?.length)
    return (
      <Card
        size='sm'
        className={
          hasWarnings
            ? 'border-warning/40 bg-warning/5'
            : 'border-success/40 bg-success/5'
        }
      >
        <CardHeader>
          <CardTitle className='flex items-center gap-2'>
            <HugeiconsIcon
              icon={hasWarnings ? Shield02Icon : CheckmarkCircle02Icon}
              className={
                hasWarnings ? 'text-warning size-4' : 'text-success size-4'
              }
              strokeWidth={2}
              aria-hidden='true'
            />
            {result.applied
              ? t('Administrator change applied')
              : t('Locked price changes ignored')}
          </CardTitle>
          <CardDescription>
            {hasWarnings
              ? result.warnings?.join('；') || t('Locked price changes ignored')
              : t(
                  'The server configuration was updated and the one-time confirmation was consumed.'
                )}
          </CardDescription>
        </CardHeader>
      </Card>
    )
  }

  return (
    <Card size='sm' className='border-primary/30 bg-primary/5'>
      <CardHeader>
        <CardTitle className='flex items-center gap-2'>
          <HugeiconsIcon
            icon={Shield02Icon}
            className='text-primary size-4'
            strokeWidth={2}
            aria-hidden='true'
          />
          {props.action.type === 'admin_pricing_change'
            ? t('Model pricing change')
            : props.action.type === 'admin_model_sync'
              ? t('Add missing models')
              : t('Administrator configuration change')}
        </CardTitle>
        <CardDescription>
          {t(
            'Review the exact current and new values before applying this change.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='grid gap-3'>
        {props.action.type === 'admin_config_change' &&
        props.action.warnings?.length ? (
          <p role='status' className='text-warning text-xs'>
            {props.action.warnings.join('；')}
          </p>
        ) : null}
        {props.action.type === 'admin_config_change' &&
        (props.action.scope ||
          props.action.target_user_id ||
          props.action.channel_id) ? (
          <div className='text-muted-foreground border-border/70 grid gap-1 border-b pb-3 text-xs'>
            <p>
              {t('Scope')}:{' '}
              {props.action.scope === 'user_skills'
                ? t('User skills')
                : props.action.scope === 'channel'
                  ? t('Channel')
                  : '—'}
            </p>
            {props.action.target_user_id ? (
              <p>
                {t('Target')} {t('User')} #{props.action.target_user_id}
              </p>
            ) : null}
            {props.action.channel_id ? (
              <p>
                {t('Target')} {t('Channel')} #{props.action.channel_id}
                {props.action.channel_name
                  ? ` · ${props.action.channel_name}`
                  : ''}
              </p>
            ) : null}
          </div>
        ) : null}
        {props.action.type === 'admin_pricing_change' ? (
          <div className='grid gap-2'>
            <p className='text-sm font-medium'>
              {props.action.pricing.model_id}
            </p>
            <div className='border-border/70 bg-background/70 grid gap-2 border p-3 text-xs'>
              <div>
                <p className='text-muted-foreground'>{t('Current value')}</p>
                <p className='mt-1 whitespace-pre-wrap'>
                  {displayValue(props.action.pricing.old)}
                </p>
              </div>
              <div>
                <p className='text-muted-foreground'>{t('New value')}</p>
                <p className='mt-1 whitespace-pre-wrap'>
                  {displayValue(props.action.pricing.next)}
                </p>
              </div>
            </div>
          </div>
        ) : props.action.type === 'admin_model_sync' ? (
          <div className='border-border/70 bg-background/70 grid gap-3 border p-3'>
            <div className='text-muted-foreground text-xs'>
              {t('Upstream catalog')}: {props.action.locale || t('default')}
            </div>
            <div className='grid gap-2'>
              {props.action.models.map((model) => (
                <div
                  className='flex items-baseline justify-between gap-3 border-b pb-2 last:border-b-0 last:pb-0'
                  key={model.model_id}
                >
                  <span className='text-sm font-medium break-all'>
                    {model.model_id}
                  </span>
                  <span className='text-muted-foreground shrink-0 text-xs'>
                    {model.vendor || t('Unassigned vendor')}
                  </span>
                </div>
              ))}
            </div>
            {props.action.vendors?.length ? (
              <div className='border-border/70 grid gap-2 border-t pt-3'>
                <p className='text-muted-foreground text-xs'>{t('Vendor')}</p>
                <p className='text-sm'>
                  {props.action.vendors.map((vendor) => vendor.name).join(', ')}
                </p>
              </div>
            ) : null}
          </div>
        ) : (
          <div className='border-border/70 bg-background/70 grid gap-2 border p-3'>
            {props.action.changes.map((change) => (
              <div
                className='grid gap-1 border-b pb-2 last:border-b-0 last:pb-0'
                key={change.key}
              >
                <p className='text-sm font-medium'>{change.label}</p>
                <p className='text-muted-foreground text-xs break-words'>
                  {t('Current value')}: {displayValue(change.old_value)}
                </p>
                <p className='text-xs break-words'>
                  {t('New value')}: {displayValue(change.new_value)}
                </p>
              </div>
            ))}
          </div>
        )}
        <p className='text-muted-foreground text-xs leading-5'>
          {t('This confirmation expires soon and can only be used once.')}
        </p>
        <Button type='button' onClick={() => void apply()} disabled={applying}>
          {applying ? <Spinner data-icon='inline-start' /> : null}
          {t('Confirm and apply')}
        </Button>
      </CardContent>
    </Card>
  )
}
