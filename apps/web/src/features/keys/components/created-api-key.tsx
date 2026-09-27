/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { Check, Copy, Eye, EyeOff, Loader2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from '@/components/ui/sheet'
import { copyToClipboard } from '@/lib/copy-to-clipboard'
import { cn } from '@/lib/utils'

import { deleteApiKey } from '../api'
import { CCSwitchDialog } from './dialogs/cc-switch-dialog'
import { KeyCreatedBurst, KeyCreatedBurstStyles } from './key-created-burst'

export type CreatedApiKeySecret = { id: number; name: string; key: string }

/** Deliberately local UI state: never place creation secrets in a query/store cache. */
export function CreatedApiKey({
  secret,
  onClose,
  onRevoked,
}: {
  secret: CreatedApiKeySecret
  onClose: () => void
  onRevoked: () => void
}) {
  const { t } = useTranslation()
  const [revealed, setRevealed] = useState(false)
  const [configuring, setConfiguring] = useState(false)
  const [revokeConfirm, setRevokeConfirm] = useState(false)
  const [pending, setPending] = useState(false)
  const [result, setResult] = useState('')
  // Copy is a small state machine so the button can confirm itself without
  // shifting the layout; the burst replays once per successful copy.
  const [copyState, setCopyState] = useState<'idle' | 'copying' | 'copied'>(
    'idle'
  )
  const [burst, setBurst] = useState<number | null>(null)
  const [saved, setSaved] = useState(false)
  const copyTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined)
  const savedTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined)
  const key = `sk-${secret.key}`

  useEffect(
    () => () => {
      clearTimeout(copyTimerRef.current)
      clearTimeout(savedTimerRef.current)
    },
    []
  )

  const copy = async () => {
    setCopyState('copying')
    const ok = await copyToClipboard(key)
    clearTimeout(copyTimerRef.current)
    if (ok) {
      setCopyState('copied')
      setBurst(Date.now())
      setResult('Copied')
      copyTimerRef.current = setTimeout(() => setCopyState('idle'), 1800)
    } else {
      setCopyState('idle')
      setResult('Failed to copy to clipboard')
    }
  }

  const confirmSaved = () => {
    setSaved(true)
    clearTimeout(savedTimerRef.current)
    savedTimerRef.current = setTimeout(() => setSaved(false), 2600)
  }
  const check = async () => {
    setPending(true)
    setResult('')
    try {
      const response = await fetch('/v1/models', {
        headers: { Authorization: `Bearer ${key}` },
        credentials: 'omit',
        cache: 'no-store',
        signal: AbortSignal.timeout(15_000),
      })
      const data = await response.json()
      setResult(
        response.ok && Array.isArray(data.data)
          ? 'Key authentication succeeded. No model request was sent or billed.'
          : 'Key check failed. Check access, expiry and allowed IP addresses.'
      )
    } catch {
      setResult(
        'Key check failed. Check access, expiry and allowed IP addresses.'
      )
    } finally {
      setPending(false)
    }
  }
  const revoke = async () => {
    setPending(true)
    try {
      const response = await deleteApiKey(secret.id)
      if (!response.success) throw new Error('revoke failed')
      onRevoked()
      onClose()
    } catch {
      setResult('Could not revoke the key. Try again from API Keys.')
    } finally {
      setPending(false)
    }
  }
  return (
    <>
      <KeyCreatedBurstStyles />
      <Sheet
        open
        onOpenChange={(open) => {
          if (!open) onClose()
        }}
      >
        <SheetContent className='sm:max-w-xl'>
          <SheetHeader>
            <SheetTitle>{t('API key created')}</SheetTitle>
            <SheetDescription>
              {t(
                'This is the only time the full API key is available. Save it before closing; it cannot be retrieved later.'
              )}
            </SheetDescription>
          </SheetHeader>
          <div className='space-y-4 p-4'>
            <p className='text-sm font-medium'>{secret.name}</p>
            <div className='relative'>
              <Input
                aria-label={t('API Key')}
                type={revealed ? 'text' : 'password'}
                value={key}
                readOnly
                autoComplete='off'
                className='font-mono'
              />
              <KeyCreatedBurst burstKey={burst} onDone={() => setBurst(null)} />
            </div>
            <div className='bg-warning/10 text-warning flex items-start gap-2 rounded-md px-3 py-2 text-xs'>
              <EyeOff className='mt-px size-3.5 shrink-0' aria-hidden='true' />
              <span>
                {t('Copy it now — this is the only time it is shown.')}
              </span>
            </div>
            <div className='flex flex-wrap gap-2'>
              <Button
                type='button'
                variant='outline'
                aria-label={t(revealed ? 'Hide API key' : 'Show API key')}
                onClick={() => setRevealed(!revealed)}
              >
                {revealed ? (
                  <EyeOff className='h-4 w-4' aria-hidden='true' />
                ) : (
                  <Eye className='h-4 w-4' aria-hidden='true' />
                )}
                {t(revealed ? 'Hide' : 'Show')}
              </Button>
              <Button
                type='button'
                className={cn(
                  'min-w-32 transition-colors',
                  copyState === 'copied' && 'console-status-success'
                )}
                disabled={copyState === 'copying'}
                onClick={() => void copy()}
              >
                {copyState === 'copying' ? (
                  <Loader2
                    className='h-4 w-4 animate-spin'
                    aria-hidden='true'
                  />
                ) : copyState === 'copied' ? (
                  <Check
                    className='console-status-success-icon h-4 w-4'
                    aria-hidden='true'
                  />
                ) : (
                  <Copy className='h-4 w-4' aria-hidden='true' />
                )}
                {t('Copy Key')}
              </Button>
              <Button
                type='button'
                variant='outline'
                onClick={() => setConfiguring(true)}
              >
                {t('Configure with CC Switch')}
              </Button>
            </div>
            <a
              href='/guide'
              className='text-primary text-sm underline underline-offset-4'
              onClick={onClose}
            >
              {t('Other client setup guides')}
            </a>
            <p className='text-muted-foreground text-xs'>
              {t(
                'The connection check only verifies this key and lists models. It does not complete your first successful model request.'
              )}
            </p>
            <Button
              type='button'
              variant='outline'
              disabled={pending}
              onClick={() => void check()}
            >
              {t('Check key connection')}
            </Button>
            {!revokeConfirm ? (
              <Button
                type='button'
                variant='ghost'
                disabled={pending}
                onClick={() => setRevokeConfirm(true)}
              >
                {t('Revoke this key')}
              </Button>
            ) : (
              <div className='space-y-2'>
                <p>
                  {t(
                    'Revoking stops clients using this key. Confirm to continue.'
                  )}
                </p>
                <Button
                  type='button'
                  variant='destructive'
                  disabled={pending}
                  onClick={() => void revoke()}
                >
                  {t('Confirm revocation')}
                </Button>
                <Button
                  type='button'
                  variant='ghost'
                  disabled={pending}
                  onClick={() => setRevokeConfirm(false)}
                >
                  {t('Cancel')}
                </Button>
              </div>
            )}
            {result && (
              <p role='status' className='text-sm'>
                {t(result)}
              </p>
            )}
            <Button
              type='button'
              variant={saved ? 'default' : 'outline'}
              className={cn(
                'transition-colors',
                saved && 'console-status-success'
              )}
              onClick={() => {
                // Reassurance for a one-time secret: the button itself
                // acknowledges the click before the sheet closes.
                confirmSaved()
                onClose()
              }}
            >
              {t('I saved the key, close')}
            </Button>
          </div>
        </SheetContent>
      </Sheet>
      {configuring && (
        <CCSwitchDialog
          open
          onOpenChange={setConfiguring}
          tokenKey={key}
          tokenId={secret.id}
        />
      )}
    </>
  )
}
