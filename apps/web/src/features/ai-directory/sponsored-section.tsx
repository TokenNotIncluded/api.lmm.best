/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ArrowUpRight, Megaphone, Plus } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { refreshCurrentAccount } from '@/features/onboarding/use-auth-user-refresh'
import { useDebounce } from '@/hooks/use-debounce'
import { isConsoleActivated } from '@/lib/console-activation'
import { useAuthStore } from '@/stores/auth-store'

import {
  createDirectoryAd,
  directoryAdErrorCode,
  listDirectoryAds,
  listMyDirectoryAds,
  quoteDirectoryAd,
  type DirectoryAd,
} from './ads-api'
import { safeDirectoryUrl } from './api'

const EMPTY_DRAFT = {
  name: '',
  url: 'https://',
  summary: '',
  description: '',
  bid: '1.00',
}

function centsFromInput(value: string): number | null {
  if (!/^(?:0|[1-9]\d{0,4})(?:\.\d{1,2})?$/.test(value)) return null
  const [whole, fraction = ''] = value.split('.')
  const cents = Number(whole) * 100 + Number(fraction.padEnd(2, '0'))
  return cents >= 100 && cents <= 1_000_000 ? cents : null
}

function bidAmount(cents: number) {
  return (cents / 100).toFixed(2)
}

function adErrorMessage(code: string | null): string {
  switch (code) {
    case 'AI_DIRECTORY_AD_INSUFFICIENT_BALANCE':
      return 'Your wallet balance is too low. Add credits and try again.'
    case 'AI_DIRECTORY_AD_QUOTE_CHANGED':
      return 'The credit price changed. Review the refreshed quote before paying.'
    case 'AI_DIRECTORY_AD_REQUEST_CONFLICT':
      return 'This payment request was already used. Review your listings before trying again.'
    case 'AI_DIRECTORY_AD_INVALID_INPUT':
      return 'Check the website details and bid amount.'
    default:
      return 'Unable to place the advertisement. Try again.'
  }
}

function SponsoredAd({ ad }: { ad: DirectoryAd }) {
  const { t } = useTranslation()
  const href = safeDirectoryUrl(ad.url)
  if (!href) return null
  const hostname = new URL(href).hostname.replace(/^www\./, '')
  return (
    <article className='ai-sponsored-item'>
      <div className='min-w-0 flex-1'>
        <span className='ai-sponsored-label'>{t('Sponsored')}</span>
        <h3>{ad.name}</h3>
        <p>{ad.summary}</p>
        {ad.description && (
          <details>
            <summary>{t('Read description')}</summary>
            <p>{ad.description}</p>
          </details>
        )}
        <span className='ai-sponsored-domain'>{hostname}</span>
      </div>
      <div className='ai-sponsored-side'>
        <span>
          {t('Bid: {{amount}} USD equivalent', {
            amount: bidAmount(ad.bid_cents),
          })}
        </span>
        <a
          href={href}
          target='_blank'
          rel='sponsored noopener noreferrer'
          aria-label={t('Open {{name}} in a new tab', { name: ad.name })}
        >
          <ArrowUpRight size={18} aria-hidden='true' />
        </a>
      </div>
    </article>
  )
}

function AdvertisementDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const cache = useQueryClient()
  const [draft, setDraft] = useState(EMPTY_DRAFT)
  const [step, setStep] = useState<'edit' | 'confirm'>('edit')
  const [requestID, setRequestID] = useState(() => crypto.randomUUID())
  const [attempted, setAttempted] = useState(false)
  const bidCents = centsFromInput(draft.bid)
  const debouncedBid = useDebounce(bidCents, 300)
  const quote = useQuery({
    queryKey: ['ai-directory-ad-quote', debouncedBid],
    queryFn: () => {
      if (debouncedBid === null) throw new Error('Invalid bid')
      return quoteDirectoryAd(debouncedBid)
    },
    enabled: open && debouncedBid !== null,
    staleTime: 0,
    retry: false,
  })
  const mine = useQuery({
    queryKey: ['ai-directory-my-ads'],
    queryFn: listMyDirectoryAds,
    enabled: open,
    staleTime: 30_000,
    retry: false,
  })
  const mutation = useMutation({
    mutationFn: createDirectoryAd,
    onSuccess: ({ ad }) => {
      toast.success(
        t('Advertisement published until {{date}}.', {
          date: new Date(ad.expires_at * 1000).toLocaleDateString(),
        })
      )
      onOpenChange(false)
      setStep('edit')
      setDraft(EMPTY_DRAFT)
      setRequestID(crypto.randomUUID())
      setAttempted(false)
      void Promise.allSettled([
        cache.invalidateQueries({ queryKey: ['ai-directory-ads'] }),
        cache.invalidateQueries({ queryKey: ['ai-directory-my-ads'] }),
        refreshCurrentAccount(),
      ])
    },
    onError: (error) => {
      const code = directoryAdErrorCode(error)
      toast.error(t(adErrorMessage(code)))
      if (code === 'AI_DIRECTORY_AD_QUOTE_CHANGED') {
        setStep('edit')
        void quote.refetch()
      }
    },
  })
  const changeOpen = (next: boolean) => {
    if (mutation.isPending) return
    onOpenChange(next)
    if (!next) setStep('edit')
  }
  const update = (patch: Partial<typeof draft>) => {
    setDraft((current) => ({ ...current, ...patch }))
    setRequestID(crypto.randomUUID())
  }
  const validName =
    draft.name.trim().length > 0 && draft.name.trim().length <= 80
  const validURL =
    draft.url.startsWith('https://') && Boolean(safeDirectoryUrl(draft.url))
  const validSummary = draft.summary.length <= 180
  const validDescription = draft.description.length <= 1200
  const validBid = bidCents !== null
  const valid =
    validName && validURL && validSummary && validDescription && validBid
  const review = () => {
    setAttempted(true)
    if (valid && quote.isSuccess && quote.data.bid_cents === bidCents) {
      setStep('confirm')
    }
  }
  const pay = () => {
    if (
      !valid ||
      bidCents === null ||
      !quote.data ||
      quote.data.bid_cents !== bidCents ||
      mutation.isPending
    ) {
      return
    }
    mutation.mutate({
      name: draft.name.trim(),
      url: draft.url.trim(),
      summary: draft.summary.trim(),
      description: draft.description.trim(),
      bid_cents: bidCents,
      expected_quota: quote.data.quota,
      request_id: requestID,
    })
  }

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogContent className='sm:max-w-xl'>
        {step === 'edit' ? (
          <>
            <DialogHeader>
              <DialogTitle>{t('Promote your website')}</DialogTitle>
              <DialogDescription>
                {t(
                  'Your ad appears for 30 days. Higher bids rank first. No automatic renewal.'
                )}
              </DialogDescription>
            </DialogHeader>
            <form
              className='grid gap-4'
              onSubmit={(event) => {
                event.preventDefault()
                review()
              }}
            >
              <div className='grid gap-1.5'>
                <Label htmlFor='sponsor-name'>{t('Website name')}</Label>
                <Input
                  id='sponsor-name'
                  value={draft.name}
                  maxLength={80}
                  onChange={(event) => update({ name: event.target.value })}
                  aria-invalid={attempted && !validName}
                />
              </div>
              <div className='grid gap-1.5'>
                <Label htmlFor='sponsor-url'>{t('Website address')}</Label>
                <Input
                  id='sponsor-url'
                  type='url'
                  value={draft.url}
                  onChange={(event) => update({ url: event.target.value })}
                  aria-invalid={attempted && !validURL}
                />
              </div>
              <div className='grid gap-1.5'>
                <Label htmlFor='sponsor-summary'>{t('Short summary')}</Label>
                <Input
                  id='sponsor-summary'
                  value={draft.summary}
                  maxLength={180}
                  onChange={(event) => update({ summary: event.target.value })}
                />
              </div>
              <div className='grid gap-1.5'>
                <Label htmlFor='sponsor-description'>
                  {t('Expanded description')}
                </Label>
                <Textarea
                  id='sponsor-description'
                  value={draft.description}
                  maxLength={1200}
                  onChange={(event) =>
                    update({ description: event.target.value })
                  }
                  rows={3}
                />
              </div>
              <div className='grid gap-1.5'>
                <Label htmlFor='sponsor-bid'>
                  {t('Bid in USD equivalent')}
                </Label>
                <Input
                  id='sponsor-bid'
                  type='number'
                  min='1'
                  max='10000'
                  step='0.01'
                  inputMode='decimal'
                  value={draft.bid}
                  onChange={(event) => update({ bid: event.target.value })}
                  aria-invalid={attempted && !validBid}
                />
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Bid 1 to 10,000 USD equivalent. Payment uses platform wallet credits.'
                  )}
                </p>
              </div>
              {validBid &&
                quote.isSuccess &&
                quote.data.bid_cents === bidCents && (
                  <p className='ai-sponsored-quote'>
                    {t(
                      'Exact charge: {{quota}} wallet units for {{days}} days.',
                      {
                        quota: quote.data.quota.toLocaleString(),
                        days: quote.data.duration_days,
                      }
                    )}
                  </p>
                )}
              {quote.isError && validBid && (
                <p role='alert' className='text-destructive text-sm'>
                  {t('Unable to get a price quote. Try again.')}
                </p>
              )}
              {attempted && !valid && (
                <p role='alert' className='text-destructive text-sm'>
                  {t('Check the website details and bid amount.')}
                </p>
              )}
              <DialogFooter>
                <Button
                  type='button'
                  variant='outline'
                  onClick={() => changeOpen(false)}
                >
                  {t('Cancel')}
                </Button>
                <Button
                  type='submit'
                  disabled={
                    valid &&
                    (!quote.isSuccess || quote.data?.bid_cents !== bidCents)
                  }
                >
                  {t('Review payment')}
                </Button>
              </DialogFooter>
            </form>
            {mine.data?.items.length ? (
              <div className='border-border border-t pt-4'>
                <p className='mb-2 text-sm font-semibold'>
                  {t('Recent advertisements')}
                </p>
                <div className='grid gap-1 text-sm'>
                  {mine.data.items.slice(0, 5).map((ad) => (
                    <p
                      key={ad.id}
                      className='text-muted-foreground flex justify-between gap-2'
                    >
                      <span className='truncate'>{ad.name}</span>
                      <span>
                        {ad.expires_at > Date.now() / 1000 &&
                        ad.status === 'active'
                          ? t('Active')
                          : t('Ended')}
                        {' · '}
                        {ad.refunded_at > 0
                          ? t('{{quota}} units refunded', {
                              quota: ad.charged_quota.toLocaleString(),
                            })
                          : t('{{quota}} units paid', {
                              quota: ad.charged_quota.toLocaleString(),
                            })}
                      </span>
                    </p>
                  ))}
                </div>
              </div>
            ) : null}
          </>
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>{t('Confirm advertisement payment')}</DialogTitle>
              <DialogDescription>
                {t(
                  'Review the exact wallet charge before publishing your website.'
                )}
              </DialogDescription>
            </DialogHeader>
            <div className='ai-sponsored-confirm'>
              <p>
                <span>{t('Website')}</span>
                <strong>{draft.name.trim()}</strong>
              </p>
              <p>
                <span>{t('Bid')}</span>
                <strong>
                  {bidCents === null
                    ? '—'
                    : t('{{amount}} USD equivalent', {
                        amount: bidAmount(bidCents),
                      })}
                </strong>
              </p>
              <p>
                <span>{t('Wallet charge')}</span>
                <strong>
                  {quote.data?.quota.toLocaleString()} {t('units')}
                </strong>
              </p>
              <p>
                <span>{t('Display period')}</span>
                <strong>
                  {t('{{days}} days', {
                    days: quote.data?.duration_days ?? 30,
                  })}
                </strong>
              </p>
            </div>
            <p className='text-muted-foreground text-sm'>
              {t(
                'The ad starts after payment and ends automatically. It will not renew on its own.'
              )}
            </p>
            <DialogFooter>
              <Button
                type='button'
                variant='outline'
                onClick={() => setStep('edit')}
                disabled={mutation.isPending}
              >
                {t('Back')}
              </Button>
              <Button type='button' onClick={pay} disabled={mutation.isPending}>
                {mutation.isPending ? t('Publishing...') : t('Pay and publish')}
              </Button>
            </DialogFooter>
            <Link
              to='/wallet'
              onClick={() => changeOpen(false)}
              className='text-primary text-center text-xs underline-offset-4 hover:underline'
            >
              {t('Need more credits? Open wallet')}
            </Link>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}

