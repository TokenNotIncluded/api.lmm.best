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
import { Phone, RefreshCw, Star } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import type {
  HeroSmsSmsCountry,
  HeroSmsSmsOffer,
  HeroSmsSmsService,
} from './sms-api.js'
import {
  SmsCatalogPicker,
  type SmsCatalogOption,
} from './sms-catalog-picker.js'
import { SmsCountryIdentity, SmsServiceIdentity } from './sms-identities.js'
import {
  SmsFavoritesPage,
  SmsPriceTierPicker,
  SmsPurchaseDetails,
  SmsQuoteSummary,
} from './sms-marketplace-sections.js'
import type { HeroSmsBatchPurchaseResult } from './sms-purchase.js'
import {
  getHeroSmsCountryName,
  getHeroSmsCountrySearchText,
  getHeroSmsQuickIndex,
  hasHeroSmsFavorite,
  type HeroSmsFavoritePair,
} from './sms-selection.js'

interface CatalogRequestState {
  isPending: boolean
  isError: boolean
  onRetry: () => void
}

interface SmsPurchaseCardProps {
  language: string
  services: HeroSmsSmsService[]
  countries: HeroSmsSmsCountry[]
  favoriteCountries: HeroSmsSmsCountry[]
  servicesState: CatalogRequestState
  countriesState: CatalogRequestState
  favorites: HeroSmsFavoritePair[]
  service: string
  country: string
  operator: string
  operators: string[]
  operatorsState: CatalogRequestState
  selectedTierPrice: string
  bidEnabled: boolean
  bidPrice: string
  quantity: number
  selectedService?: HeroSmsSmsService
  selectedCountry?: HeroSmsSmsCountry
  selectedIsFavorite: boolean
  offer?: HeroSmsSmsOffer
  catalogOffer?: HeroSmsSmsOffer
  offerIsFetching: boolean
  offerIsError: boolean
  offerError: unknown
  batchProgress: { completed: number; total: number } | null
  batchResult: HeroSmsBatchPurchaseResult | null
  batchFeedback: string
  canPurchase: boolean
  reconciliationPending: boolean
  onServiceChange: (value: string) => void
  onCountryChange: (value: string) => void
  onOperatorChange: (value: string) => void
  onTierChange: (customerPriceUSD: string) => void
  onBidEnabledChange: (enabled: boolean) => void
  onBidPriceChange: (value: string) => void
  onQuantityChange: (value: number) => void
  onSelectFavorite: (favorite: HeroSmsFavoritePair) => void
  onRemoveFavorite: (favorite: HeroSmsFavoritePair) => void
  onToggleFavorite: () => void
  onRefreshOffer: () => void
  onReconcile: () => void
  onPurchase: () => void
}

function SmsStepLabel({ step, children }: { step: number; children: string }) {
  return (
    <span className='flex items-center gap-2'>
      <span
        aria-hidden='true'
        className='bg-muted text-muted-foreground flex size-5 items-center justify-center rounded-full text-[11px] font-semibold tabular-nums'
      >
        {step}
      </span>
      <span>{children}</span>
    </span>
  )
}

function SmsServiceField({
  value,
  options,
  state,
  onChange,
}: {
  value: string
  options: SmsCatalogOption[]
  state: CatalogRequestState
  onChange: (value: string) => void
}) {
  const { t } = useTranslation()
  let control = <Skeleton className='h-12 w-full' />
  if (!state.isPending) {
    control = (
      <SmsCatalogPicker
        id='hero-sms-service'
        value={value}
        options={options}
        placeholder={t('Select a service')}
        searchPlaceholder={t('Search services by name or code...')}
        noResultsText={t('No matching services')}
        allText={t('All')}
        popularText={t('Popular')}
        favoritesText={t('Favorites')}
        retryText={t('Retry')}
        errorText={t('Unable to load phone services')}
        isError={state.isError}
        onRetry={state.onRetry}
        onValueChange={onChange}
      />
    )
  }
  return (
    <div className='space-y-2'>
      <Label htmlFor='hero-sms-service'>
        <SmsStepLabel step={1}>{t('Service')}</SmsStepLabel>
      </Label>
      {control}
    </div>
  )
}

