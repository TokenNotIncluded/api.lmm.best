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
import { zodResolver } from '@hookform/resolvers/zod'
import { RefreshCw } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import type { Resolver } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { getCurrencyDisplay } from '@/lib/currency'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { getUsdExchangeRate } from '../api'
import { FormDirtyIndicator } from '../components/form-dirty-indicator'
import { FormNavigationGuard } from '../components/form-navigation-guard'
import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useSettingsForm } from '../hooks/use-settings-form'
import { useUpdateOption } from '../hooks/use-update-option'
import { safeNumberFieldProps } from '../utils/numeric-field'

const createPricingSchema = (t: (key: string) => string) =>
  z.object({
    PublicCreditsPerUSD: z.coerce
      .number()
      .finite(t('Public credits per USD must be a positive whole number.'))
      .int(t('Public credits per USD must be a positive whole number.'))
      .min(1, t('Public credits per USD must be a positive whole number.'))
      .max(
        Number.MAX_SAFE_INTEGER,
        t('Public credits per USD must be a positive whole number.')
      )
      .optional(),
    USDExchangeRate: z.coerce
      .number()
      .finite(t('Payment rate must be finite'))
      .min(0.0001, t('Exchange rate must be greater than 0')),
    DisplayTokenStatEnabled: z.boolean(),
    // Accepted only to read older settings; these fields are never editable or
    // submitted by this form and cannot redefine credit or personal currency.
    QuotaPerUnit: z.number(),
    TopUpPlatformUnitsPerCNY: z.number(),
    DisplayInCurrencyEnabled: z.boolean(),
    general_setting: z.object({
      quota_display_type: z.enum(['USD', 'CNY', 'TOKENS', 'CUSTOM']),
      custom_currency_symbol: z.string().optional(),
      custom_currency_code: z.string().optional(),
      custom_currency_exchange_rate: z.number().optional(),
    }),
  })

type PricingFormValues = z.infer<ReturnType<typeof createPricingSchema>>
type PricingSectionProps = { defaultValues: PricingFormValues }

