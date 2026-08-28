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
import { useState } from 'react'
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
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { DEFAULT_CURRENCY_CONFIG } from '@/stores/system-config-store'

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

const ISO_CURRENCY_CODE_PATTERN = /^[A-Z]{3}$/

type ExchangeRateField =
  | 'USDExchangeRate'
  | 'general_setting.custom_currency_exchange_rate'

type ExchangeRateTarget = {
  currency: string
  field: ExchangeRateField
}

function normalizeCurrencyCode(value: string | undefined): string | null {
  const code = value?.trim().toUpperCase() ?? ''
  return ISO_CURRENCY_CODE_PATTERN.test(code) ? code : null
}

function resolveExchangeRateTarget(
  displayType: PricingFormValues['general_setting']['quota_display_type'],
  customCurrencyCode: string | undefined
): ExchangeRateTarget | null {
  if (displayType === 'USD' || displayType === 'CNY') {
    return { currency: displayType, field: 'USDExchangeRate' }
  }

  if (displayType !== 'CUSTOM') return null

  const currency = normalizeCurrencyCode(customCurrencyCode)
  if (!currency) return null

  return {
    currency,
    field: 'general_setting.custom_currency_exchange_rate',
  }
}

type ExchangeRateSyncButtonProps = {
  currency: string | null
  isPending: boolean
  onSync: () => void
}

function ExchangeRateSyncButton({
  currency,
  isPending,
  onSync,
}: ExchangeRateSyncButtonProps) {
  const { t } = useTranslation()
  const label = isPending ? t('Syncing...') : t('Sync')

  return (
    <Button
      type='button'
      variant='outline'
      size='sm'
      className='shrink-0'
      onClick={onSync}
      disabled={isPending || !currency}
      aria-label={t('Sync USD exchange rate')}
      aria-busy={isPending}
    >
      <RefreshCw
        className={isPending ? 'animate-spin' : undefined}
        aria-hidden='true'
      />
      <span>{label}</span>
    </Button>
  )
}

const createPricingSchema = (t: (key: string) => string) =>
  z
    .object({
      QuotaPerUnit: z.coerce.number().min(0, t('Value must be at least 0')),
      USDExchangeRate: z.coerce
        .number()
        .min(0.0001, t('Exchange rate must be greater than 0')),
      DisplayInCurrencyEnabled: z.boolean(),
      DisplayTokenStatEnabled: z.boolean(),
      general_setting: z.object({
        quota_display_type: z.enum(['USD', 'CNY', 'TOKENS', 'CUSTOM']),
        custom_currency_symbol: z.string().max(8).optional(),
        custom_currency_code: z.string().max(3).optional(),
        custom_currency_exchange_rate: z.coerce
          .number()
          .min(0.0001, t('Exchange rate must be greater than 0'))
          .optional(),
      }),
    })
    .superRefine((data, ctx) => {
      const displayType = data.general_setting.quota_display_type

      if (displayType === 'CUSTOM') {
        if (!data.general_setting.custom_currency_symbol?.trim()) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: ['general_setting', 'custom_currency_symbol'],
            message: t('Custom currency symbol is required'),
          })
        }

        if (!normalizeCurrencyCode(data.general_setting.custom_currency_code)) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: ['general_setting', 'custom_currency_code'],
            message: t('Enter a three-letter ISO 4217 currency code'),
          })
        }

        if (data.general_setting.custom_currency_exchange_rate == null) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: ['general_setting', 'custom_currency_exchange_rate'],
            message: t('Exchange rate is required'),
          })
        }
      }
    })

type PricingFormValues = z.infer<ReturnType<typeof createPricingSchema>>

type PricingSectionProps = {
  defaultValues: PricingFormValues
}

