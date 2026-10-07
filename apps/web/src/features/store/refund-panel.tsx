/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { Textarea } from '@/components/ui/textarea'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import { useAuthStore } from '@/stores/auth-store'

import {
  storeRefundNativeAmountSupported,
  storeRefundNativeAmountText,
} from './refund-amount'
import { storeRefundApi, StoreRefundRequestError } from './refund-api'
import {
  storeRefundAmountMax,
  storeRefundInput,
  storeRefundModes,
  storeRefundPending,
  storeRefundQuantityMax,
} from './refund-input'
import type {
  StorePickupRefundProof,
  StoreRefund,
  StoreRefundAudience,
  StoreRefundInput,
  StoreRefundMode,
  StoreRefundView,
} from './refund-types'
import { StoreError, StoreLoading } from './shared'
import type { StoreClaimMetadata } from './types'
import { storeDate } from './utils'

const modeLabels = {
  full: 'Full refund',
  quantity: 'Refund by quantity',
  amount: 'Refund by amount',
} as const
const statusLabels = {
  requested: 'Refund requested',
  awaiting_provider: 'Awaiting payment provider',
  reconciliation_required: 'Payment refunded; platform settlement pending',
  completed: 'Refund completed',
  rejected: 'Refund rejected',
  cancelled: 'Refund request cancelled',
} as const

function WalletAmount({ value }: { value: number }) {
  const { t } = useTranslation()
  const wallet = useWalletCurrency()
  const amount = wallet.quotaToInput(value)
  return (
    <span className='tabular-nums'>
      {Number.isSafeInteger(value) && value >= 0 && amount !== ''
        ? `${amount} ${wallet.label}`
        : t('No data provided')}
    </span>
  )
}

function NativeAmount({
  minor,
  currency,
}: {
  minor: number
  currency: string
}) {
  const { t } = useTranslation()
  return (
    <span className='tabular-nums'>
      {storeRefundNativeAmountText(minor, currency) ??
        t('{{amount}} {{currency}} minor units', { amount: minor, currency })}
    </span>
  )
}

