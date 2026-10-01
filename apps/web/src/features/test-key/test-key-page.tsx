/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { isAxiosError } from 'axios'
import { Check, Copy, KeyRound } from 'lucide-react'
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { createApiKey } from '@/features/keys/api'
import { ApiBaseUrl } from '@/features/keys/components/api-base-url'
import { useStatus } from '@/hooks/use-status'
import { getUserGroups } from '@/lib/api'
import { isConsoleActivated } from '@/lib/console-activation'
import { copyToClipboard } from '@/lib/copy-to-clipboard'
import { getCurrencyLabel } from '@/lib/currency'
import { useAuthStore } from '@/stores/auth-store'

import { TEST_KEY_PATH } from './bookmarklet'
import { useTestKeyCopy } from './copy'
import { testKeyPayload, testKeyQuota } from './test-key'

type Result =
  | { state: 'created'; secret: string; copied: boolean }
  | { state: 'uncertain' | 'hidden' }

function TestKeyForm({
  userId,
  sessionId,
}: {
  userId: number
  sessionId?: string
}) {
  const { t } = useTranslation()
  const q = useTestKeyCopy()
  const {
    capabilitiesReady,
    error: statusError,
    refetch: refetchStatus,
  } = useStatus()
  const [amount, setAmount] = useState('1')
  const [group, setGroup] = useState('')
  const [confirmations, setConfirmations] = useState(0)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState<Result | null>(null)
  const pending = useRef(false)
  const mounted = useRef(false)
  const revision = useRef(0)
  const groups = useQuery({
    queryKey: ['test-key-groups', userId, sessionId],
    queryFn: async () => {
      const response = await getUserGroups()
      if (!response.success || !response.data) {
        throw new Error('Groups unavailable')
      }
      return response.data
    },
    retry: false,
    refetchOnWindowFocus: false,
  })
  const currentSession = () => {
    const auth = useAuthStore.getState().auth
    return (
      mounted.current &&
      auth.user?.id === userId &&
      auth.session?.sid === sessionId &&
      isConsoleActivated(auth.user)
    )
  }
  useEffect(() => {
    mounted.current = true
    const hideSecret = () => {
      mounted.current = false
      revision.current += 1
      setBusy(false)
      setResult((previous) =>
        previous || pending.current ? { state: 'hidden' } : null
      )
    }
    const restore = () => {
      mounted.current = true
    }
    window.addEventListener('pagehide', hideSecret)
    window.addEventListener('pageshow', restore)
    return () => {
      mounted.current = false
      window.removeEventListener('pagehide', hideSecret)
      window.removeEventListener('pageshow', restore)
    }
  }, [])
  const warning = groups.data?.[group]?.warning
  const requiredConfirmations = warning?.enabled
    ? Math.max(
        1,
        Math.min(
          3,
          Number.isFinite(warning.confirmations)
            ? Math.trunc(warning.confirmations)
            : 1
        )
      )
    : 0
  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (pending.current || result || !currentSession() || !capabilitiesReady) {
      return
    }
    const quota = testKeyQuota(amount)
    if (quota === null) {
      setError(q('invalidBudget'))
      return
    }
    if (!groups.data?.[group] || confirmations < requiredConfirmations) return
    // Only an explicit click creates a credential. Navigating to this page does
    // not mutate the account, including after login or a StrictMode remount.
    pending.current = true
    setBusy(true)
    setError('')
    const requestRevision = revision.current
    const active = () =>
      currentSession() && requestRevision === revision.current
    try {
      const response = await createApiKey(
        testKeyPayload(quota, group, confirmations)
      )
      if (!active()) return
      if (!response.success) {
        setError(response.message || q('failed'))
        return
      }
      const raw = response.data?.key
      if (typeof raw !== 'string' || !raw.trim()) {
        setResult({ state: 'uncertain' })
        return
      }
      const secret = raw.startsWith('sk-') ? raw : `sk-${raw}`
      // Keep the reveal out of storage, URLs, query caches and the opener.
      setResult({ state: 'created', secret, copied: false })
      const copied = await copyToClipboard(secret)
      if (active()) {
        setResult((previous) =>
          previous?.state === 'created' ? { ...previous, copied } : previous
        )
      }
    } catch (failure) {
      if (!active()) return
      if (
        isAxiosError(failure) &&
        failure.response?.status === 422 &&
        failure.response.data?.code === 'GROUP_WARNING_CONFIRMATION_REQUIRED'
      ) {
        // The server rejected this request before creating a key. Refresh the
        // warning and require another explicit confirmation and submit.
        setConfirmations(0)
        await groups.refetch()
        if (active()) setError(failure.response.data.message || q('failed'))
        return
      }
      // A lost response may follow a successful POST. Do not silently mint a
      // second key on retry; take the user to their existing key list instead.
      if (active()) setResult({ state: 'uncertain' })
    } finally {
      pending.current = false
      if (active()) setBusy(false)
    }
  }
  const copySecret = async () => {
    if (result?.state !== 'created' || !currentSession()) return
    const copied = await copyToClipboard(result.secret)
    if (currentSession()) {
      setResult((previous) =>
        previous?.state === 'created' ? { ...previous, copied } : previous
      )
    }
  }
  return (
    <div className='space-y-5'>
      <p className='text-muted-foreground text-sm leading-6'>{q('scope')}</p>
      {result ? (
        <section
          className='bg-muted/30 space-y-3 rounded-xl border p-4'
          aria-live='polite'
        >
          {result.state === 'created' ? (
            <>
              <p className='text-sm font-medium'>
                {q(result.copied ? 'saved' : 'manualCopy')}
              </p>
              <Label htmlFor='test-key-secret'>API Key</Label>
              <Input
                id='test-key-secret'
                value={result.secret}
                readOnly
                autoComplete='off'
                spellCheck={false}
                className='font-mono text-xs'
                onFocus={(event) => event.target.select()}
              />
              <Button
                type='button'
                className='min-h-11 w-full'
                variant='outline'
                onClick={() => void copySecret()}
              >
                {result.copied ? (
                  <Check aria-hidden='true' />
                ) : (
                  <Copy aria-hidden='true' />
                )}
                {t('Copy')}
              </Button>
              <p className='text-muted-foreground text-xs leading-5'>
                {q('oneTime')}
              </p>
            </>
          ) : (
            <p className='text-sm'>{q(result.state)}</p>
          )}
        </section>
      ) : (
        <form
          onSubmit={(event) => void submit(event)}
          className='space-y-4'
          aria-busy={busy}
        >
          <div className='space-y-2'>
            <Label htmlFor='test-key-budget'>
              {q('budget', { currency: getCurrencyLabel() })}
            </Label>
            <Input
              id='test-key-budget'
              value={amount}
              onChange={(event) => setAmount(event.target.value)}
              type='number'
              min='0'
              step='any'
              inputMode='decimal'
              required
              disabled={busy}
              className='h-11 text-base'
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor='test-key-group'>{t('Group')}</Label>
            <NativeSelect
              id='test-key-group'
              value={group}
              onChange={(event) => {
                setGroup(event.target.value)
                setConfirmations(0)
              }}
              required
              disabled={busy || !groups.isSuccess}
              className='w-full [&_select]:h-11'
            >
              <NativeSelectOption value='' disabled>
                {t('Select a group')}
              </NativeSelectOption>
              {Object.entries(groups.data ?? {}).map(([name, info]) => (
                <NativeSelectOption key={name} value={name}>
                  {name}
                  {info.desc ? ` · ${info.desc}` : ''}
                </NativeSelectOption>
              ))}
            </NativeSelect>
            {groups.isLoading && (
              <p className='text-muted-foreground text-xs' role='status'>
                {t('Loading...')}
              </p>
            )}
            {groups.isError && (
              <div role='alert' className='text-destructive text-sm'>
                {q('groupsFailed')}{' '}
                <Button
                  type='button'
                  variant='link'
                  onClick={() => void groups.refetch()}
                >
                  {t('Retry')}
                </Button>
              </div>
            )}
            {groups.isSuccess && Object.keys(groups.data).length === 0 && (
              <p role='status' className='text-muted-foreground text-sm'>
                {q('noGroups')}
              </p>
            )}
          </div>
          {warning?.enabled && (
            <div className='bg-muted/40 space-y-3 rounded-lg border p-3'>
              <p className='text-sm whitespace-pre-wrap'>{warning.message}</p>
              <Button
                type='button'
                variant='outline'
                disabled={busy || confirmations >= requiredConfirmations}
                onClick={() =>
                  setConfirmations((count) =>
                    Math.min(requiredConfirmations, count + 1)
                  )
                }
              >
                {q('confirmGroup', {
                  current: confirmations,
                  total: requiredConfirmations,
                })}
              </Button>
            </div>
          )}
          {statusError && (
            <p role='alert' className='text-sm'>
              {t('Failed to load data')}{' '}
              <Button
                type='button'
                variant='link'
                onClick={() => void refetchStatus()}
              >
                {t('Retry')}
              </Button>
            </p>
          )}
          {error && (
            <p role='alert' className='text-destructive text-sm'>
              {error}
            </p>
          )}
          <Button
            type='submit'
            className='min-h-11 w-full'
            disabled={
              busy ||
              !capabilitiesReady ||
              !groups.data?.[group] ||
              confirmations < requiredConfirmations
            }
          >
            <KeyRound aria-hidden='true' />
            {busy ? t('Creating...') : q('create')}
          </Button>
        </form>
      )}
      <ApiBaseUrl />
      <p className='text-muted-foreground text-xs leading-5'>{q('lifetime')}</p>
      <a
        href='/keys'
        target='_blank'
        rel='noopener noreferrer'
        className='inline-flex min-h-11 items-center text-sm underline underline-offset-4'
      >
        {t('API Keys')}
      </a>
    </div>
  )
}