export function PricingSection({ defaultValues }: PricingSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const [isSyncingExchangeRate, setIsSyncingExchangeRate] = useState(false)

  const pricingSchema = createPricingSchema(t)

  const { form, handleSubmit, handleReset, isDirty, isSubmitting } =
    useSettingsForm<PricingFormValues>({
      resolver: zodResolver(pricingSchema) as Resolver<
        PricingFormValues,
        unknown,
        PricingFormValues
      >,
      defaultValues,
      onSubmit: async (_data, changedFields) => {
        for (const [key, value] of Object.entries(changedFields)) {
          if (value === undefined || value === null) continue
          if (typeof value === 'object') continue

          let serialized: string | boolean = value as string | boolean

          if (typeof value === 'boolean') {
            serialized = String(value)
          } else if (typeof value === 'number') {
            serialized = Number.isFinite(value) ? String(value) : '0'
          }

          await updateOption.mutateAsync({
            key,
            value: serialized,
          })
        }
      },
    })

  const displayType = form.watch('general_setting.quota_display_type') ?? 'USD'
  const customCurrencyCode = form.watch('general_setting.custom_currency_code')
  const exchangeRateTarget = resolveExchangeRateTarget(
    displayType,
    customCurrencyCode
  )

  const handleSyncExchangeRate = async () => {
    if (!exchangeRateTarget) return

    setIsSyncingExchangeRate(true)
    try {
      const response = await getUsdExchangeRate(exchangeRateTarget.currency)
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Failed to load exchange rate'))
      }

      const quoteCurrency = normalizeCurrencyCode(response.data.quote_currency)
      const receivedRate = Number(response.data.rate)
      const responseIsInvalid =
        response.data.base_currency !== 'USD' ||
        quoteCurrency !== exchangeRateTarget.currency ||
        !Number.isFinite(receivedRate) ||
        receivedRate <= 0
      if (responseIsInvalid) {
        throw new Error(
          t('The exchange-rate provider returned an invalid rate')
        )
      }

      const rate = exchangeRateTarget.currency === 'USD' ? 1 : receivedRate
      form.setValue(exchangeRateTarget.field, rate, {
        shouldDirty: true,
        shouldValidate: true,
      })
      toast.success(
        t(
          'Latest exchange rate loaded: 1 USD = {{rate}} {{currency}}. Save changes to apply it.',
          {
            rate: rate.toString(),
            currency: exchangeRateTarget.currency,
          }
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
  const displayInCurrencyEnabled = form.watch('DisplayInCurrencyEnabled')
  const showTokensOnlyOption = displayType === 'TOKENS'
  const showQuotaPerUnit =
    displayType === 'TOKENS' ||
    defaultValues.QuotaPerUnit !== DEFAULT_CURRENCY_CONFIG.quotaPerUnit
  const showDisplayInCurrencyOption = displayInCurrencyEnabled === false

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
            {showQuotaPerUnit && (
              <FormField
                control={form.control}
                name='QuotaPerUnit'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Quota Per Unit')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        step='0.01'
                        value={field.value as number}
                        disabled
                        name={field.name}
                        onBlur={field.onBlur}
                        ref={field.ref}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('Number of tokens per unit quota')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            )}

            <FormField
              control={form.control}
              name='general_setting.quota_display_type'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Display Mode')}</FormLabel>
                  <Select
                    items={[
                      { value: 'USD', label: t('USD') },
                      { value: 'CNY', label: t('CNY') },
                      { value: 'CUSTOM', label: t('Custom Currency') },
                      { value: 'TOKENS', label: t('Tokens Only') },
                    ]}
                    value={field.value}
                    onValueChange={field.onChange}
                  >
                    <FormControl>
                      <SelectTrigger>
                        <SelectValue placeholder={t('Select display mode')} />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent alignItemWithTrigger={false}>
                      <SelectGroup>
                        <SelectItem value='USD'>{t('USD')}</SelectItem>
                        <SelectItem value='CNY'>{t('CNY')}</SelectItem>
                        <SelectItem value='CUSTOM'>
                          {t('Custom Currency')}
                        </SelectItem>
                        {showTokensOnlyOption && (
                          <SelectItem value='TOKENS'>
                            {t('Tokens Only')}
                          </SelectItem>
                        )}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                  <FormDescription>
                    {t('Choose how quota values are shown to users')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            {displayType !== 'TOKENS' && displayType !== 'CUSTOM' && (
              <FormField
                control={form.control}
                name='USDExchangeRate'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      {displayType === 'CNY'
                        ? t('CNY per USD')
                        : t('USD Exchange Rate')}
                    </FormLabel>
                    <div className='flex items-center gap-2'>
                      <FormControl>
                        <Input
                          type='number'
                          step='0.01'
                          {...safeNumberFieldProps(field)}
                        />
                      </FormControl>
                      <ExchangeRateSyncButton
                        currency={exchangeRateTarget?.currency ?? null}
                        isPending={isSyncingExchangeRate}
                        onSync={handleSyncExchangeRate}
                      />
                    </div>
                    <FormDescription>
                      {t(
                        'Real exchange rate between USD and your payment gateway currency'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            )}

            {displayType === 'CUSTOM' && (
              <div className='grid gap-4 sm:grid-cols-3'>
                <FormField
                  control={form.control}
                  name='general_setting.custom_currency_symbol'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Custom Currency Symbol')}</FormLabel>
                      <FormControl>
                        <Input
                          type='text'
                          value={field.value ?? ''}
                          onChange={field.onChange}
                          name={field.name}
                          onBlur={field.onBlur}
                          ref={field.ref}
                          maxLength={8}
                          placeholder={t('e.g. ¥ or HK$')}
                        />
                      </FormControl>
                      <FormDescription>
                        {t('Prefix used when displaying prices')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='general_setting.custom_currency_code'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Custom Currency Code')}</FormLabel>
                      <FormControl>
                        <Input
                          type='text'
                          value={field.value ?? ''}
                          onChange={(event) => {
                            const code = event.target.value
                              .replaceAll(/[^A-Za-z]/g, '')
                              .slice(0, 3)
                              .toUpperCase()
                            field.onChange(code)
                          }}
                          name={field.name}
                          onBlur={field.onBlur}
                          ref={field.ref}
                          maxLength={3}
                          autoCapitalize='characters'
                          autoComplete='off'
                          spellCheck={false}
                          placeholder='CNY'
                        />
                      </FormControl>
                      <FormDescription>
                        {t('ISO 4217 code used for live exchange-rate sync')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='general_setting.custom_currency_exchange_rate'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Units per USD')}</FormLabel>
                      <div className='flex items-center gap-2'>
                        <FormControl>
                          <Input
                            type='number'
                            step='0.01'
                            value={field.value ?? ''}
                            onChange={(e) =>
                              field.onChange(
                                e.target.value === ''
                                  ? undefined
                                  : e.target.valueAsNumber
                              )
                            }
                            name={field.name}
                            onBlur={field.onBlur}
                            ref={field.ref}
                            placeholder={t('e.g. 8 means 1 USD = 8 units')}
                          />
                        </FormControl>
                        <ExchangeRateSyncButton
                          currency={exchangeRateTarget?.currency ?? null}
                          isPending={isSyncingExchangeRate}
                          onSync={handleSyncExchangeRate}
                        />
                      </div>
                      <FormDescription>
                        {t('Conversion rate from USD to your custom currency')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>
            )}

            {showDisplayInCurrencyOption && (
              <FormField
                control={form.control}
                name='DisplayInCurrencyEnabled'
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>{t('Display in Currency')}</FormLabel>
                      <FormDescription>
                        {displayType === 'TOKENS'
                          ? t(
                              'Tokens-only mode will show raw quota values regardless of this toggle.'
                            )
                          : t('Show prices in currency instead of quota.')}
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
            )}

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
