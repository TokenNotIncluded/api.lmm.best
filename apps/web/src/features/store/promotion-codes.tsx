/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from '@/components/ui/dialog'
import { Empty, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Separator } from '@/components/ui/separator'
import { useAuthStore } from '@/stores/auth-store'

import { storePromotionApi } from './promotion-api'
import type {
  StorePromotionAction,
  StorePromotionCode,
  StorePromotionCodeInput,
} from './promotion-types'
import { promotionDiscountBps, promotionShareUrl } from './promotion-utils'
import { CopyStoreValue, StoreError, StoreLoading } from './shared'
import type { StoreProduct } from './types'

function localDateTime(timestamp: number | null) {
  if (!timestamp) return ''
  const date = new Date(timestamp * 1000)
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, 16)
}

export function StorePromotionCodes({
  product,
  onClose,
}: {
  product: StoreProduct
  onClose: () => void
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const client = useQueryClient()
  const id = useId()
  const [page, setPage] = useState(1)
  const [selected, setSelected] = useState<string[]>([])
  const [editing, setEditing] = useState<StorePromotionCode | null>(null)
  const [discount, setDiscount] = useState('10')
  const [expires, setExpires] = useState('')
  const [maximum, setMaximum] = useState('')
  const [scope, setScope] = useState<'all' | 'selected'>('all')
  const [variantIds, setVariantIds] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [notice, setNotice] = useState('')
  const [confirm, setConfirm] = useState<{
    ids: string[]
    action: 'revoke' | 'delete'
  } | null>(null)
  const owned = product.seller_id === user?.id
  const queryKey = ['store', 'promotion-codes', user?.id, product.id]
  const list = useQuery({
    queryKey: [...queryKey, page],
    queryFn: () => storePromotionApi.list(product.id, page),
    enabled: owned,
    retry: false,
  })
  const items = list.data?.items || []
  const currentSelection = selected.filter((selectedId) =>
    items.some((item) => item.id === selectedId)
  )
  const discountBps = promotionDiscountBps(discount)
  const maxUses = maximum === '' ? null : Number(maximum)
  const expiresAt =
    expires === '' ? null : Math.floor(new Date(expires).getTime() / 1000)
  const validDraft =
    discountBps !== undefined &&
    (maxUses === null ||
      (/^[1-9]\d*$/.test(maximum) && Number.isSafeInteger(maxUses))) &&
    (expiresAt === null ||
      (Number.isSafeInteger(expiresAt) && expiresAt > Date.now() / 1000)) &&
    (scope === 'all' || variantIds.length > 0)

  function reset() {
    setEditing(null)
    setDiscount('10')
    setExpires('')
    setMaximum('')
    setScope('all')
    setVariantIds([])
  }
  function edit(promotion: StorePromotionCode) {
    setEditing(promotion)
    setDiscount(String(promotion.discount_bps / 100))
    setExpires(localDateTime(promotion.expires_at))
    setMaximum(promotion.max_uses === null ? '' : String(promotion.max_uses))
    setScope(promotion.variant_ids.length ? 'selected' : 'all')
    setVariantIds(promotion.variant_ids)
    setError(null)
    document.getElementById(`${id}-discount`)?.focus()
  }
  async function perform(task: () => Promise<unknown>, firstPage = false) {
    if (busy || !owned) return
    setBusy(true)
    setError(null)
    setNotice('')
    try {
      await task()
      setSelected([])
      if (firstPage) setPage(1)
      await client.invalidateQueries({ queryKey })
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(false)
    }
  }
  function batch(ids: string[], action: StorePromotionAction) {
    if (!ids.length) return
    if (action === 'delete' || action === 'revoke') {
      setError(null)
      setConfirm({ ids, action })
      return
    }
    void perform(async () => {
      await storePromotionApi.batch(product.id, ids, action)
      reset()
    })
  }
  function save() {
    if (!validDraft || discountBps === undefined) return
    const input: StorePromotionCodeInput = {
      discount_bps: discountBps,
      expires_at: expiresAt,
      max_uses: maxUses,
      variant_ids: scope === 'all' ? [] : variantIds,
      status:
        items.find((item) => item.id === editing?.id)?.status ||
        editing?.status ||
        'active',
    }
    void perform(async () => {
      if (editing) await storePromotionApi.update(product.id, editing.id, input)
      else await storePromotionApi.create(product.id, input)
      reset()
    }, true)
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose()
      }}
    >
      <DialogContent className='max-h-[90dvh] overflow-y-auto sm:max-w-3xl'>
        <DialogTitle>{t('Promotion codes')}</DialogTitle>
        <DialogDescription>{product.title}</DialogDescription>
        <StoreError
          error={
            error ||
            list.error ||
            (!owned ? new Error('Product not found') : null)
          }
          retry={() => void list.refetch()}
        />
        {owned && (
          <>
            <form
              className='space-y-4'
              onSubmit={(event) => {
                event.preventDefault()
                save()
              }}
            >
              <h2 className='text-sm font-semibold'>
                {t(editing ? 'Edit promotion code' : 'Create promotion code')}
              </h2>
              <FieldGroup className='grid gap-4 sm:grid-cols-3'>
                <Field
                  data-disabled={busy}
                  data-invalid={discountBps === undefined}
                >
                  <FieldLabel htmlFor={`${id}-discount`}>
                    {t('Discount (%)')}
                  </FieldLabel>
                  <Input
                    id={`${id}-discount`}
                    inputMode='decimal'
                    value={discount}
                    disabled={busy}
                    required
                    aria-invalid={discountBps === undefined}
                    onChange={(event) => setDiscount(event.target.value)}
                  />
                  <FieldDescription className='text-xs'>
                    {t('100% discount makes the order free.')}
                  </FieldDescription>
                </Field>
                <Field data-disabled={busy}>
                  <FieldLabel htmlFor={`${id}-expires`}>
                    {t('Expires at')}
                  </FieldLabel>
                  <Input
                    id={`${id}-expires`}
                    type='datetime-local'
                    value={expires}
                    disabled={busy}
                    onChange={(event) => setExpires(event.target.value)}
                  />
                  <FieldDescription className='text-xs'>
                    {t('Leave empty for no expiry.')}
                  </FieldDescription>
                </Field>
                <Field data-disabled={busy}>
                  <FieldLabel htmlFor={`${id}-maximum`}>
                    {t('Maximum uses')}
                  </FieldLabel>
                  <Input
                    id={`${id}-maximum`}
                    inputMode='numeric'
                    value={maximum}
                    disabled={busy}
                    onChange={(event) => setMaximum(event.target.value)}
                  />
                  <FieldDescription className='text-xs'>
                    {t('Leave empty for unlimited uses.')}
                  </FieldDescription>
                </Field>
              </FieldGroup>
              <Field data-disabled={busy}>
                <FieldLabel htmlFor={`${id}-scope`}>
                  {t('Applies to')}
                </FieldLabel>
                <NativeSelect
                  id={`${id}-scope`}
                  className='w-full'
                  value={scope}
                  disabled={busy}
                  onChange={(event) =>
                    setScope(event.target.value as 'all' | 'selected')
                  }
                >
                  <NativeSelectOption value='all'>
                    {t('All product variants')}
                  </NativeSelectOption>
                  <NativeSelectOption
                    value='selected'
                    disabled={!product.variants?.length}
                  >
                    {t('Selected variants')}
                  </NativeSelectOption>
                </NativeSelect>
                {scope === 'selected' && (
                  <div className='grid gap-2 sm:grid-cols-2'>
                    {product.variants?.map((variant) => (
                      <label
                        key={variant.id}
                        className='flex min-h-11 items-center gap-2 text-sm'
                      >
                        <Checkbox
                          disabled={busy}
                          checked={variantIds.includes(variant.id)}
                          onCheckedChange={(checked) =>
                            setVariantIds((previous) =>
                              checked
                                ? [...previous, variant.id]
                                : previous.filter(
                                    (variantId) => variantId !== variant.id
                                  )
                            )
                          }
                        />
                        {variant.name || t('Default variant')}
                      </label>
                    ))}
                  </div>
                )}
              </Field>
              <div className='flex justify-end gap-2'>
                {editing && (
                  <Button
                    type='button'
                    variant='outline'
                    disabled={busy}
                    onClick={reset}
                  >
                    {t('Cancel')}
                  </Button>
                )}
                <Button type='submit' disabled={busy || !validDraft}>
                  {t(
                    busy
                      ? 'Saving...'
                      : editing
                        ? 'Save'
                        : 'Create promotion code'
                  )}
                </Button>
              </div>
            </form>
            <Separator />
            <div className='flex flex-wrap items-center gap-2'>
              <Button
                size='sm'
                variant='outline'
                disabled={busy || !items.length}
                onClick={() => setSelected(items.map((item) => item.id))}
              >
                {t('Select all on this page')}
              </Button>
              <Button
                size='sm'
                variant='outline'
                disabled={busy || !items.length}
                onClick={() =>
                  setSelected(
                    items
                      .filter((item) => !currentSelection.includes(item.id))
                      .map((item) => item.id)
                  )
                }
              >
                {t('Invert selection')}
              </Button>
              <Button
                size='sm'
                variant='ghost'
                disabled={busy || !currentSelection.length}
                onClick={() => setSelected([])}
              >
                {t('Clear selection')}
              </Button>
              <Button
                size='sm'
                variant='outline'
                disabled={busy}
                onClick={() =>
                  void perform(async () => {
                    const result = await storePromotionApi.cleanup(product.id)
                    setNotice(
                      t('Cleaned up {{count}} promotion codes.', {
                        count: result.deleted,
                      })
                    )
                    reset()
                  }, true)
                }
              >
                {t('Clean up expired and exhausted codes')}
              </Button>
            </div>
            {currentSelection.length > 0 && (
              <div
                aria-label={t('Bulk actions')}
                className='flex flex-wrap gap-2'
              >
                {(['pause', 'resume', 'revoke', 'delete'] as const).map(
                  (action) => (
                    <Button
                      key={action}
                      size='sm'
                      variant={action === 'delete' ? 'destructive' : 'outline'}
                      disabled={busy}
                      onClick={() => batch(currentSelection, action)}
                    >
                      {t(
                        {
                          pause: 'Pause selected',
                          resume: 'Resume selected',
                          revoke: 'Revoke selected',
                          delete: 'Delete selected',
                        }[action]
                      )}{' '}
                      · {currentSelection.length}
                    </Button>
                  )
                )}
              </div>
            )}
            {notice && (
              <p role='status' className='text-muted-foreground text-xs'>
                {notice}
              </p>
            )}
            {list.isPending ? (
              <StoreLoading />
            ) : !items.length ? (
              <Empty className='py-6'>
                <EmptyHeader>
                  <EmptyTitle>{t('No promotion codes yet')}</EmptyTitle>
                </EmptyHeader>
              </Empty>
            ) : (
              <div className='divide-y'>
                {items.map((item) => (
                  <article key={item.id} className='space-y-3 py-4'>
                    <div className='flex flex-wrap items-start justify-between gap-3'>
                      <label className='flex min-h-11 min-w-0 items-start gap-2 text-sm'>
                        <Checkbox
                          aria-label={`${t('Select promotion code')}: ${item.code}`}
                          disabled={busy}
                          checked={currentSelection.includes(item.id)}
                          onCheckedChange={(checked) =>
                            setSelected((previous) =>
                              checked
                                ? [...previous, item.id]
                                : previous.filter(
                                    (selectedId) => selectedId !== item.id
                                  )
                            )
                          }
                        />
                        <span className='min-w-0 space-y-1'>
                          <span className='block font-mono break-all'>
                            {item.code}
                          </span>
                          <span className='text-muted-foreground block text-xs'>
                            {item.discount_bps / 100}% ·{' '}
                            {item.discount_bps === 10000
                              ? t('Free claim')
                              : t('Discount')}
                          </span>
                        </span>
                      </label>
                      <Badge variant='secondary'>{t(item.status)}</Badge>
                    </div>
                    <div className='text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs'>
                      <span>
                        {t('Uses: {{used}} / {{max}}', {
                          used: item.uses_count,
                          max: item.max_uses ?? t('Unlimited'),
                        })}
                      </span>
                      <span>
                        {t('Reserved: {{count}}', {
                          count: item.reserved_count,
                        })}
                      </span>
                      <span>
                        {item.expires_at
                          ? t('Expires: {{time}}', {
                              time: new Date(
                                item.expires_at * 1000
                              ).toLocaleString(),
                            })
                          : t('No expiry')}
                      </span>
                      <span>
                        {item.variant_ids.length
                          ? item.variant_ids
                              .map(
                                (variantId) =>
                                  product.variants?.find(
                                    (variant) => variant.id === variantId
                                  )?.name || variantId
                              )
                              .join(' · ')
                          : t('All product variants')}
                      </span>
                    </div>
                    <div className='flex flex-wrap gap-2'>
                      <CopyStoreValue
                        value={promotionShareUrl(
                          product.id,
                          item.code,
                          product.test_mode === true
                        )}
                        label='Copy share link'
                        disabled={busy || item.status !== 'active'}
                      />
                      <Button
                        size='sm'
                        variant='outline'
                        disabled={busy || item.status === 'revoked'}
                        onClick={() => edit(item)}
                      >
                        {t('Edit')}
                      </Button>
                      {item.status !== 'revoked' && (
                        <Button
                          size='sm'
                          variant='outline'
                          disabled={busy}
                          onClick={() =>
                            batch(
                              [item.id],
                              item.status === 'active' ? 'pause' : 'resume'
                            )
                          }
                        >
                          {t(item.status === 'active' ? 'Pause' : 'Resume')}
                        </Button>
                      )}
                      {item.status !== 'revoked' && (
                        <Button
                          size='sm'
                          variant='outline'
                          disabled={busy}
                          onClick={() => batch([item.id], 'revoke')}
                        >
                          {t('Revoke')}
                        </Button>
                      )}
                      <Button
                        size='sm'
                        variant='destructive'
                        disabled={busy}
                        onClick={() => batch([item.id], 'delete')}
                      >
                        {t('Delete')}
                      </Button>
                    </div>
                  </article>
                ))}
              </div>
            )}
            <div className='flex justify-end gap-2'>
              <Button
                size='sm'
                variant='outline'
                disabled={busy || page === 1}
                onClick={() => {
                  setPage((previous) => previous - 1)
                  setSelected([])
                }}
              >
                {t('Previous page')}
              </Button>
              <Button
                size='sm'
                variant='outline'
                disabled={busy || !list.data?.has_more}
                onClick={() => {
                  setPage((previous) => previous + 1)
                  setSelected([])
                }}
              >
                {t('Next page')}
              </Button>
            </div>
            <AlertDialog
              open={!!confirm}
              onOpenChange={(open) => {
                if (!open && !busy) setConfirm(null)
              }}
            >
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>
                    {t(
                      confirm?.action === 'delete'
                        ? 'Delete promotion codes?'
                        : 'Revoke promotion codes?'
                    )}
                  </AlertDialogTitle>
                  <AlertDialogDescription>
                    {product.title} ·{' '}
                    {t('{{count}} codes selected.', {
                      count: confirm?.ids.length || 0,
                    })}
                  </AlertDialogDescription>
                </AlertDialogHeader>
                <StoreError error={error} />
                <AlertDialogFooter>
                  <AlertDialogCancel disabled={busy}>
                    {t('Cancel')}
                  </AlertDialogCancel>
                  <AlertDialogAction
                    variant='destructive'
                    disabled={busy}
                    onClick={() => {
                      if (!confirm) return
                      const { ids, action } = confirm
                      void perform(async () => {
                        await storePromotionApi.batch(product.id, ids, action)
                        setConfirm(null)
                        reset()
                      }, action === 'delete')
                    }}
                  >
                    {t(
                      busy
                        ? 'Saving...'
                        : confirm?.action === 'delete'
                          ? 'Delete'
                          : 'Revoke'
                    )}
                  </AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}