export function StoreRefundPanel({
  orderId,
  audience,
  pickupProof,
  onChanged,
  showOrderIdentity = false,
  initiallyOpen = false,
}: {
  orderId: string
  audience: StoreRefundAudience
  pickupProof?: StorePickupRefundProof
  onChanged?: () => Promise<void> | void
  showOrderIdentity?: boolean
  initiallyOpen?: boolean
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const owner = `${pickupProof ? 'pickup' : user?.id || 'guest'}:${audience}`
  const proofSession = useId()
  const [open, setOpen] = useState(initiallyOpen)
  const [pending, setPending] = useState<StoreRefundInput | undefined>(() =>
    storeRefundPending(owner, orderId)
  )
  const [unknown, setUnknown] = useState(!!pending)
  const [needsRefresh, setNeedsRefresh] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  const inFlight = useRef(false)
  const client = useQueryClient()
  const query = useQuery({
    queryKey: [
      'store',
      'refunds',
      orderId,
      owner,
      pickupProof ? proofSession : null,
    ],
    queryFn: () =>
      pickupProof
        ? storeRefundApi.pickupRead(pickupProof)
        : storeRefundApi.read(orderId),
    enabled: open && (audience !== 'root' || user?.role === 100),
    retry: false,
    gcTime: pickupProof ? 0 : undefined,
  })
  const actualRoot = audience === 'root' && user?.role === 100
  const refundView = query.data
  const canManage = !pickupProof && (audience === 'seller' || actualRoot)
  const canRequest = audience === 'buyer'
  useEffect(() => {
    if (
      pending &&
      query.data?.refunds.some(
        (refund) => refund.request_key === pending.request_key
      )
    ) {
      storeRefundPending(owner, orderId, null)
      setPending(undefined)
      setUnknown(false)
      setError(null)
    }
  }, [orderId, owner, pending, query.data])

  async function refresh() {
    const result = await query.refetch()
    if (!result.error) {
      setNeedsRefresh(false)
      if (!pending) {
        setUnknown(false)
        setError(null)
      }
    }
  }
  async function mutate(
    run: () => Promise<StoreRefund>,
    input?: StoreRefundInput
  ) {
    if (
      inFlight.current ||
      needsRefresh ||
      (unknown && (!input || input.request_key !== pending?.request_key))
    ) {
      return
    }
    inFlight.current = true
    setBusy(true)
    setError(null)
    if (input) {
      storeRefundPending(owner, orderId, input)
      setPending(input)
    }
    try {
      await run()
      if (input) {
        storeRefundPending(owner, orderId, null)
        setPending(undefined)
      }
      setUnknown(false)
      setNeedsRefresh(true)
      const [refreshed] = await Promise.all([
        query.refetch(),
        client.invalidateQueries({ queryKey: ['store', 'orders'] }),
        client.invalidateQueries({ queryKey: ['store', 'order'] }),
        client.invalidateQueries({ queryKey: ['store', 'product'] }),
        client.invalidateQueries({ queryKey: ['store', 'product-preview'] }),
      ])
      if (!refreshed.error) setNeedsRefresh(false)
      await onChanged?.()
    } catch (issue) {
      setError(issue)
      const uncertain =
        issue instanceof StoreRefundRequestError && issue.outcomeUnknown
      setUnknown(uncertain)
      if (!uncertain && input) {
        storeRefundPending(owner, orderId, null)
        setPending(undefined)
      }
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }
  const request = (input: StoreRefundInput) => {
    if (pickupProof) return storeRefundApi.pickupRequest(pickupProof, input)
    return canManage
      ? storeRefundApi.proactive(orderId, input)
      : storeRefundApi.request(orderId, input)
  }
  if (audience === 'root' && !actualRoot) return null
  return (
    <section className='space-y-3' aria-label={t('Refunds')}>
      <Button
        type='button'
        size='sm'
        variant='outline'
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        {t('Refunds')}
      </Button>
      {open && (
        <div className='space-y-4'>
          <StoreError error={query.error || error} />
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <h3 className='text-sm font-semibold'>{t('Refund history')}</h3>
            <Button
              type='button'
              size='sm'
              variant='ghost'
              disabled={busy || query.isFetching}
              onClick={() => void refresh()}
            >
              {t('Check refund status')}
            </Button>
          </div>
          {query.isPending ? (
            <StoreLoading />
          ) : (
            refundView && (
              <>
                {showOrderIdentity && (
                  <div className='space-y-1 text-sm'>
                    <p className='font-medium break-words'>
                      {refundView.product_title}
                    </p>
                    <p className='text-muted-foreground break-words'>
                      {refundView.variant_name || t('Historic/default variant')}
                    </p>
                  </div>
                )}
                <dl className='grid grid-cols-2 gap-x-4 gap-y-2 text-sm'>
                  <dt className='text-muted-foreground'>{t('Order value')}</dt>
                  <dd className='text-right'>
                    <WalletAmount value={refundView.principal_quota} />
                  </dd>
                  <dt className='text-muted-foreground'>
                    {t('Refunded order value')}
                  </dt>
                  <dd className='text-right'>
                    <WalletAmount value={refundView.refunded_quota} />
                  </dd>
                  <dt className='text-muted-foreground'>
                    {t('Available to refund')}
                  </dt>
                  <dd className='text-right'>
                    <WalletAmount value={refundView.remaining_quota} />
                  </dd>
                  {refundView.payment_method !== 'balance' &&
                    refundView.native_basis_verified &&
                    refundView.amount_minor !== undefined && (
                      <>
                        <dt className='text-muted-foreground'>
                          {t('Original payment')}
                        </dt>
                        <dd className='text-right'>
                          <NativeAmount
                            minor={refundView.amount_minor}
                            currency={refundView.currency}
                          />
                        </dd>
                      </>
                    )}
                </dl>
                {!refundView.refunds.length && (
                  <p className='text-muted-foreground text-sm'>
                    {t('No refunds yet')}
                  </p>
                )}
                <div className='space-y-3'>
                  {refundView.refunds.map((refund) => (
                    <StoreRefundHistoryRow
                      key={refund.id}
                      refund={refund}
                      view={refundView}
                      canManage={canManage}
                      canCancel={canRequest && !pickupProof}
                      disabled={busy || unknown || needsRefresh}
                      onDecision={(decision, reason) =>
                        void mutate(() =>
                          storeRefundApi.decision(
                            orderId,
                            refund.id,
                            decision,
                            reason
                          )
                        )
                      }
                      onCancel={() =>
                        void mutate(() =>
                          storeRefundApi.cancel(orderId, refund.id)
                        )
                      }
                    />
                  ))}
                </div>
                {unknown && (
                  <Alert>
                    <AlertDescription className='space-y-2'>
                      <p>
                        {t(
                          'The refund result is not known yet. Check its status before submitting another request.'
                        )}
                      </p>
                      {pending && (
                        <Button
                          type='button'
                          size='sm'
                          variant='outline'
                          disabled={busy || query.isFetching}
                          onClick={() =>
                            void mutate(() => request(pending), pending)
                          }
                        >
                          {t('Retry the same refund request')}
                        </Button>
                      )}
                    </AlertDescription>
                  </Alert>
                )}
                {needsRefresh && (
                  <Alert>
                    <AlertDescription>
                      {t(
                        'Refund recorded. Refresh its status before requesting another refund.'
                      )}
                    </AlertDescription>
                  </Alert>
                )}
                {(canRequest || canManage) &&
                  storeRefundModes(refundView).length > 0 &&
                  !unknown &&
                  !needsRefresh && (
                    <>
                      <Separator />
                      <StoreRefundForm
                        key={`${orderId}-${audience}`}
                        view={refundView}
                        proactive={canManage}
                        disabled={busy}
                        onSubmit={(input) =>
                          void mutate(() => request(input), input)
                        }
                      />
                    </>
                  )}
              </>
            )
          )}
        </div>
      )}
    </section>
  )
}

function StoreRefundForm({
  view,
  proactive,
  disabled,
  onSubmit,
}: {
  view: StoreRefundView
  proactive: boolean
  disabled: boolean
  onSubmit: (input: StoreRefundInput) => void
}) {
  const { t } = useTranslation()
  const wallet = useWalletCurrency()
  const id = useId()
  const [mode, setMode] = useState<StoreRefundMode>('full')
  const [quantity, setQuantity] = useState('1')
  const balance = view.payment_method === 'balance'
  const amountKey = balance
    ? `balance:${wallet.currency}:${wallet.quotaToInput(1)}`
    : `native:${view.currency}`
  const [amountDraft, setAmountDraft] = useState({ key: amountKey, value: '' })
  // A display-currency or conversion change must not reinterpret a typed value.
  const amount = amountDraft.key === amountKey ? amountDraft.value : ''
  useEffect(() => {
    setAmountDraft((previous) =>
      previous.key === amountKey ? previous : { key: amountKey, value: '' }
    )
  }, [amountKey])
  const [reason, setReason] = useState('')
  const [stockIds, setStockIds] = useState<string[]>([])
  const modes = storeRefundModes(view)
  const actualMode = modes.includes(mode) ? mode : 'full'
  const balanceDisplay = balance
    ? { currency: wallet.currency, config: wallet.config }
    : undefined
  const max = storeRefundAmountMax(view)
  const maximum = balance
    ? wallet.quotaToInput(max)
      ? `${wallet.quotaToInput(max)} ${wallet.label}`
      : t('No data provided')
    : (storeRefundNativeAmountText(max, view.currency) ??
      t('{{amount}} {{currency}} minor units', {
        amount: max,
        currency: view.currency,
      }))
  const input = storeRefundInput(
    view,
    { mode: actualMode, quantity, amount, reason, stockIds },
    'validation',
    balanceDisplay
  )
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault()
        if (disabled || !input) return
        const body = storeRefundInput(
          view,
          { mode: actualMode, quantity, amount, reason, stockIds },
          crypto.randomUUID(),
          balanceDisplay
        )
        if (body) onSubmit(body)
      }}
    >
      <FieldGroup className='gap-4'>
        <Field data-disabled={disabled}>
          <FieldLabel id={`${id}-mode`}>{t('Refund type')}</FieldLabel>
          <ToggleGroup
            aria-labelledby={`${id}-mode`}
            variant='outline'
            size='sm'
            spacing={1}
            value={[actualMode]}
            onValueChange={(values) => {
              const next = values[0] as StoreRefundMode | undefined
              if (next && modes.includes(next)) {
                setMode(next)
                setStockIds([])
              }
            }}
          >
            {modes.map((value) => (
              <ToggleGroupItem key={value} value={value} disabled={disabled}>
                {t(modeLabels[value])}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </Field>
        {!balance &&
          view.native_basis_verified &&
          !storeRefundNativeAmountSupported(view.currency) && (
            <p className='text-muted-foreground text-sm'>
              {t('Refund by amount is not supported for this currency.')}
            </p>
          )}
        {actualMode === 'quantity' && (
          <>
            <Field data-disabled={disabled} data-invalid={!input && !!reason}>
              <FieldLabel htmlFor={`${id}-quantity`}>
                {t('Quantity to refund')}
              </FieldLabel>
              <Input
                id={`${id}-quantity`}
                inputMode='numeric'
                value={quantity}
                disabled={disabled}
                onChange={(event) => {
                  setQuantity(event.target.value)
                  setStockIds([])
                }}
              />
              <FieldDescription>
                {t('Available to refund: {{count}} items', {
                  count: storeRefundQuantityMax(view),
                })}
              </FieldDescription>
            </Field>
            <FieldSet>
              <FieldLegend variant='label'>
                {t('Choose specific items')}
              </FieldLegend>
              <FieldDescription>
                {t(
                  'Leave items unselected to use the original delivery order.'
                )}
              </FieldDescription>
              <FieldGroup className='max-h-56 gap-2 overflow-y-auto'>
                {view.eligible_items.map((item) => (
                  <Field
                    key={item.stock_id}
                    orientation='horizontal'
                    data-disabled={disabled}
                  >
                    <Checkbox
                      id={`${id}-${item.stock_id}`}
                      checked={stockIds.includes(item.stock_id)}
                      disabled={disabled}
                      onCheckedChange={(checked) => {
                        const next = checked
                          ? [...stockIds, item.stock_id]
                          : stockIds.filter((value) => value !== item.stock_id)
                        setStockIds(next)
                        if (next.length) setQuantity(String(next.length))
                      }}
                    />
                    <FieldLabel
                      htmlFor={`${id}-${item.stock_id}`}
                      className='font-normal'
                    >
                      {t('Item {{position}}', { position: item.position })}
                    </FieldLabel>
                  </Field>
                ))}
              </FieldGroup>
            </FieldSet>
          </>
        )}
        {actualMode === 'amount' && (
          <Field data-disabled={disabled} data-invalid={!input && !!reason}>
            <FieldLabel htmlFor={`${id}-amount`}>
              {t('Refund amount ({{currency}})', {
                currency: balance ? wallet.label : view.currency,
              })}
            </FieldLabel>
            <Input
              id={`${id}-amount`}
              inputMode={
                balance && wallet.currency === 'CREDIT' ? 'numeric' : 'decimal'
              }
              value={amount}
              disabled={disabled}
              onChange={(event) =>
                setAmountDraft({ key: amountKey, value: event.target.value })
              }
            />
            <FieldDescription>
              {t('Maximum refund: {{amount}}', {
                amount: maximum,
              })}
            </FieldDescription>
          </Field>
        )}
        <Field data-disabled={disabled}>
          <FieldLabel htmlFor={`${id}-reason`}>{t('Refund reason')}</FieldLabel>
          <Textarea
            id={`${id}-reason`}
            value={reason}
            rows={2}
            maxLength={1000}
            required
            disabled={disabled}
            onChange={(event) => setReason(event.target.value)}
          />
        </Field>
        <Button type='submit' disabled={disabled || !input} className='w-fit'>
          {t(proactive ? 'Refund this order' : 'Request refund')}
        </Button>
      </FieldGroup>
    </form>
  )
}

function StoreRefundHistoryRow({
  refund,
  view,
  canManage,
  canCancel,
  disabled,
  onDecision,
  onCancel,
}: {
  refund: StoreRefund
  view: StoreRefundView
  canManage: boolean
  canCancel: boolean
  disabled: boolean
  onDecision: (decision: 'approve' | 'reject', reason: string) => void
  onCancel: () => void
}) {
  const { t, i18n } = useTranslation()
  const id = useId()
  const [reason, setReason] = useState('')
  return (
    <article className='space-y-2 rounded-md border p-3 text-sm'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <span className='font-medium'>
          {t(modeLabels[refund.mode])}
          {refund.mode === 'quantity' &&
            ` · ${t('Quantity')}: ${refund.quantity}`}
        </span>
        <Badge
          variant={refund.status === 'completed' ? 'secondary' : 'outline'}
        >
          {t(statusLabels[refund.status])}
        </Badge>
      </div>
      <p className='text-muted-foreground text-xs'>
        {storeDate(refund.created_at, i18n.language)}
      </p>
      {view.payment_method === 'balance' ? (
        <WalletAmount value={refund.amount_quota} />
      ) : view.native_basis_verified && refund.amount_minor > 0 ? (
        <NativeAmount minor={refund.amount_minor} currency={refund.currency} />
      ) : null}
      <p className='break-words whitespace-pre-wrap'>{refund.reason}</p>
      {refund.decision_reason && (
        <p className='text-muted-foreground break-words whitespace-pre-wrap'>
          {refund.decision_reason}
        </p>
      )}
      {refund.status === 'requested' && canManage && (
        <FieldGroup className='gap-2'>
          <Field data-disabled={disabled}>
            <FieldLabel htmlFor={`${id}-decision`}>
              {t('Decision reason')}
            </FieldLabel>
            <Textarea
              id={`${id}-decision`}
              value={reason}
              rows={2}
              maxLength={1000}
              disabled={disabled}
              onChange={(event) => setReason(event.target.value)}
            />
          </Field>
          <div className='flex flex-wrap gap-2'>
            <Button
              type='button'
              size='sm'
              disabled={disabled}
              onClick={() => onDecision('approve', reason)}
            >
              {t('Approve refund')}
            </Button>
            <Button
              type='button'
              size='sm'
              variant='outline'
              disabled={disabled}
              onClick={() => onDecision('reject', reason)}
            >
              {t('Reject refund')}
            </Button>
          </div>
        </FieldGroup>
      )}
      {refund.status === 'requested' && canCancel && (
        <Button
          type='button'
          size='sm'
          variant='outline'
          disabled={disabled}
          onClick={onCancel}
        >
          {t('Cancel refund request')}
        </Button>
      )}
    </article>
  )
}

export function StorePickupRefunds({
  metadata,
  token,
  onChanged,
}: {
  metadata: StoreClaimMetadata
  token: string
  onChanged: () => Promise<void> | void
}) {
  const { t } = useTranslation()
  const id = useId()
  const user = useAuthStore((state) => state.auth.user)
  const [code, setCode] = useState('')
  const [proof, setProof] = useState<StorePickupRefundProof | undefined>()
  if (metadata.pickup_login_required && !metadata.pickup_login_satisfied) {
    return null
  }
  if (metadata.pickup_login_required && !user) {
    return (
      <div className='space-y-3 rounded-lg border p-4'>
        <p className='text-sm'>
          {t('Sign in with the purchasing account to view or request refunds.')}
        </p>
        <Button
          size='sm'
          variant='outline'
          render={
            <a
              href={`/sign-in?redirect=${encodeURIComponent(window.location.pathname)}`}
            />
          }
        >
          {t('Sign in')}
        </Button>
      </div>
    )
  }
  return (
    <div className='space-y-3'>
      {!proof ? (
        <form
          onSubmit={(event) => {
            event.preventDefault()
            setProof({
              order_id: metadata.order_id,
              token,
              ...(code ? { code } : {}),
            })
            setCode('')
          }}
        >
          <FieldGroup className='gap-3'>
            {metadata.pickup_code_required && (
              <Field>
                <FieldLabel htmlFor={`${id}-code`}>
                  {t('Pickup code')}
                </FieldLabel>
                <Input
                  id={`${id}-code`}
                  type='password'
                  value={code}
                  maxLength={72}
                  autoComplete='off'
                  required
                  onChange={(event) => setCode(event.target.value)}
                />
              </Field>
            )}
            <Button
              type='submit'
              variant='outline'
              size='sm'
              className='w-fit'
              disabled={
                metadata.pickup_code_required &&
                (!code || new TextEncoder().encode(code).length > 72)
              }
            >
              {t('View refunds or request a refund')}
            </Button>
          </FieldGroup>
        </form>
      ) : (
        <>
          <StoreRefundPanel
            orderId={metadata.order_id}
            audience='buyer'
            pickupProof={proof}
            onChanged={onChanged}
            initiallyOpen
          />
          {metadata.pickup_code_required && (
            <Button
              type='button'
              variant='ghost'
              size='sm'
              onClick={() => setProof(undefined)}
            >
              {t('Change pickup code')}
            </Button>
          )}
        </>
      )}
    </div>
  )
}

export function StoreRootRefunds() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const id = useId()
  const [draft, setDraft] = useState('')
  const [orderId, setOrderId] = useState('')
  if (user?.role !== 100) return null
  return (
    <section className='space-y-3'>
      <h2 className='text-sm font-semibold'>{t('Order refunds')}</h2>
      <form
        onSubmit={(event) => {
          event.preventDefault()
          if (/^[a-zA-Z0-9-]{1,64}$/.test(draft)) setOrderId(draft)
        }}
      >
        <FieldGroup className='max-w-md gap-3'>
          <Field>
            <FieldLabel htmlFor={`${id}-order`}>{t('Order ID')}</FieldLabel>
            <Input
              id={`${id}-order`}
              value={draft}
              maxLength={64}
              onChange={(event) => setDraft(event.target.value)}
            />
          </Field>
          <Button
            type='submit'
            size='sm'
            className='w-fit'
            disabled={!/^[a-zA-Z0-9-]{1,64}$/.test(draft)}
          >
            {t('Find order refunds')}
          </Button>
        </FieldGroup>
      </form>
      {orderId && (
        <StoreRefundPanel
          key={orderId}
          orderId={orderId}
          audience='root'
          showOrderIdentity
          initiallyOpen
        />
      )}
    </section>
  )
}
