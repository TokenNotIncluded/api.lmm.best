/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import {
  Copy01Icon,
  Loading03Icon,
  SparklesIcon,
  Tick02Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { Outlet, useRouterState } from '@tanstack/react-router'
import { Fragment, useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { PublicLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { StoreNavigation } from './store-navigation'
import type { StoreProduct } from './types'

export function StoreShell() {
  const user = useAuthStore((state) => state.auth.user)
  const path = useRouterState({ select: (state) => state.location.pathname })
  if (path.startsWith('/store/claim/')) {
    return (
      <main className='bg-background text-foreground min-h-svh px-4 py-10'>
        <div className='mx-auto max-w-2xl'>
          <Outlet />
        </div>
      </main>
    )
  }
  return (
    <PublicLayout
      className={path === '/store' ? 'flex flex-col' : undefined}
      mainClassName={path === '/store' ? 'flex flex-1 flex-col' : undefined}
    >
      <div
        className={cn(
          'mx-auto flex w-full max-w-6xl flex-col gap-5 sm:gap-7',
          path === '/store' && 'flex flex-1 flex-col'
        )}
      >
        <StoreNavigation path={path} canReview={!!user && user.role >= 10} />
        <Outlet />
      </div>
    </PublicLayout>
  )
}
export function StoreAuthGate({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  if (user) return <Fragment key={user.id}>{children}</Fragment>
  const redirect = `${window.location.pathname}${window.location.search}`
  return (
    <div className='space-y-4 py-10'>
      <h1 className='text-lg font-semibold'>{t('Sign in to continue')}</h1>
      <p className='text-muted-foreground text-sm'>
        {t(
          'You can browse products without signing in. Sign in to buy or manage products.'
        )}
      </p>
      <Button
        render={
          <a href={`/sign-in?redirect=${encodeURIComponent(redirect)}`} />
        }
      >
        {t('Sign in')}
      </Button>
    </div>
  )
}
export function StoreAmount({ quota }: { quota: number }) {
  const money = useWalletCurrency()
  const { t } = useTranslation()
  if (!Number.isSafeInteger(quota) || quota < 0) {
    return <span>{t('No data provided')}</span>
  }
  return (
    <span className='tabular-nums'>
      {money.formatQuota(quota, { abbreviate: false, digitsSmall: 2 })}
    </span>
  )
}
export function StoreLoading() {
  const { t } = useTranslation()
  return (
    <div
      role='status'
      className='text-muted-foreground flex items-center gap-2 py-8 text-sm'
    >
      <HugeiconsIcon
        icon={Loading03Icon}
        className='size-4 animate-spin motion-reduce:animate-none'
      />
      {t('Loading...')}
    </div>
  )
}
export function StoreError({
  error,
  retry,
}: {
  error: unknown
  retry?: () => void
}) {
  const { t } = useTranslation()
  if (!error) return null
  return (
    <div
      role='alert'
      className='border-destructive/30 bg-destructive/5 text-destructive flex flex-wrap items-center justify-between gap-3 rounded-md border p-3 text-sm'
    >
      <span>
        {t(error instanceof Error ? error.message : 'Store request failed')}
      </span>
      {retry && (
        <Button size='sm' variant='outline' onClick={retry}>
          {t('Retry')}
        </Button>
      )}
    </div>
  )
}
export function StoreBadges({ product }: { product: StoreProduct }) {
  const { t } = useTranslation()
  return (
    <div className='flex flex-wrap items-center gap-2 text-xs empty:hidden'>
      {product.unlimited_supply && (
        <span className='bg-muted text-muted-foreground rounded px-2 py-1'>
          {t('Unlimited supply')}
        </span>
      )}
      {product.category?.name && (
        <span className='bg-muted text-muted-foreground rounded px-2 py-1'>
          {product.category.name}
        </span>
      )}
      {product.official && (
        <span className='bg-primary/10 text-primary inline-flex items-center gap-1 rounded px-2 py-1'>
          <HugeiconsIcon icon={Tick02Icon} className='size-3.5' />
          {t('Official')}
        </span>
      )}
      {product.status === 'published' &&
        !product.trading_paused &&
        ((product.unlimited_supply && product.sale_limit == null) ||
          (product.sale_available ?? 0) > 0) &&
        product.promotion_expires_at > Date.now() / 1000 && (
          <Tooltip>
            <TooltipTrigger
              render={
                <span
                  tabIndex={0}
                  aria-label={t('Promoted product')}
                  className='text-warning inline-flex items-center gap-1 rounded px-1 py-1'
                />
              }
            >
              <HugeiconsIcon icon={SparklesIcon} className='size-4' />
              {t('Promoted')}
            </TooltipTrigger>
            <TooltipContent>
              {t(
                'The seller paid for higher placement. Promotion does not guarantee product quality.'
              )}
            </TooltipContent>
          </Tooltip>
        )}
    </div>
  )
}

export function CopyStoreValue({
  value,
  label = 'Copy',
  disabled = false,
}: {
  value: string
  label?: string
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  const [error, setError] = useState(false)
  useEffect(() => {
    setCopied(false)
    setError(false)
  }, [value])
  async function copy() {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      setError(false)
    } catch {
      setError(true)
    }
  }
  return (
    <span className='inline-flex flex-col items-start gap-1'>
      <Button
        type='button'
        size='sm'
        variant='outline'
        disabled={disabled}
        onClick={() => void copy()}
      >
        <HugeiconsIcon
          icon={copied ? Tick02Icon : Copy01Icon}
          className='size-4'
        />
        {t(copied ? 'Copied' : label)}
      </Button>
      {error && (
        <span role='alert' className='text-destructive text-xs'>
          {t('Copy failed. Select and copy the text manually.')}
        </span>
      )}
    </span>
  )
}
