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
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import * as z from 'zod'

import { Dialog } from '@/components/dialog'
import { ReactIconByName } from '@/components/react-icon-by-name'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
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
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from '@/components/ui/input-group'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { usesDedicatedPaymentPricing } from '@/lib/payment-pricing'

import { getPaymentMethodAudienceRoleOptions } from './payment-method-audience'

const SETTLEMENT_UNIT_PATTERN = /^[A-Za-z0-9._-]{1,16}$/
const POSITIVE_DECIMAL_PATTERN = /^[0-9]+(?:\.[0-9]+)?$/
const NON_NEGATIVE_INTEGER_PATTERN = /^(?:0|[1-9][0-9]*)$/
const NON_NEGATIVE_DECIMAL_PATTERN = /^[0-9]+(?:\.[0-9]+)?$/

const createPaymentMethodDialogSchema = (t: (key: string) => string) =>
  z
    .object({
      name: z.string().min(1, t('Payment method name is required')),
      type: z.string().min(1, t('Payment type key is required')),
      icon: z.string().optional(),
      enabled: z.boolean(),
      description: z
        .string()
        .max(240, t('Description must be 240 characters or fewer'))
        .optional(),
      color: z
        .string()
        .optional()
        .refine(
          (value) => !value?.trim() || /^#[0-9a-fA-F]{6}$/.test(value.trim()),
          { message: t('Color must be a six-digit hex value') }
        ),
      min_topup: z
        .string()
        .optional()
        .refine(
          (value) =>
            !value?.trim() ||
            (NON_NEGATIVE_DECIMAL_PATTERN.test(value.trim()) &&
              Number.isFinite(Number(value.trim()))),
          {
            message: t('Minimum top-up must be a non-negative decimal number'),
          }
        ),
      max_topup: z
        .string()
        .optional()
        .refine(
          (value) => {
            if (!value?.trim()) return true
            const trimmed = value.trim()
            return POSITIVE_DECIMAL_PATTERN.test(trimmed) && Number(trimmed) > 0
          },
          {
            message: t('Maximum top-up must be a positive decimal number'),
          }
        ),
      unlock_after_days: z
        .string()
        .optional()
        .refine(
          (value) =>
            !value?.trim() ||
            (NON_NEGATIVE_INTEGER_PATTERN.test(value.trim()) &&
              Number.isSafeInteger(Number(value.trim()))),
          { message: t('Unlock delay must be a non-negative whole number') }
        ),
      audience_mode: z.enum(['legacy', 'all', 'include', 'exclude']),
      audience_match: z.enum(['any', 'all']),
      audience_email_contains: z.string().optional(),
      audience_oauth_provider: z.string().optional(),
      audience_user_group: z.string().optional(),
      audience_role: z.enum(['none', 'common', 'admin', 'root']),
      audience_linuxdo_score_min: z
        .string()
        .optional()
        .refine(
          (value) =>
            !value?.trim() ||
            (NON_NEGATIVE_DECIMAL_PATTERN.test(value.trim()) &&
              Number.isFinite(Number(value.trim()))),
          { message: t('LinuxDO score must be a non-negative number') }
        ),
      audience_linuxdo_score_max: z
        .string()
        .optional()
        .refine(
          (value) =>
            !value?.trim() ||
            (NON_NEGATIVE_DECIMAL_PATTERN.test(value.trim()) &&
              Number.isFinite(Number(value.trim()))),
          { message: t('LinuxDO score must be a non-negative number') }
        ),
      topup_ratio: z
        .string()
        .optional()
        .refine(
          (value) => {
            if (!value?.trim()) return true
            const trimmed = value.trim()
            return POSITIVE_DECIMAL_PATTERN.test(trimmed) && Number(trimmed) > 0
          },
          {
            message: t('Payment multiplier must be a positive decimal number'),
          }
        ),
      settlement_currency: z
        .string()
        .optional()
        .refine(
          (value) => !value?.trim() || /^[A-Za-z]{3}$/.test(value.trim()),
          {
            message: t('Settlement currency must be a three-letter ISO code'),
          }
        ),
      settlement_units_per_usd: z
        .string()
        .optional()
        .refine(
          (value) => {
            if (!value?.trim()) return true
            const trimmed = value.trim()
            return POSITIVE_DECIMAL_PATTERN.test(trimmed) && Number(trimmed) > 0
          },
          {
            message: t('USD settlement rate must be a positive decimal number'),
          }
        ),
      platform_units_per_usd: z.string().optional(),
      settlement_units_per_platform_unit: z
        .string()
        .optional()
        .refine(
          (value) => {
            if (!value?.trim()) return true
            const trimmed = value.trim()
            return POSITIVE_DECIMAL_PATTERN.test(trimmed) && Number(trimmed) > 0
          },
          { message: t('Legacy direct rate must be a positive decimal number') }
        ),
      settlement_unit: z
        .string()
        .optional()
        .refine(
          (value) =>
            !value?.trim() || SETTLEMENT_UNIT_PATTERN.test(value.trim()),
          {
            message: t(
              'Settlement unit must use only letters, numbers, dots, hyphens, or underscores (max 16 characters)'
            ),
          }
        ),
      unit_price: z
        .string()
        .optional()
        .refine(
          (value) => {
            if (!value?.trim()) return true
            const trimmed = value.trim()
            return POSITIVE_DECIMAL_PATTERN.test(trimmed) && Number(trimmed) > 0
          },
          { message: t('Gateway price must be a positive decimal number') }
        ),
    })
    .superRefine((values, ctx) => {
      const hasSettlementCurrency = !!values.settlement_currency?.trim()
      const hasSettlementRate = !!values.settlement_units_per_usd?.trim()
      const hasSettlementUnit = !!values.settlement_unit?.trim()
      const directRate =
        values.settlement_units_per_platform_unit?.trim() ||
        values.unit_price?.trim()
      const hasDirectRate = !!directRate

      if (hasSettlementCurrency && !hasSettlementRate) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          message: t('Set the amount charged for each real USD'),
          path: ['settlement_units_per_usd'],
        })
      }
      if (hasSettlementRate && !hasSettlementCurrency) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          message: t('Set the ISO settlement currency for this USD rate'),
          path: ['settlement_currency'],
        })
      }
      if (hasSettlementUnit && !hasDirectRate) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          message: t('Set a valid gateway price when a settlement unit is set'),
          path: ['unit_price'],
        })
      }
      if (hasDirectRate && !hasSettlementUnit) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          message: t('Set a settlement unit when a gateway price is set'),
          path: ['settlement_unit'],
        })
      }
      if (
        values.settlement_units_per_platform_unit?.trim() &&
        values.unit_price?.trim() &&
        values.settlement_units_per_platform_unit.trim() !==
          values.unit_price.trim()
      ) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          message: t('Legacy direct-rate fields must match'),
          path: ['unit_price'],
        })
      }

      const hasAudienceCondition =
        !!values.audience_email_contains?.trim() ||
        (!!values.audience_oauth_provider?.trim() &&
          values.audience_oauth_provider !== 'none') ||
        !!values.audience_linuxdo_score_min?.trim() ||
        !!values.audience_linuxdo_score_max?.trim() ||
        !!values.audience_user_group?.trim() ||
        (!!values.audience_role?.trim() && values.audience_role !== 'none')
      if (
        (values.audience_mode === 'include' ||
          values.audience_mode === 'exclude') &&
        !hasAudienceCondition
      ) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          message: t('Add at least one audience condition'),
          path: ['audience_mode'],
        })
      }

      const scoreMin = values.audience_linuxdo_score_min?.trim()
      const scoreMax = values.audience_linuxdo_score_max?.trim()
      if (scoreMin && scoreMax && Number(scoreMin) > Number(scoreMax)) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          message: t('Minimum LinuxDO score cannot exceed maximum score'),
          path: ['audience_linuxdo_score_max'],
        })
      }

      const minTopUp = values.min_topup?.trim()
      const maxTopUp = values.max_topup?.trim()
      if (
        minTopUp &&
        maxTopUp &&
        NON_NEGATIVE_DECIMAL_PATTERN.test(minTopUp) &&
        Number(minTopUp) > Number(maxTopUp)
      ) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          message: t('Minimum top-up cannot exceed maximum top-up'),
          path: ['max_topup'],
        })
      }
    })