export function PricingSection({ defaultValues }: PricingSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const currency = useSystemConfigStore((state) => state.config.currency)
  const verifiedCurrency = getCurrencyDisplay(currency).config
  const publicBaseline = useRef(defaultValues.PublicCreditsPerUSD)
  useEffect(() => {
    publicBaseline.current = defaultValues.PublicCreditsPerUSD
  }, [defaultValues.PublicCreditsPerUSD])
  const publicCreditsSupported =
    verifiedCurrency.creditUnitSchemaVersion === 2 &&
    Number.isFinite(verifiedCurrency.creditsPerUsd) &&
    Number(verifiedCurrency.creditsPerUsd) > 0 &&
    Number.isSafeInteger(verifiedCurrency.publicCreditsPerUsd) &&
    Number(verifiedCurrency.publicCreditsPerUsd) > 0 &&
    Number.isSafeInteger(defaultValues.PublicCreditsPerUSD) &&
    Number(defaultValues.PublicCreditsPerUSD) > 0
  const creditAnchor =
    verifiedCurrency.currencyUnit === 'credit' &&
    Number.isFinite(verifiedCurrency.creditsPerUsd) &&
    Number(verifiedCurrency.creditsPerUsd) > 0
      ? verifiedCurrency.creditsPerUsdExact ||
        String(verifiedCurrency.creditsPerUsd)
      : t('Credit conversion unavailable')
  const [isSyncingExchangeRate, setIsSyncingExchangeRate] = useState(false)
  const { form, handleSubmit, handleReset, isDirty, isSubmitting } =
    useSettingsForm<PricingFormValues>({
      resolver: zodResolver(createPricingSchema(t)) as Resolver<
        PricingFormValues,
        unknown,
        PricingFormValues
      >,
      defaultValues,
      onSubmit: async (_data, changedFields) => {
        if ('PublicCreditsPerUSD' in changedFields && !publicCreditsSupported) {
          throw new Error(
            t('Public credit settings are unavailable on this server.')
          )
        }
        for (const [key, value] of Object.entries(changedFields)) {
          if (
            key !== 'USDExchangeRate' &&
            key !== 'DisplayTokenStatEnabled' &&
            key !== 'PublicCreditsPerUSD'
          ) {
            continue
          }
          if (
            value === undefined ||
            value === null ||
            typeof value === 'object'
          ) {
            continue
          }
          await updateOption.mutateAsync({
            key,
            value: String(value),
            ...(key === 'PublicCreditsPerUSD'
              ? {
                  publicCreditUnitBaseline: {
                    publicCreditsPerUsd: Number(publicBaseline.current),
                    ledgerQuotaPerUsd: Number(verifiedCurrency.creditsPerUsd),
                  },
                }
              : {}),
          })
          if (key === 'PublicCreditsPerUSD') {
            publicBaseline.current = Number(value)
          }
        }
      },
    })

  const handleSyncExchangeRate = async () => {
    setIsSyncingExchangeRate(true)
    try {
      const response = await getUsdExchangeRate('CNY')
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Failed to load exchange rate'))
      }
      const rate = Number(response.data.rate)
      if (
        response.data.base_currency !== 'USD' ||
        response.data.quote_currency?.trim().toUpperCase() !== 'CNY' ||
        !Number.isFinite(rate) ||
        rate <= 0
      ) {
        throw new Error(
          t('The exchange-rate provider returned an invalid rate')
        )
      }
      form.setValue('USDExchangeRate', rate, {
        shouldDirty: true,
        shouldValidate: true,
      })
      toast.success(
        t(
          'Latest exchange rate loaded: 1 USD = {{rate}} {{currency}}. Save changes to apply it.',
          { rate: rate.toString(), currency: 'CNY' }
        )
      )
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t('Failed to load exchange rate')
      )
    } finally {
      setIsSyncingExchangeRate(false)
    }
  }

  return (
    <>
      <FormNavigationGuard when={isDirty} />
      <SettingsSection title={t('Pricing & Display')}>
        <Form {...form}>
          <SettingsForm onSubmit={handleSubmit}>
            <SettingsPageFormActions
              onSave={handleSubmit}
              onReset={handleReset}
              isSaving={updateOption.isPending || isSubmitting}
              isResetDisabled={!isDirty}
            />
            <FormDirtyIndicator isDirty={isDirty} />
            <p className='text-muted-foreground text-sm'>
              {t(
                'Display balances in CNY, USD, or credits. Chinese users default to CNY and English users to USD.'
              )}
            </p>
            <FormField
              control={form.control}
              name='PublicCreditsPerUSD'
              render={({ field }) => (
                <FormItem>
                  <FormLabel htmlFor='public-credits-per-usd'>
                    {t('Public credits per USD')}
                  </FormLabel>
                  <FormControl>
                    <Input
                      id='public-credits-per-usd'
                      type='number'
                      min={1}
                      max={Number.MAX_SAFE_INTEGER}
                      step={1}
                      disabled
                      {...safeNumberFieldProps(field)}
                    />
                  </FormControl>
                  <FormDescription>
                    {publicCreditsSupported
                      ? t(
                          'Used for stored balances and billing. This value is fixed.'
                        )
                      : t(
                          'Public credit settings are unavailable on this server.'
                        )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormItem>
              <FormLabel htmlFor='credits-per-usd'>
                {t('Internal ledger units per USD')}
              </FormLabel>
              <Input id='credits-per-usd' value={creditAnchor} readOnly />
              <FormDescription>
                {t(
                  'Used for stored balances and billing. This value is fixed.'
                )}
              </FormDescription>
            </FormItem>
            <FormField
              control={form.control}
              name='USDExchangeRate'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('CNY per USD')}</FormLabel>
                  <div className='flex items-center gap-2'>
                    <FormControl>
                      <Input
                        type='number'
                        step='any'
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <Button
                      type='button'
                      variant='outline'
                      size='sm'
                      className='shrink-0'
                      onClick={() => void handleSyncExchangeRate()}
                      disabled={isSyncingExchangeRate}
                      aria-label={t('Sync USD exchange rate')}
                      aria-busy={isSyncingExchangeRate}
                    >
                      <RefreshCw
                        className={
                          isSyncingExchangeRate ? 'animate-spin' : undefined
                        }
                        aria-hidden='true'
                      />
                      <span>
                        {isSyncingExchangeRate ? t('Syncing...') : t('Sync')}
                      </span>
                    </Button>
                  </div>
                  <FormDescription>
                    {t(
                      'Used for CNY display and payments. Model prices are stored in real USD.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='DisplayTokenStatEnabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Display Token Statistics')}</FormLabel>
                    <FormDescription>
                      {t('Show token usage statistics in the UI')}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
          </SettingsForm>
        </Form>
      </SettingsSection>
    </>
  )
}
