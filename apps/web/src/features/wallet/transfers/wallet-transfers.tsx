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
/*
Copyright (C) 2026 LIghtJUNction
*/
import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
} from '@tanstack/react-query'
import { ArrowUpRight, RefreshCw } from 'lucide-react'
import { QRCodeSVG } from 'qrcode.react'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
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
  useSystemConfigStore,
  DEFAULT_CURRENCY_CONFIG,
} from '@/stores/system-config-store'

import {
  formatTransferQuota,
  cancelTransfer,
  createTransfer,
  listTransfers,
  transferLink,
  transferQuota,
  type WalletTransfer,
} from './api'

export function TransferShare({ transfer }: { transfer: WalletTransfer }) {
  const { t } = useTranslation()
  const link = transferLink(transfer.token)
  const qr = useRef<HTMLDivElement>(null)
  const [copying, setCopying] = useState(false)
  const image = async (copy: boolean) => {
    const svg = qr.current?.querySelector('svg')
    if (!svg || copying) return
    setCopying(true)
    const source = URL.createObjectURL(
      new Blob([new XMLSerializer().serializeToString(svg)], {
        type: 'image/svg+xml',
      })
    )
    try {
      const img = new Image()
      img.src = source
      await img.decode()
      const canvas = document.createElement('canvas')
      canvas.width = canvas.height = 600
      const context = canvas.getContext('2d')
      if (!context) {
        throw new Error(t('Unable to copy QR code. Download it instead.'))
      }
      context.fillStyle = '#ffffff'
      context.fillRect(0, 0, 600, 600)
      context.drawImage(img, 30, 30, 540, 540)
      const blob = await new Promise<Blob>((resolve, reject) =>
        canvas.toBlob(
          (value) =>
            value
              ? resolve(value)
              : reject(
                  new Error(t('Unable to copy QR code. Download it instead.'))
                ),
          'image/png'
        )
      )
      if (copy) {
        await navigator.clipboard.write([
          new ClipboardItem({ 'image/png': blob }),
        ])
        toast.success(t('Copied!'))
      } else {
        const url = URL.createObjectURL(blob)
        const anchor = document.createElement('a')
        anchor.href = url
        anchor.download = `wallet-transfer-${transfer.id}.png`
        anchor.click()
        setTimeout(() => URL.revokeObjectURL(url), 1000)
      }
    } catch {
      toast.error(t('Unable to copy QR code. Download it instead.'))
    } finally {
      URL.revokeObjectURL(source)
      setCopying(false)
    }
  }
  return (
    <div className='grid justify-items-center gap-3 rounded-lg border p-4'>
      <p className='text-sm font-medium'>
        {formatTransferQuota(transfer.quota)} ·{' '}
        {transfer.status === 'claimed'
          ? t('Claimed')
          : transfer.status === 'cancelled'
            ? t('Cancelled')
            : t('Awaiting claim')}
      </p>
      <p className='text-destructive text-sm font-medium'>
        {t('Do not share this link publicly.')}
      </p>
      <p className='text-muted-foreground text-center text-sm'>
        {t('Anyone with this link can claim the transfer after signing in.')}
      </p>
      <div ref={qr} className='rounded-lg bg-white p-3'>
        <QRCodeSVG value={link} size={180} title={t('Transfer QR code')} />
      </div>
      <p className='max-w-full text-xs break-all'>{link}</p>
      <CopyButton value={link} size='default' variant='outline'>
        {t('Copy link')}
      </CopyButton>
      <div className='flex flex-wrap justify-center gap-2'>
        <Button
          variant='outline'
          disabled={copying}
          onClick={() => void image(true)}
        >
          {t('Copy QR code')}
        </Button>
        <Button
          variant='ghost'
          disabled={copying}
          onClick={() => void image(false)}
        >
          {t('Download QR code')}
        </Button>
      </div>
    </div>
  )
}