type PaymentMethodDialogFormValues = z.infer<
  ReturnType<typeof createPaymentMethodDialogSchema>
>

const PAYMENT_METHOD_FORM_ID = 'payment-method-form'

export type PaymentMethodData = {
  name: string
  type: string
  icon?: string
  enabled?: string
  description?: string
  color?: string
  min_topup?: string
  max_topup?: string
  unlock_after_days?: string
  audience_mode?: 'legacy' | 'all' | 'include' | 'exclude'
  audience_match?: 'any' | 'all'
  audience_email_contains?: string
  audience_oauth_provider?: string
  audience_user_group?: string
  audience_role?: 'none' | 'common' | 'admin' | 'root'
  audience_linuxdo_score_min?: string
  audience_linuxdo_score_max?: string
  topup_ratio?: string
  settlement_currency?: string
  settlement_units_per_usd?: string
  platform_units_per_usd?: string
  settlement_units_per_platform_unit?: string
  /** @deprecated Direct-rate compatibility fields. */
  settlement_unit?: string
  /** @deprecated Direct-rate compatibility fields. */
  unit_price?: string
}

type PaymentMethodDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSave: (data: PaymentMethodData) => void
  editData?: PaymentMethodData | null
}

const PAYMENT_TYPE_ICON_NAMES: Record<string, string> = {
  alipay: 'SiAlipay',
  epay: 'SiLinux',
  stripe: 'SiStripe',
  wxpay: 'SiWechat',
}

