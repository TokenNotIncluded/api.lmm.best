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
import { Delete02Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { Minus, Plus, RefreshCw } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'

import { formatHeroSmsUSD, parseHeroSmsError } from './api.js'
import type {
  HeroSmsSmsCountry,
  HeroSmsSmsOffer,
  HeroSmsSmsService,
} from './sms-api.js'
import { SmsCountryIdentity, SmsServiceIdentity } from './sms-identities.js'
import {
  clampHeroSmsQuantity,
  getHeroSmsCountryName,
  HERO_SMS_MAX_FAVORITES,
  HERO_SMS_MAX_QUANTITY,
  type HeroSmsFavoritePair,
} from './sms-selection.js'

interface SmsFavoritesPageProps {
  favorites: HeroSmsFavoritePair[]
  services: HeroSmsSmsService[]
  countries: HeroSmsSmsCountry[]
  language: string
  onSelect: (favorite: HeroSmsFavoritePair) => void
  onRemove: (favorite: HeroSmsFavoritePair) => void
}

export function SmsFavoritesPage({
  favorites,
  services,
  countries,
  language,
  onSelect,
  onRemove,
}: SmsFavoritesPageProps) {
  const { t } = useTranslation()
  const serviceMap = useMemo(
    () => new Map(services.map((item) => [item.code, item] as const)),
    [services]
  )
  const countryMap = useMemo(
    () => new Map(countries.map((item) => [item.id, item] as const)),
    [countries]
  )
  const resolved = useMemo(
    () =>
      favorites.map((favorite) => ({
        favorite,
        service: serviceMap.get(favorite.serviceCode),
        country: countryMap.get(favorite.countryId),
      })),
    [countryMap, favorites, serviceMap]
  )

  return (
    <div className='space-y-3'>
      <div className='flex items-center justify-between gap-3'>
        <div>
          <p className='text-sm font-medium'>{t('Favorite combinations')}</p>
          <p className='text-muted-foreground text-xs'>
            {t('Select a saved combination to start a new purchase.')}
          </p>
        </div>
        <span className='text-muted-foreground text-xs tabular-nums'>
          {resolved.length}/{HERO_SMS_MAX_FAVORITES}
        </span>
      </div>
      {resolved.length === 0 ? (
        <div className='text-muted-foreground rounded-xl border border-dashed px-4 py-8 text-center text-sm'>
          {t('No favorite combinations yet')}
        </div>
      ) : (
        <div className='grid gap-2'>
          {resolved.map(({ favorite, service, country }) => (
            <div
              key={`${favorite.serviceCode}:${favorite.countryId}`}
              className='flex items-center gap-2 rounded-xl border p-2'
            >
              <Button
                type='button'
                variant='ghost'
                disabled={!service || !country}
                className='h-auto min-w-0 flex-1 justify-start gap-3 px-2 py-1.5'
                onClick={() => onSelect(favorite)}
              >
                {service ? (
                  <SmsServiceIdentity service={service} />
                ) : (
                  <span className='bg-muted text-muted-foreground flex size-8 items-center justify-center rounded-lg border text-[10px] font-semibold'>
                    {favorite.serviceCode.slice(0, 2).toUpperCase()}
                  </span>
                )}
                {country ? (
                  <SmsCountryIdentity country={country} language={language} />
                ) : (
                  <span className='bg-muted text-muted-foreground flex size-8 items-center justify-center rounded-lg border text-[10px] font-semibold tabular-nums'>
                    #{favorite.countryId}
                  </span>
                )}
                <span className='min-w-0 text-left'>
                  <span className='block truncate text-sm font-medium'>
                    {service?.name ?? favorite.serviceCode}
                  </span>
                  <span className='text-muted-foreground block truncate text-xs'>
                    {country
                      ? getHeroSmsCountryName(country, language)
                      : `#${favorite.countryId}`}
                  </span>
                </span>
              </Button>
              <Button
                type='button'
                variant='ghost'
                size='icon'
                aria-label={t('Remove favorite combination')}
                onClick={() => onRemove(favorite)}
              >
                <HugeiconsIcon icon={Delete02Icon} />
              </Button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

interface SmsPriceTierPickerProps {
  offer?: HeroSmsSmsOffer
  selectedTierPrice: string
  bidEnabled: boolean
  bidPrice: string
  onTierChange: (customerPriceUSD: string) => void
  onBidEnabledChange: (enabled: boolean) => void
  onBidPriceChange: (value: string) => void
}

export function SmsPriceTierPicker({
  offer,
  selectedTierPrice,
  bidEnabled,
  bidPrice,
  onTierChange,
  onBidEnabledChange,
  onBidPriceChange,
}: SmsPriceTierPickerProps) {
  const { t } = useTranslation()
  const tiers = useMemo(
    () =>
      [...(offer?.tiers ?? [])].sort(
        (left, right) =>
          Number(left.customer_price_usd) - Number(right.customer_price_usd)
      ),
    [offer?.tiers]
  )
  if (tiers.length === 0) return null
  const selected = selectedTierPrice || tiers[0]?.customer_price_usd || ''
  const value = bidEnabled ? '__custom_bid__' : selected
  return (
    <div className='space-y-2'>
      <div className='flex items-center justify-between gap-3'>
        <p id='hero-sms-price-tier-label' className='text-sm font-medium'>
          {t('Price tiers')}
        </p>
        <span className='text-muted-foreground text-xs'>
          {t('Lowest price first')}
        </span>
      </div>
      <RadioGroup
        value={value}
        aria-labelledby='hero-sms-price-tier-label'
        className='max-h-52 gap-1.5 overflow-y-auto pr-1'
        onValueChange={(next) => {
          if (next === '__custom_bid__') {
            onBidEnabledChange(true)
            return
          }
          onBidEnabledChange(false)
          onTierChange(next)
        }}
      >
        {tiers.map((tier) => {
          const id = `hero-sms-price-${tier.customer_price_usd}`
          return (
            <Label
              key={tier.id}
              htmlFor={id}
              className='hover:bg-muted/50 flex cursor-pointer items-center gap-3 rounded-lg border px-3 py-2'
            >
              <RadioGroupItem id={id} value={tier.customer_price_usd} />
              <span className='min-w-0 flex-1 text-sm font-medium tabular-nums'>
                ≤ {formatHeroSmsUSD(Number(tier.customer_price_usd))}
              </span>
              <span className='text-muted-foreground text-xs tabular-nums'>
                {t('{{count}} available', { count: tier.inventory })}
              </span>
            </Label>
          )
        })}
        <div className='hover:bg-muted/50 flex items-center gap-3 rounded-lg border px-3 py-2'>
          <Label
            htmlFor='hero-sms-custom-bid'
            className='flex min-w-0 flex-1 cursor-pointer items-center gap-3'
          >
            <RadioGroupItem id='hero-sms-custom-bid' value='__custom_bid__' />
            <span className='text-sm font-medium'>
              {t('Custom maximum bid')}
            </span>
          </Label>
          <Input
            aria-label={t('Maximum unit price')}
            type='number'
            inputMode='decimal'
            min='0.000001'
            step='0.000001'
            value={bidPrice}
            className='h-8 w-28 text-right tabular-nums'
            placeholder='0.00'
            onFocus={() => onBidEnabledChange(true)}
            onChange={(event) => onBidPriceChange(event.target.value)}
            onWheel={(event) => event.currentTarget.blur()}
          />
        </div>
      </RadioGroup>
      <p className='text-muted-foreground text-xs leading-relaxed'>
        {t(
          'Availability is the provider quantity available at or below that unit price. A bid may be fulfilled for less, and the difference is refunded.'
        )}
      </p>
    </div>
  )
}

interface SmsPurchaseDetailsProps {
  operator: string
  operators: string[]
  operatorsState: {
    isPending: boolean
    isError: boolean
    onRetry: () => void
  }
  quantity: number
  offer?: HeroSmsSmsOffer
  country: string
  onOperatorChange: (value: string) => void
  onQuantityChange: (value: number) => void
}

export function SmsPurchaseDetails({
  operator,
  operators,
  operatorsState,
  quantity,
  offer,
  country,
  onOperatorChange,
  onQuantityChange,
}: SmsPurchaseDetailsProps) {
  const { t } = useTranslation()
  const maximum = Math.min(
    HERO_SMS_MAX_QUANTITY,
    offer?.inventory ?? HERO_SMS_MAX_QUANTITY
  )
  return (
    <div className='space-y-3'>
      <div className='flex items-center gap-2 text-sm font-medium'>
        <span className='bg-muted text-muted-foreground inline-flex size-5 items-center justify-center rounded-full text-[11px] tabular-nums'>
          3
        </span>
        <span>{t('Purchase details')}</span>
      </div>
      <div className='grid gap-3 sm:grid-cols-2'>
        <div className='space-y-2'>
          <Label htmlFor='hero-sms-operator'>{t('Operator')}</Label>
          <Select
            value={operator || '__any__'}
            disabled={!country || operatorsState.isPending}
            onValueChange={(value) =>
              onOperatorChange(
                value == null || value === '__any__' ? '' : value
              )
            }
          >
            <SelectTrigger id='hero-sms-operator' className='w-full'>
              <SelectValue placeholder={t('Any operator')} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='__any__'>{t('Any operator')}</SelectItem>
              {operators.map((item) => (
                <SelectItem key={item} value={item}>
                  {item}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {operatorsState.isError ? (
            <Button
              type='button'
              variant='link'
              size='sm'
              className='h-auto px-0 text-xs'
              onClick={operatorsState.onRetry}
            >
              {t('Retry loading operators')}
            </Button>
          ) : null}
        </div>
        <div className='space-y-2'>
          <Label htmlFor='hero-sms-quantity'>{t('Quantity')}</Label>
          <div className='grid grid-cols-[auto_minmax(0,1fr)_auto] gap-1'>
            <Button
              type='button'
              variant='outline'
              size='icon'
              aria-label={t('Decrease quantity')}
              disabled={!offer || quantity <= 1}
              onClick={() => onQuantityChange(quantity - 1)}
            >
              <Minus aria-hidden='true' />
            </Button>
            <Input
              id='hero-sms-quantity'
              type='number'
              inputMode='numeric'
              min={1}
              max={maximum}
              value={quantity}
              disabled={!offer}
              className='text-center tabular-nums'
              onChange={(event) =>
                onQuantityChange(
                  clampHeroSmsQuantity(
                    Number(event.target.value),
                    offer?.inventory ?? HERO_SMS_MAX_QUANTITY
                  )
                )
              }
              onWheel={(event) => event.currentTarget.blur()}
            />
            <Button
              type='button'
              variant='outline'
              size='icon'
              aria-label={t('Increase quantity')}
              disabled={!offer || quantity >= maximum}
              onClick={() => onQuantityChange(quantity + 1)}
            >
              <Plus aria-hidden='true' />
            </Button>
          </div>
        </div>
      </div>
    </div>
  )
}

interface SmsQuoteSummaryProps {
  offer?: HeroSmsSmsOffer
  quantity: number
  isFetching: boolean
  isError: boolean
  error: unknown
  onRefresh: () => void
}

export function SmsQuoteSummary({
  offer,
  quantity,
  isFetching,
  isError,
  error,
  onRefresh,
}: SmsQuoteSummaryProps) {
  const { t } = useTranslation()
  if (isFetching) return <Skeleton className='h-24 w-full' />
  if (offer) {
    const unitPrice = Number(offer.customer_price_usd)
    return (
      <div className='bg-muted/35 grid grid-cols-3 gap-3 rounded-lg border p-3 text-sm'>
        <div>
          <p className='text-muted-foreground text-xs'>{t('Inventory')}</p>
          <p className='mt-1 font-medium tabular-nums'>{offer.inventory}</p>
        </div>
        <div>
          <p className='text-muted-foreground text-xs'>
            {t('Maximum unit price')}
          </p>
          <p className='mt-1 font-medium tabular-nums'>
            {formatHeroSmsUSD(unitPrice)}
          </p>
        </div>
        <div>
          <p className='text-muted-foreground text-xs'>{t('Maximum total')}</p>
          <p className='mt-1 font-semibold tabular-nums'>
            {formatHeroSmsUSD(unitPrice * quantity)}
          </p>
        </div>
      </div>
    )
  }
  if (!isError) return null
  return (
    <div className='space-y-2' role='alert'>
      <p className='text-destructive text-sm'>
        {t(parseHeroSmsError(error).message)}
      </p>
      <Button type='button' variant='outline' size='sm' onClick={onRefresh}>
        <RefreshCw data-icon='inline-start' />
        {t('Refresh quote')}
      </Button>
    </div>
  )
}