export function TestKeyPage() {
  const { t } = useTranslation()
  const q = useTestKeyCopy()
  const { user, session } = useAuthStore((state) => state.auth)
  const framed = typeof window !== 'undefined' && window.self !== window.top
  return (
    <main className='bg-background text-foreground min-h-svh px-5 py-7 sm:px-7'>
      <div className='mx-auto w-full max-w-md'>
        <header className='mb-6 border-b pb-5'>
          <p className='text-muted-foreground mb-2 text-xs font-medium'>LMM</p>
          <h1 className='text-2xl font-semibold tracking-tight'>
            {q('title')}
          </h1>
        </header>
        {framed ? (
          <>
            <p className='mb-4 text-sm'>{q('frame')}</p>
            <a
              href={TEST_KEY_PATH}
              target='_blank'
              rel='noopener noreferrer'
              className='underline'
            >
              {q('open')}
            </a>
          </>
        ) : !user ? (
          <>
            <p className='mb-4 text-sm'>{q('login')}</p>
            <Button
              render={
                <Link to='/sign-in' search={{ redirect: TEST_KEY_PATH }} />
              }
            >
              {t('Sign in')}
            </Button>
          </>
        ) : !isConsoleActivated(user) ? (
          <>
            <p className='mb-4 text-sm'>{q('access')}</p>
            <Button render={<Link to='/getting-started' />}>
              {t('Getting started')}
            </Button>
          </>
        ) : (
          <TestKeyForm
            key={`${user.id}:${session?.sid ?? ''}`}
            userId={user.id}
            sessionId={session?.sid}
          />
        )}
      </div>
    </main>
  )
}