const getDefaultIconName = (type: string) => PAYMENT_TYPE_ICON_NAMES[type] ?? ''

export function PaymentMethodDialog({
  open,
  onOpenChange,
  onSave,
  editData,
}: PaymentMethodDialogProps) {
  const { t } = useTranslation()
  const isEditMode = !!editData
  const paymentMethodDialogSchema = createPaymentMethodDialogSchema(t)
  const paymentTypeOptions = [
    {
      iconName: 'SiAlipay',
      label: `${t('Alipay')} (Epay: alipay)`,
      name: t('Alipay'),
      value: 'alipay',
    },
    {
      iconName: 'SiWechat',
      label: `${t('WeChat Pay')} (Epay: wxpay)`,
      name: t('WeChat Pay'),
      value: 'wxpay',
    },
    {
      iconName: 'SiStripe',
      label: `${t('Stripe')} (stripe)`,
      name: t('Stripe'),
      value: 'stripe',
    },
    {
      iconName: '',
      label: 'Creem (creem)',
      name: 'Creem',
      value: 'creem',
    },
    {
      iconName: '',
      label: 'Waffo (waffo)',
      name: 'Waffo',
      value: 'waffo',
    },
    {
      iconName: 'SiLinux',
      label: 'LINUX DO Credit (Epay: epay)',
      name: 'LINUX DO Credit',
      value: 'epay',
    },
    {
      iconName: '',
      label: 'Waffo Pancake (waffo_pancake)',
      name: 'Waffo Pancake',
      value: 'waffo_pancake',
    },
  ]
  const getPaymentTypeOption = (value: string) =>
    paymentTypeOptions.find((option) => option.value === value)

  const form = useForm<PaymentMethodDialogFormValues>({
    resolver: zodResolver(paymentMethodDialogSchema),
    defaultValues: {
      name: '',
      type: '',
      icon: '',
      enabled: true,
      description: '',
      color: '',
      min_topup: '',
      max_topup: '',
      unlock_after_days: '',
      audience_mode: 'legacy',
      audience_match: 'any',
      audience_email_contains: '',
      audience_oauth_provider: 'none',
      audience_user_group: '',
      audience_role: 'none',
      audience_linuxdo_score_min: '',
      audience_linuxdo_score_max: '',
      topup_ratio: '',
      settlement_currency: '',
      settlement_units_per_usd: '',
      platform_units_per_usd: '',
      settlement_units_per_platform_unit: '',
      settlement_unit: '',
      unit_price: '',
    },
  })

  const iconValue = form.watch('icon')
  const selectedType = form.watch('type')
  const settlementCurrencyValue = form.watch('settlement_currency')?.trim()
  const settlementRateValue = form.watch('settlement_units_per_usd')?.trim()
  const legacySettlementUnit = form.watch('settlement_unit')?.trim()
  const legacyDirectRate =
    form.watch('settlement_units_per_platform_unit')?.trim() ||
    form.watch('unit_price')?.trim()
  const usesDedicatedPricing = usesDedicatedPaymentPricing(selectedType)
  const audienceMode = form.watch('audience_mode')

  useEffect(() => {
    if (editData) {
      form.reset({
        name: editData.name,
        type: editData.type,
        icon: editData.icon ?? getDefaultIconName(editData.type),
        enabled: editData.enabled !== 'false',
        description: editData.description ?? '',
        color: editData.color ?? '',
        min_topup: editData.min_topup ?? '',
        max_topup: editData.max_topup ?? '',
        unlock_after_days: editData.unlock_after_days ?? '',
        audience_mode: editData.audience_mode ?? 'legacy',
        audience_match: editData.audience_match ?? 'any',
        audience_email_contains: editData.audience_email_contains ?? '',
        audience_oauth_provider: editData.audience_oauth_provider ?? 'none',
        audience_user_group: editData.audience_user_group ?? '',
        audience_role: editData.audience_role ?? 'none',
        audience_linuxdo_score_min: editData.audience_linuxdo_score_min ?? '',
        audience_linuxdo_score_max: editData.audience_linuxdo_score_max ?? '',
        topup_ratio: editData.topup_ratio ?? '',
        settlement_currency: editData.settlement_currency ?? '',
        settlement_units_per_usd: editData.settlement_units_per_usd ?? '',
        platform_units_per_usd: editData.platform_units_per_usd ?? '',
        settlement_units_per_platform_unit:
          editData.settlement_units_per_platform_unit ?? '',
        settlement_unit: editData.settlement_unit ?? '',
        unit_price: editData.unit_price ?? '',
      })
    } else {
      form.reset({
        name: '',
        type: '',
        icon: '',
        enabled: true,
        description: '',
        color: '',
        min_topup: '',
        max_topup: '',
        unlock_after_days: '',
        audience_mode: 'legacy',
        audience_match: 'any',
        audience_email_contains: '',
        audience_oauth_provider: 'none',
        audience_user_group: '',
        audience_role: 'none',
        audience_linuxdo_score_min: '',
        audience_linuxdo_score_max: '',
        topup_ratio: '',
        settlement_currency: '',
        settlement_units_per_usd: '',
        platform_units_per_usd: '',
        settlement_units_per_platform_unit: '',
        settlement_unit: '',
        unit_price: '',
      })
    }
  }, [editData, form, open])

  const handleSubmit = (values: PaymentMethodDialogFormValues) => {
    const data: PaymentMethodData = {
      name: values.name,
      type: values.type,
    }
    if (values.icon && values.icon.trim() !== '') {
      data.icon = values.icon.trim()
    }
    if (!values.enabled) data.enabled = 'false'
    if (values.description?.trim()) data.description = values.description.trim()
    if (values.color?.trim()) data.color = values.color.trim()
    if (values.min_topup && values.min_topup.trim() !== '') {
      data.min_topup = values.min_topup
    }
    if (values.max_topup?.trim()) {
      data.max_topup = values.max_topup.trim()
    }
    if (
      values.unlock_after_days?.trim() &&
      values.unlock_after_days.trim() !== '0'
    ) {
      data.unlock_after_days = values.unlock_after_days.trim()
    }
    if (values.audience_mode !== 'legacy') {
      data.audience_mode = values.audience_mode
      if (
        values.audience_mode === 'include' ||
        values.audience_mode === 'exclude'
      ) {
        data.audience_match = values.audience_match
        if (values.audience_email_contains?.trim()) {
          data.audience_email_contains = values.audience_email_contains.trim()
        }
        if (
          values.audience_oauth_provider?.trim() &&
          values.audience_oauth_provider !== 'none'
        ) {
          data.audience_oauth_provider = values.audience_oauth_provider.trim()
        }
        if (values.audience_linuxdo_score_min?.trim()) {
          data.audience_linuxdo_score_min =
            values.audience_linuxdo_score_min.trim()
        }
        if (values.audience_linuxdo_score_max?.trim()) {
          data.audience_linuxdo_score_max =
            values.audience_linuxdo_score_max.trim()
        }
        if (values.audience_user_group?.trim()) {
          data.audience_user_group = values.audience_user_group.trim()
        }
        if (values.audience_role?.trim() && values.audience_role !== 'none') {
          data.audience_role = values.audience_role
        }
      }
    }
    if (
      !usesDedicatedPaymentPricing(values.type) &&
      values.topup_ratio &&
      values.topup_ratio.trim() !== ''
    ) {
      data.topup_ratio = values.topup_ratio.trim()
    }
    if (!usesDedicatedPaymentPricing(values.type)) {
      const settlementCurrency = values.settlement_currency?.trim()
      const settlementRate = values.settlement_units_per_usd?.trim()
      if (settlementCurrency && settlementRate) {
        data.settlement_currency = settlementCurrency.toUpperCase()
        data.settlement_units_per_usd = settlementRate
        if (values.platform_units_per_usd?.trim()) {
          data.platform_units_per_usd = values.platform_units_per_usd.trim()
        }
      } else {
        // Preserve an existing direct-rate configuration until the operator
        // explicitly migrates it to the real-USD bridge above.
        if (values.settlement_unit?.trim()) {
          data.settlement_unit = values.settlement_unit.trim()
        }
        if (values.settlement_units_per_platform_unit?.trim()) {
          data.settlement_units_per_platform_unit =
            values.settlement_units_per_platform_unit.trim()
        }
        if (values.unit_price?.trim()) {
          data.unit_price = values.unit_price.trim()
        }
      }
    }
    onSave(data)
    form.reset()
    onOpenChange(false)
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={isEditMode ? t('Edit payment method') : t('Add payment method')}
      description={t('Configure a payment method for user recharge options.')}
      contentClassName='sm:max-w-[620px]'
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button type='submit' form={PAYMENT_METHOD_FORM_ID}>
            {isEditMode ? t('Update') : t('Add')}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id={PAYMENT_METHOD_FORM_ID}
          onSubmit={form.handleSubmit(handleSubmit)}
          className='space-y-4'
        >
          <FormField
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <FormItem className='bg-muted/20 flex items-center justify-between rounded-md border p-3'>
                <div className='space-y-1'>
                  <FormLabel>{t('Payment method enabled')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Disabled methods are hidden from users and rejected at checkout.'
                    )}
                  </FormDescription>
                </div>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='name'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Name')}</FormLabel>
                <FormControl>
                  <Input placeholder={t('e.g., Alipay, WeChat')} {...field} />
                </FormControl>
                <FormDescription>
                  {t('Display name for this payment method.')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='type'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Payment type key')}</FormLabel>
                <FormControl>
                  <Combobox
                    options={paymentTypeOptions}
                    value={field.value}
                    onValueChange={(value) => {
                      if (value === null) return
                      const currentIcon = form.getValues('icon')?.trim()
                      const currentName = form.getValues('name')?.trim()
                      const previousOption = getPaymentTypeOption(field.value)
                      const nextOption = getPaymentTypeOption(value)

                      field.onChange(value)
                      if (usesDedicatedPaymentPricing(value)) {
                        form.setValue('topup_ratio', '', {
                          shouldDirty: true,
                          shouldValidate: true,
                        })
                        for (const key of [
                          'settlement_currency',
                          'settlement_units_per_usd',
                          'platform_units_per_usd',
                          'settlement_units_per_platform_unit',
                          'settlement_unit',
                          'unit_price',
                        ] as const) {
                          form.setValue(key, '', {
                            shouldDirty: true,
                            shouldValidate: true,
                          })
                        }
                      }
                      if (
                        nextOption?.iconName &&
                        (!currentIcon ||
                          currentIcon === previousOption?.iconName)
                      ) {
                        form.setValue('icon', nextOption.iconName, {
                          shouldDirty: true,
                        })
                      }
                      if (
                        nextOption?.name &&
                        (!currentName || currentName === previousOption?.name)
                      ) {
                        form.setValue('name', nextOption.name, {
                          shouldDirty: true,
                        })
                      }
                    }}
                    placeholder={t('Select or enter payment type key')}
                    searchPlaceholder={t('Search payment type keys...')}
                    allowCustomValue
                  />
                </FormControl>
                <FormDescription className='leading-relaxed'>
                  {t(
                    'Used to decide the payment flow. Built-in keys include stripe for Stripe and waffo_pancake for Waffo Pancake; other values are sent to Epay as the type parameter.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='icon'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Icon')}</FormLabel>
                <FormControl>
                  <div className='flex items-center gap-2'>
                    <Input
                      placeholder={t('e.g., SiAlipay')}
                      {...field}
                      className='flex-1'
                    />
                    {iconValue && (
                      <ReactIconByName
                        name={iconValue}
                        className='text-muted-foreground size-5 shrink-0'
                        title={iconValue}
                      />
                    )}
                  </div>
                </FormControl>
                <FormDescription>
                  {t(
                    'Enter a react-icons component name. Invalid names show no icon.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <div className='grid gap-4 sm:grid-cols-[1fr_auto]'>
            <FormField
              control={form.control}
              name='description'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('User-facing payment description')}</FormLabel>
                  <FormControl>
                    <Textarea
                      rows={2}
                      placeholder={t(
                        'Optional instructions or maintenance note'
                      )}
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Shown on the user payment button; do not put secrets here.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='color'
              render={({ field }) => (
                <FormItem className='sm:w-36'>
                  <FormLabel>{t('Display color')}</FormLabel>
                  <FormControl>
                    <div className='flex items-center gap-2'>
                      <Input
                        type='color'
                        value={
                          field.value && /^#[0-9a-fA-F]{6}$/.test(field.value)
                            ? field.value
                            : '#64748b'
                        }
                        onChange={field.onChange}
                        className='h-10 w-12 cursor-pointer p-1'
                      />
                      <Input placeholder='#64748b' {...field} />
                    </div>
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <FormField
            control={form.control}
            name='min_topup'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Minimum top-up (optional)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    step='0.01'
                    placeholder={t('e.g., 50')}
                    {...field}
                  />
                </FormControl>
                <FormDescription>
                  {t('Optional minimum recharge amount for this method.')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='max_topup'
            render={({ field }) => (
              <FormItem>
                <FormLabel>
                  {t('Maximum credited amount per payment (USD, optional)')}
                </FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min='0.01'
                    step='0.01'
                    placeholder={t('e.g., 20')}
                    {...field}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Limit how many US dollars can be credited in one payment. Leave empty for no per-method limit.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='unlock_after_days'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Unlock after registration (days)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min='0'
                    step='1'
                    placeholder='0'
                    {...field}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Users can use this payment method after this many full days. Leave empty or set 0 for immediate access.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <div className='bg-muted/20 space-y-4 rounded-md border p-3'>
            <div>
              <p className='text-sm font-medium'>{t('Payment audience')}</p>
              <p className='text-muted-foreground mt-1 text-xs leading-relaxed'>
                {t(
                  'Control who can see and use this method. Checkout uses the same server-side rules.'
                )}
              </p>
            </div>

            <FormField
              control={form.control}
              name='audience_mode'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Visibility')}</FormLabel>
                  <FormControl>
                    <Combobox
                      options={[
                        {
                          label: t('Follow legacy account restrictions'),
                          value: 'legacy',
                        },
                        { label: t('Visible to everyone'), value: 'all' },
                        {
                          label: t('Visible only to matching users'),
                          value: 'include',
                        },
                        {
                          label: t('Hidden from matching users'),
                          value: 'exclude',
                        },
                      ]}
                      value={field.value}
                      onValueChange={(value) => {
                        if (value) field.onChange(value)
                      }}
                      placeholder={t('Select visibility')}
                      searchPlaceholder={t('Search visibility options...')}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Legacy mode preserves the existing special-account payment marker until you define a rule.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            {(audienceMode === 'include' || audienceMode === 'exclude') && (
              <>
                <FormField
                  control={form.control}
                  name='audience_match'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {t('When multiple conditions exist')}
                      </FormLabel>
                      <FormControl>
                        <Combobox
                          options={[
                            {
                              label: t('Match any condition'),
                              value: 'any',
                            },
                            {
                              label: t('Match all conditions'),
                              value: 'all',
                            },
                          ]}
                          value={field.value}
                          onValueChange={(value) => {
                            if (value) field.onChange(value)
                          }}
                          placeholder={t('Select condition matching')}
                          searchPlaceholder={t('Search matching options...')}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='audience_email_contains'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Email contains')}</FormLabel>
                      <FormControl>
                        <InputGroup>
                          <InputGroupInput placeholder='linux.do' {...field} />
                          <InputGroupAddon align='inline-end'>
                            <InputGroupButton
                              type='button'
                              variant='ghost'
                              onClick={() =>
                                form.setValue(
                                  'audience_email_contains',
                                  'linux.do',
                                  { shouldDirty: true, shouldValidate: true }
                                )
                              }
                            >
                              {t('Use linux.do preset')}
                            </InputGroupButton>
                          </InputGroupAddon>
                        </InputGroup>
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Case-insensitive substring match against the email.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='audience_oauth_provider'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('OAuth login method')}</FormLabel>
                      <FormControl>
                        <Combobox
                          options={[
                            { label: t('No OAuth condition'), value: 'none' },
                            { label: 'LinuxDO', value: 'linuxdo' },
                            { label: 'GitHub', value: 'github' },
                            { label: 'Discord', value: 'discord' },
                            { label: 'OIDC', value: 'oidc' },
                            { label: t('WeChat'), value: 'wechat' },
                            { label: 'Telegram', value: 'telegram' },
                          ]}
                          value={field.value || 'none'}
                          onValueChange={(value) => {
                            if (value) field.onChange(value)
                          }}
                          placeholder={t('Select OAuth login method')}
                          searchPlaceholder={t('Search OAuth login methods...')}
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'LinuxDO is the preset; other supported account bindings can also be matched.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <div className='grid gap-3 sm:grid-cols-2'>
                  <FormField
                    control={form.control}
                    name='audience_linuxdo_score_min'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Minimum LinuxDO score')}</FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min='0'
                            step='any'
                            placeholder='0'
                            {...field}
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <FormField
                    control={form.control}
                    name='audience_linuxdo_score_max'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Maximum LinuxDO score')}</FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min='0'
                            step='any'
                            placeholder={t('No maximum')}
                            {...field}
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </div>
                <div className='grid gap-3 sm:grid-cols-2'>
                  <FormField
                    control={form.control}
                    name='audience_user_group'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('User group condition')}</FormLabel>
                        <FormControl>
                          <Input placeholder='default, vip' {...field} />
                        </FormControl>
                        <FormDescription>
                          {t(
                            'Comma-separated user groups; matching is case-insensitive.'
                          )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <FormField
                    control={form.control}
                    name='audience_role'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Account role condition')}</FormLabel>
                        <FormControl>
                          <Combobox
                            options={getPaymentMethodAudienceRoleOptions(t)}
                            value={field.value || 'none'}
                            onValueChange={(value: string | null) =>
                              value && field.onChange(value)
                            }
                            placeholder={t('Select account role')}
                            searchPlaceholder={t('Search account roles...')}
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </div>
                <p className='text-muted-foreground text-xs leading-relaxed'>
                  {t(
                    'LinuxDO score is refreshed when the user signs in with LinuxDO OAuth.'
                  )}
                </p>
              </>
            )}
          </div>

          {!usesDedicatedPricing && (
            <FormField
              control={form.control}
              name='topup_ratio'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Payment multiplier (optional)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min='0.000000000001'
                      step='any'
                      placeholder='1'
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      "Multiplied with the user's group top-up multiplier. Leave empty for 1."
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          )}

          {usesDedicatedPricing ? (
            <div className='bg-muted/40 rounded-md border p-3 text-sm'>
              <p className='font-medium'>{t('Dedicated gateway pricing')}</p>
              <p className='text-muted-foreground mt-1 text-xs leading-relaxed'>
                {t(
                  'This payment flow uses its dedicated gateway price setting, so per-method settlement pricing is not applied here.'
                )}
              </p>
            </div>
          ) : (
            <>
              <FormField
                control={form.control}
                name='settlement_currency'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Settlement currency (ISO code)')}</FormLabel>
                    <FormControl>
                      <Input
                        placeholder='CNY'
                        maxLength={3}
                        value={field.value ?? ''}
                        onChange={(event) =>
                          field.onChange(event.target.value.toUpperCase())
                        }
                        onBlur={field.onBlur}
                        name={field.name}
                        ref={field.ref}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('The actual fiat currency charged by this gateway.')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='settlement_units_per_usd'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      {t('Settlement amount per 1 real USD')}
                    </FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min='0.000000000001'
                        step='any'
                        placeholder='6.8'
                        {...field}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Example: enter 1 for USD or 6.8 when 1 USD equals 6.8 CNY.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <div className='bg-muted/30 space-y-2 rounded-md border p-3'>
                <p className='text-muted-foreground text-xs leading-relaxed'>
                  {t(
                    'Checkout first converts the platform amount to real USD using the synchronized USD rate, then converts USD to the gateway settlement currency.'
                  )}
                </p>
                {settlementCurrencyValue && settlementRateValue ? (
                  <p className='text-sm font-medium'>
                    {t('Settlement preview: 1 USD = {{rate}} {{currency}}', {
                      rate: settlementRateValue,
                      currency: settlementCurrencyValue,
                    })}
                  </p>
                ) : null}
                {legacySettlementUnit && legacyDirectRate ? (
                  <p className='text-warning text-xs leading-relaxed'>
                    {t(
                      'Legacy direct pricing is preserved until you enter and save the real-USD settlement fields above.'
                    )}
                  </p>
                ) : null}
              </div>
            </>
          )}
        </form>
      </Form>
    </Dialog>
  )
}