function SmsCountryField({
  value,
  service,
  options,
  state,
  isFavorite,
  selected,
  onChange,
  onToggleFavorite,
}: {
  value: string
  service: string
  options: SmsCatalogOption[]
  state: CatalogRequestState
  isFavorite: boolean
  selected: boolean
  onChange: (value: string) => void
  onToggleFavorite: () => void
}) {
  const { t } = useTranslation()
  let control = <Skeleton className='h-12 w-full' />
  if (!state.isPending) {
    control = (
      <div className='grid grid-cols-[minmax(0,1fr)_auto] gap-2'>
        <SmsCatalogPicker
          id='hero-sms-country'
          value={value}
          options={options}
          placeholder={
            service ? t('Select a country') : t('Select a service first')
          }
          searchPlaceholder={t('Search countries in any language...')}
          noResultsText={t('No matching countries')}
          allText={t('All')}
          popularText={t('Popular')}
          favoritesText={t('Favorites')}
          retryText={t('Retry')}
          errorText={t('Unable to load phone countries')}
          disabled={!service}
          isError={state.isError}
          onRetry={state.onRetry}
          onValueChange={onChange}
        />
        <Button
          type='button'
          variant={isFavorite ? 'secondary' : 'outline'}
          size='icon'
          aria-label={
            isFavorite
              ? t('Remove favorite combination')
              : t('Add favorite combination')
          }
          disabled={!selected}
          onClick={onToggleFavorite}
        >
          <Star
            aria-hidden='true'
            className={isFavorite ? 'fill-current' : undefined}
          />
        </Button>
      </div>
    )
  }
  return (
    <div className='space-y-2'>
      <Label htmlFor='hero-sms-country'>
        <SmsStepLabel step={2}>{t('Country')}</SmsStepLabel>
      </Label>
      {control}
    </div>
  )
}

function SmsBatchStatus({
  progress,
  result,
  feedback,
  reconciliationPending,
  onReconcile,
}: {
  progress: SmsPurchaseCardProps['batchProgress']
  result: SmsPurchaseCardProps['batchResult']
  feedback: string
  reconciliationPending: boolean
  onReconcile: () => void
}) {
  const { t } = useTranslation()
  if (progress) {
    return (
      <div
        className='bg-muted/35 rounded-lg border p-3 text-sm'
        role='status'
        aria-live='polite'
      >
        <p className='font-medium'>{t('Purchasing phone activations...')}</p>
        <p className='text-muted-foreground mt-1 tabular-nums'>
          {t('{{completed}} of {{total}} completed', progress)}
        </p>
      </div>
    )
  }
  if (!result?.failure) return null
  return (
    <div
      className='border-destructive/30 bg-destructive/5 rounded-lg border p-3 text-sm'
      role='alert'
    >
      <p className='font-medium'>
        {result.orders.length > 0
          ? t('Purchase partially completed')
          : t('Purchase not completed')}
      </p>
      <p className='text-muted-foreground mt-1'>{feedback}</p>
      {result.orders.length > 0 ? (
        <p className='mt-2 tabular-nums'>
          {t(
            '{{succeeded}} of {{requested}} phone activations were purchased',
            {
              succeeded: result.orders.length,
              requested: result.requested,
            }
          )}
        </p>
      ) : null}
      {result.failure.ambiguous ? (
        <Button
          type='button'
          variant='outline'
          size='sm'
          className='mt-3'
          disabled={reconciliationPending}
          onClick={onReconcile}
        >
          <RefreshCw data-icon='inline-start' />
          {t('Resolve purchase and continue')}
        </Button>
      ) : null}
    </div>
  )
}

function purchaseButtonText(
  quantity: number,
  t: ReturnType<typeof useTranslation>['t']
) {
  if (quantity === 1) return t('Buy phone activation')
  return t('Buy {{count}} phone activations', { count: quantity })
}