export function SponsoredDirectorySection() {
  const { t } = useTranslation()
  const auth = useAuthStore((state) => state.auth)
  const isSignedIn = Boolean(auth.user && auth.accessToken)
  const canPromote = isSignedIn && isConsoleActivated(auth.user)
  const [open, setOpen] = useState(false)
  const adsQuery = useInfiniteQuery({
    queryKey: ['ai-directory-ads'],
    queryFn: ({ pageParam }) => listDirectoryAds(pageParam),
    initialPageParam: 0,
    getNextPageParam: (lastPage) =>
      lastPage.has_more ? lastPage.next_offset : undefined,
    staleTime: 30_000,
    retry: false,
  })
  const ads = adsQuery.data?.pages.flatMap((page) => page.items) ?? []

  return (
    <section
      className='ai-sponsored-section'
      aria-labelledby='ai-sponsored-heading'
    >
      <div className='ai-sponsored-heading'>
        <div>
          <h2 id='ai-sponsored-heading'>{t('Sponsored websites')}</h2>
          <p>{t('Paid placements are labeled and ranked by bid.')}</p>
        </div>
        {canPromote ? (
          <Button type='button' size='sm' onClick={() => setOpen(true)}>
            <Plus data-icon='inline-start' />
            {t('Promote your website')}
          </Button>
        ) : (
          <Button
            size='sm'
            render={
              isSignedIn ? (
                <Link to='/getting-started' />
              ) : (
                <Link to='/sign-in' search={{ redirect: '/ai-directory' }} />
              )
            }
          >
            <Plus data-icon='inline-start' />
            {t('Promote your website')}
          </Button>
        )}
      </div>
      {adsQuery.isPending && (
        <p className='ai-sponsored-empty'>{t('Loading advertisements...')}</p>
      )}
      {adsQuery.isError && (
        <div className='ai-sponsored-empty'>
          <p>{t('Unable to load advertisements.')}</p>
          <Button variant='link' onClick={() => void adsQuery.refetch()}>
            {t('Try again')}
          </Button>
        </div>
      )}
      {adsQuery.isSuccess && ads.length === 0 && (
        <div className='ai-sponsored-empty'>
          <Megaphone size={22} strokeWidth={1.5} aria-hidden='true' />
          <p>{t('Your website could be featured here.')}</p>
          <span>{t('Placements start at 1 USD equivalent for 30 days.')}</span>
        </div>
      )}
      {ads.length > 0 && (
        <div className='ai-sponsored-list'>
          {ads.map((ad) => (
            <SponsoredAd key={ad.id} ad={ad} />
          ))}
        </div>
      )}
      {adsQuery.hasNextPage && (
        <Button
          variant='outline'
          className='mt-3'
          disabled={adsQuery.isFetchingNextPage}
          onClick={() => void adsQuery.fetchNextPage()}
        >
          {t('Load more advertisements')}
        </Button>
      )}
      {canPromote && <AdvertisementDialog open={open} onOpenChange={setOpen} />}
    </section>
  )
}