export function WalletTransfers({
  userID,
  balance,
  onBalanceChange,
}: {
  userID: number
  balance: number
  onBalanceChange: () => Promise<void>
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [amount, setAmount] = useState('')
  const [requestKey, setRequestKey] = useState(() => crypto.randomUUID())
  const [share, setShare] = useState<WalletTransfer | null>(null)
  const configured = useSystemConfigStore(
    (state) => state.config.currency.quotaPerUnit
  )
  const quotaPerUnit =
    configured > 0 ? configured : DEFAULT_CURRENCY_CONFIG.quotaPerUnit
  const quota = transferQuota(amount, quotaPerUnit)
  const key = ['wallet-transfers', userID]
  const history = useInfiniteQuery({
    queryKey: key,
    enabled: open,
    initialPageParam: 0,
    queryFn: ({ pageParam }) => listTransfers(pageParam),
    getNextPageParam: (last) =>
      last.length === 50 ? last.at(-1)?.id : undefined,
    refetchInterval: open ? 10000 : false,
  })
  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: key })
    await onBalanceChange()
  }
  const error = (err: Error) => toast.error(t(err.message))
  const create = useMutation({
    mutationFn: () => {
      if (!quota || quota > balance) {
        throw new Error(t('Invalid transfer amount or request'))
      }
      return createTransfer(quota, requestKey)
    },
    onSuccess: async (transfer) => {
      setShare(transfer)
      setAmount('')
      setRequestKey(crypto.randomUUID())
      await refresh()
    },
    onError: error,
  })
  const cancel = useMutation({
    mutationFn: cancelTransfer,
    onSuccess: async (_, id) => {
      if (share?.id === id) setShare(null)
      await refresh()
    },
    onError: error,
  })
  const date = (value: number) =>
    value ? new Date(value * 1000).toLocaleString() : '—'
  const status = (value: WalletTransfer['status']) =>
    value === 'claimed'
      ? t('Claimed')
      : value === 'cancelled'
        ? t('Cancelled')
        : t('Awaiting claim')
  return (
    <>
      <div className='flex justify-end'>
        <Button variant='outline' onClick={() => setOpen(true)}>
          <ArrowUpRight />
          {t('Transfer')}
        </Button>
      </div>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className='max-h-[90dvh] overflow-y-auto sm:max-w-2xl'>
          <DialogHeader>
            <DialogTitle>{t('Wallet transfer')}</DialogTitle>
            <DialogDescription>
              {t(
                'Create a private link to transfer part of your wallet balance.'
              )}
            </DialogDescription>
          </DialogHeader>
          <p className='text-sm'>
            {t('Current Balance')}: {formatTransferQuota(balance)}
          </p>
          <form
            onSubmit={(event) => {
              event.preventDefault()
              if (quota && quota <= balance && !create.isPending) {
                create.mutate()
              }
            }}
          >
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor='transfer-amount'>
                  {t('Transfer amount (platform credits)')}
                </FieldLabel>
                <Input
                  id='transfer-amount'
                  inputMode='decimal'
                  value={amount}
                  disabled={create.isPending}
                  onChange={(event) => {
                    setAmount(event.target.value)
                    setRequestKey(crypto.randomUUID())
                  }}
                  aria-invalid={amount !== '' && (!quota || quota > balance)}
                />
                <p className='text-muted-foreground text-sm'>
                  {t(
                    'The amount is deducted immediately. You can cancel before it is claimed.'
                  )}
                </p>
              </Field>
              <Button
                type='submit'
                disabled={!quota || quota > balance || create.isPending}
              >
                {create.isPending
                  ? t('Creating...')
                  : t('Create transfer link')}
              </Button>
            </FieldGroup>
          </form>
          {share && (
            <TransferShare
              transfer={
                history.data?.pages
                  .flat()
                  .find((item) => item.id === share.id) ?? share
              }
            />
          )}
          <div className='flex items-center justify-between gap-2'>
            <h3 className='font-medium'>{t('Transfer history')}</h3>
            <Button
              variant='ghost'
              size='sm'
              disabled={history.isFetching}
              onClick={() => void history.refetch()}
            >
              <RefreshCw />
              {t('Refresh')}
            </Button>
          </div>
          {history.isPending && <p role='status'>{t('Loading...')}</p>}
          {history.isError && (
            <p role='alert' className='text-destructive'>
              {t('Failed to load transfer history.')}
            </p>
          )}
          {history.data?.pages.flat().length === 0 && (
            <p className='text-muted-foreground text-sm'>
              {t('No transfers yet.')}
            </p>
          )}
          <div className='grid gap-3'>
            {history.data?.pages.flat().map((transfer) => (
              <article
                key={transfer.id}
                className='grid gap-2 rounded-lg border p-3 text-sm'
              >
                <div className='flex justify-between gap-3'>
                  <strong>{formatTransferQuota(transfer.quota)}</strong>
                  <span>{status(transfer.status)}</span>
                </div>
                <p>
                  {t('Created at')}: {date(transfer.created_at)}
                </p>
                {transfer.status === 'claimed' && (
                  <>
                    <p>
                      {t('Claimed at')}: {date(transfer.claimed_at)}
                    </p>
                    <p className='break-all'>
                      {t('Recipient')}: #{transfer.recipient_id} ·{' '}
                      {transfer.recipient_name || transfer.recipient_username} ·{' '}
                      {transfer.recipient_username} ·{' '}
                      {transfer.recipient_email || '—'}
                    </p>
                  </>
                )}
                {transfer.status === 'cancelled' && (
                  <p>
                    {t('Cancelled at')}: {date(transfer.cancelled_at)}
                  </p>
                )}

                <div className='flex flex-wrap gap-2'>
                  <Button
                    variant='outline'
                    size='sm'
                    onClick={() => setShare(transfer)}
                  >
                    {t('Link and QR code')}
                  </Button>
                  {transfer.status === 'pending' && (
                    <Button
                      variant='ghost'
                      size='sm'
                      disabled={cancel.isPending}
                      onClick={() => {
                        if (
                          window.confirm(
                            t(
                              'Cancel this transfer and refund the amount to your wallet?'
                            )
                          )
                        ) {
                          cancel.mutate(transfer.id)
                        }
                      }}
                    >
                      {t('Cancel transfer')}
                    </Button>
                  )}
                </div>
              </article>
            ))}
          </div>
          {history.hasNextPage && (
            <Button
              variant='outline'
              disabled={history.isFetchingNextPage}
              onClick={() => void history.fetchNextPage()}
            >
              {t('Load more')}
            </Button>
          )}
        </DialogContent>
      </Dialog>
    </>
  )
}