export function SmsPurchaseCard(props: SmsPurchaseCardProps) {
  const { t } = useTranslation()
  const [page, setPage] = useState<'purchase' | 'favorites'>('purchase')
  const serviceOptions = useMemo<SmsCatalogOption[]>(
    () =>
      props.services.map((item) => ({
        value: item.code,
        label: item.name,
        description: item.code,
        searchText: `${item.name} ${item.code}`,
        indexKey: getHeroSmsQuickIndex(item.name),
        popularity: item.popularity,
        favorite: props.favorites.some(
          (favorite) => favorite.serviceCode === item.code
        ),
        leading: <SmsServiceIdentity service={item} />,
      })),
    [props.favorites, props.services]
  )
  const countryOptions = useMemo<SmsCatalogOption[]>(
    () =>
      props.countries.map((item) => {
        const label = getHeroSmsCountryName(item, props.language)
        const description =
          item.english_name && item.english_name !== label
            ? item.english_name
            : `#${item.id}`
        return {
          value: String(item.id),
          label,
          description,
          searchText: getHeroSmsCountrySearchText(item),
          indexKey: getHeroSmsQuickIndex(item.english_name || item.name),
          popularity: item.popularity,
          favorite: props.service
            ? hasHeroSmsFavorite(props.favorites, props.service, item.id)
            : false,
          leading: (
            <SmsCountryIdentity country={item} language={props.language} />
          ),
        }
      }),
    [props.countries, props.favorites, props.language, props.service]
  )

  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2 text-base'>
          <Phone className='size-4' />
          {t('Purchase temporary phone number')}
        </CardTitle>
      </CardHeader>
      <CardContent>
        <Tabs
          value={page}
          onValueChange={(value) =>
            setPage(value === 'favorites' ? 'favorites' : 'purchase')
          }
        >
          <TabsList className='mb-5 grid w-full grid-cols-2'>
            <TabsTrigger value='purchase'>{t('Purchase')}</TabsTrigger>
            <TabsTrigger value='favorites'>
              {t('Favorites')} ({props.favorites.length})
            </TabsTrigger>
          </TabsList>
          <TabsContent value='purchase' className='mt-0 space-y-5'>
            <SmsServiceField
              value={props.service}
              options={serviceOptions}
              state={props.servicesState}
              onChange={props.onServiceChange}
            />
            <SmsCountryField
              value={props.country}
              service={props.service}
              options={countryOptions}
              state={props.countriesState}
              isFavorite={props.selectedIsFavorite}
              selected={Boolean(props.selectedService && props.selectedCountry)}
              onChange={props.onCountryChange}
              onToggleFavorite={props.onToggleFavorite}
            />
            <SmsPurchaseDetails
              operator={props.operator}
              operators={props.operators}
              operatorsState={props.operatorsState}
              quantity={props.quantity}
              offer={props.offer}
              country={props.country}
              onOperatorChange={props.onOperatorChange}
              onQuantityChange={props.onQuantityChange}
            />
            <SmsPriceTierPicker
              offer={props.catalogOffer}
              selectedTierPrice={props.selectedTierPrice}
              bidEnabled={props.bidEnabled}
              bidPrice={props.bidPrice}
              onTierChange={props.onTierChange}
              onBidEnabledChange={props.onBidEnabledChange}
              onBidPriceChange={props.onBidPriceChange}
            />
            <SmsQuoteSummary
              offer={props.offer}
              quantity={props.quantity}
              isFetching={props.offerIsFetching}
              isError={props.offerIsError}
              error={props.offerError}
              onRefresh={props.onRefreshOffer}
            />
            <SmsBatchStatus
              progress={props.batchProgress}
              result={props.batchResult}
              feedback={props.batchFeedback}
              reconciliationPending={props.reconciliationPending}
              onReconcile={props.onReconcile}
            />
            <Button
              type='button'
              className='w-full'
              disabled={!props.canPurchase}
              onClick={props.onPurchase}
            >
              {purchaseButtonText(props.quantity, t)}
            </Button>
            <p className='text-muted-foreground text-xs leading-relaxed'>
              {t(
                'Each number is purchased independently. If the provider stops mid-batch, completed purchases remain available and the rest stop without a hidden retry.'
              )}
            </p>
          </TabsContent>
          <TabsContent value='favorites' className='mt-0'>
            <SmsFavoritesPage
              favorites={props.favorites}
              services={props.services}
              countries={props.favoriteCountries}
              language={props.language}
              onSelect={(favorite) => {
                props.onSelectFavorite(favorite)
                setPage('purchase')
              }}
              onRemove={props.onRemoveFavorite}
            />
          </TabsContent>
        </Tabs>
      </CardContent>
    </Card>
  )
}
