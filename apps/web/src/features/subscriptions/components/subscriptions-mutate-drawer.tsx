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
import {
  CalendarClock,
  CreditCard,
  RefreshCw,
  Settings2,
} from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useForm, useWatch, type Resolver } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CreditAmountInput } from '@/components/credit-amount-input'
import { BadgeCell } from '@/components/data-table'
import {
  SideDrawerSection,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
  sideDrawerSwitchItemClassName,
} from '@/components/drawer-layout'
import { StatusBadge } from '@/components/status-badge'
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
import { IconBadge } from '@/components/ui/icon-badge'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import { useCreditInputDisplay } from '@/hooks/use-credit-input-display'

import {
  createPlan,
  updatePlan,
  getGroups,
  createWaffoPancakePlanProduct,
  listWaffoPancakePlanProductOptions,
} from '../api'
import { getDurationUnitOptions, getResetPeriodOptions } from '../constants'
import {
  getPlanFormSchema,
  PLAN_FORM_DEFAULTS,
  planToFormValues,
  formValuesToPlanPayload,
  getSubscriptionPaymentMethodLabel,
  type PlanFormValues,
} from '../lib'
import type { PlanRecord, WaffoPancakeProductType } from '../types'
import { useSubscriptions } from './subscriptions-provider'
import { WaffoPancakeProductsEditor } from './waffo-pancake-products-editor'
import {
  ensurePancakePlanProducts,
  pancakeProductsForPlan,
  pancakeProductsPayload,
  PancakeProductCreationUncertain,
} from '../lib/waffo-pancake-products'

interface PancakeProductOption {
  id: string
  name: string
  status: string
  billingPeriod?: string
  product_type: WaffoPancakeProductType
}

type RawPancakeProductOption = Omit<PancakeProductOption, 'product_type'> & {
  product_type?: WaffoPancakeProductType
}

function normalizePancakeProductOptions(
  products: RawPancakeProductOption[]
): PancakeProductOption[] {
  return products.map((product) => ({
    ...product,
    // Older providers returned subscription products without a discriminator.
    product_type:
      product.product_type === 'one_time' ? 'one_time' : 'subscription',
  }))
}

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow?: PlanRecord
}

export function SubscriptionsMutateDrawer({
  open,
  onOpenChange,
  currentRow,
}: Props) {
  const { t } = useTranslation()
  const isEdit = !!currentRow?.plan?.id
  const { triggerRefresh } = useSubscriptions()
  const { label: currencyLabel } = useCreditInputDisplay()
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [groupOptions, setGroupOptions] = useState<string[]>([])
  const submitInFlight = useRef(false)
  const [pancakeDraft, setPancakeDraft] = useState(() => pancakeProductsForPlan())
  const [pancakeBlockedTypes, setPancakeBlockedTypes] = useState<Set<WaffoPancakeProductType>>(new Set())
  const [pancakeProducts, setPancakeProducts] = useState<
    PancakeProductOption[]
  >([])

  const schema = getPlanFormSchema(t)
  const form = useForm<PlanFormValues>({
    // SAFETY: getPlanFormSchema is the sole runtime schema for PlanFormValues;
    // the bridge is needed because the installed resolver types erase coerced inputs.
    resolver: zodResolver(schema) as unknown as Resolver<PlanFormValues>,
    defaultValues: PLAN_FORM_DEFAULTS,
  })

  useEffect(() => {
    if (open) {
      setPancakeDraft(pancakeProductsForPlan(currentRow?.plan))
      setPancakeBlockedTypes(new Set())
      if (currentRow?.plan) {
        form.reset(planToFormValues(currentRow.plan))
      } else {
        form.reset(PLAN_FORM_DEFAULTS)
      }
      getGroups()
        .then((res) => {
          if (res.success) setGroupOptions(res.data || [])
        })
        .catch(() => {})
      // Best-effort — empty list still lets the operator use "+ Create".
      listWaffoPancakePlanProductOptions()
        .then((res) => {
          const products = res.data?.products
          if (res.message === 'success' && Array.isArray(products)) {
            setPancakeProducts(normalizePancakeProductOptions(products))
          } else {
            setPancakeProducts([])
          }
        })
        .catch(() => setPancakeProducts([]))
    }
  }, [open, currentRow, form])

  const durationUnit = useWatch({
    control: form.control,
    name: 'duration_unit',
  })
  const resetPeriod = useWatch({
    control: form.control,
    name: 'quota_reset_period',
  })
  const watchedAllowBalancePay = useWatch({
    control: form.control,
    name: 'allow_balance_pay',
  })
  const watchedStripePriceId = useWatch({
    control: form.control,
    name: 'stripe_price_id',
  })
  const watchedCreemProductId = useWatch({
    control: form.control,
    name: 'creem_product_id',
  })
  const [pancakeTitle, pancakePrice, pancakeCurrency, pancakeDurationValue, pancakeCustomSeconds] = useWatch({
    control: form.control,
    name: ['title', 'price_amount', 'currency', 'duration_value', 'custom_seconds'],
  })
  const pancakeTerms = {
    title: pancakeTitle || '',
    price_amount: Number(pancakePrice || 0),
    currency: pancakeCurrency || 'USD',
    duration_unit: durationUnit,
    duration_value: Number(pancakeDurationValue || 1),
    custom_seconds: Number(pancakeCustomSeconds || 0),
  }
  // Generic ePay methods are global rather than plan fields. Preserve the
  // authoritative admin catalog entries while this existing plan is edited.
  const inheritedEpayMethods = (currentRow?.payment_methods || []).filter(
    (method) =>
      !['balance', 'stripe', 'creem', 'waffo_pancake'].includes(method)
  )
  const selectedPaymentMethods = [
    watchedAllowBalancePay ? 'balance' : null,
    watchedStripePriceId?.trim() ? 'stripe' : null,
    watchedCreemProductId?.trim() ? 'creem' : null,
    pancakeDraft.some((product) => product.enabled) ? 'waffo_pancake' : null,
    ...inheritedEpayMethods,
  ].filter((method): method is string => !!method)
  const onSubmit = async (values: PlanFormValues) => {
    if (submitInFlight.current) return
    submitInFlight.current = true
    setIsSubmitting(true)
    try {
      const products = await ensurePancakePlanProducts(
        pancakeDraft, values, createWaffoPancakePlanProduct,
        setPancakeDraft, pancakeBlockedTypes
      )
      const payload = formValuesToPlanPayload(values)
      payload.plan.waffo_pancake_products = pancakeProductsPayload(products)
      if (isEdit && currentRow?.plan?.id) {
        const res = await updatePlan(currentRow.plan.id, payload)
        if (res.success) {
          toast.success(t('Update succeeded'))
          onOpenChange(false)
          triggerRefresh()
        }
      } else {
        const res = await createPlan(payload)
        if (res.success) {
          toast.success(t('Create succeeded'))
          onOpenChange(false)
          triggerRefresh()
        }
      }
    } catch (error) {
      if (error instanceof PancakeProductCreationUncertain) {
        setPancakeBlockedTypes((previous) => new Set([...previous, error.productType]))
        toast.error(t('Product creation could not be confirmed. Refresh the catalog and select the product before retrying. Do not create it again blindly.'))
      } else {
        toast.error(t('Request failed'))
      }
    } finally {
      submitInFlight.current = false
      setIsSubmitting(false)
    }
  }

  const refreshPancakeProducts = async () => {
    try {
      const res = await listWaffoPancakePlanProductOptions()
      if (res.message !== 'success' || !Array.isArray(res.data?.products)) {
        toast.error(t('Request failed'))
        return
      }
      setPancakeProducts(normalizePancakeProductOptions(res.data.products))
    } catch {
      toast.error(t('Request failed'))
    }
  }

  const durationUnitOpts = getDurationUnitOptions(t)
  const resetPeriodOpts = getResetPeriodOptions(t)

  return (
    <Sheet
      open={open}
      onOpenChange={(v) => {
        if (submitInFlight.current) return
        onOpenChange(v)
        if (!v) {
          form.reset()
        }
      }}
    >
      <SheetContent className={sideDrawerContentClassName('sm:max-w-[600px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {isEdit ? t('Update plan info') : t('Create new subscription plan')}
          </SheetTitle>
          <SheetDescription>
            {isEdit
              ? t('Modify existing subscription plan configuration')
              : t(
                  'Fill in the following info to create a new subscription plan'
                )}
          </SheetDescription>
        </SheetHeader>
        <Form {...form}>
          <form
            id='subscription-form'
            onSubmit={form.handleSubmit(onSubmit)}
            className={sideDrawerFormClassName()}
          >
            {/* Basic Info */}
            <SideDrawerSection>
              <h3 className='flex items-center gap-2 text-sm font-medium'>
                <IconBadge tone='info' size='xs'>
                  <Settings2 />
                </IconBadge>
                {t('Basic Info')}
              </h3>

              <FormField
                control={form.control}
                name='title'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Plan Title')}</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder={t('e.g. Basic Plan')} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='subtitle'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Plan Subtitle')}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        placeholder={t('e.g. Suitable for light usage')}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <div className='grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3'>
                <FormField
                  control={form.control}
                  name='price_amount'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Plan Price')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          step='0.01'
                          min={0}
                          onChange={(e) =>
                            field.onChange(Number(e.target.value))
                          }
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Real fiat list price; gateways convert it to their settlement currency and wallet payments debit the equivalent credits.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='currency'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Plan Price Currency')}</FormLabel>
                      <Select
                        value={field.value}
                        onValueChange={field.onChange}
                      >
                        <FormControl>
                          <SelectTrigger>
                            <SelectValue />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent>
                          <SelectItem value='CNY'>CNY · 人民币</SelectItem>
                          <SelectItem value='USD'>USD</SelectItem>
                        </SelectContent>
                      </Select>
                      <FormDescription>
                        {t(
                          'Payments use real fiat. Wallet balances are stored in credits.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='total_amount'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {t('Quota ({{currency}})', { currency: currencyLabel })}
                      </FormLabel>
                      <FormControl>
                        <CreditAmountInput
                          {...field}
                          placeholder={t('Enter quota in {{currency}}', {
                            currency: currencyLabel,
                          })}
                          onValueChange={field.onChange}
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Total quota included in the plan, usable per billing period. 0 means unlimited.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>

              <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='upgrade_group'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Upgrade Group')}</FormLabel>
                      <Select
                        items={[
                          { value: '__none__', label: t('No Upgrade') },
                          ...groupOptions.map((g) => ({ value: g, label: g })),
                        ]}
                        onValueChange={(v) =>
                          field.onChange(v === '__none__' ? '' : v)
                        }
                        value={field.value || ''}
                      >
                        <FormControl>
                          <SelectTrigger>
                            <SelectValue placeholder={t('No Upgrade')} />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent alignItemWithTrigger={false}>
                          <SelectGroup>
                            <SelectItem value='__none__'>
                              {t('No Upgrade')}
                            </SelectItem>
                            {groupOptions.map((g) => (
                              <SelectItem key={g} value={g}>
                                {g}
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='downgrade_group'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Downgrade Group')}</FormLabel>
                      <Select
                        items={[
                          {
                            value: '__none__',
                            label: t('Downgrade to pre-purchase group'),
                          },
                          ...groupOptions.map((g) => ({ value: g, label: g })),
                        ]}
                        onValueChange={(v) =>
                          field.onChange(v === '__none__' ? '' : v)
                        }
                        value={field.value || ''}
                      >
                        <FormControl>
                          <SelectTrigger>
                            <SelectValue
                              placeholder={t('Downgrade to pre-purchase group')}
                            />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent alignItemWithTrigger={false}>
                          <SelectGroup>
                            <SelectItem value='__none__'>
                              {t('Downgrade to pre-purchase group')}
                            </SelectItem>
                            {groupOptions.map((g) => (
                              <SelectItem key={g} value={g}>
                                {g}
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                      <FormDescription>
                        {t(
                          'Downgrade to this group after the subscription expires'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='max_purchase_per_user'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Purchase Limit')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          min={0}
                          onChange={(e) =>
                            field.onChange(
                              Number.parseInt(e.target.value, 10) || 0
                            )
                          }
                        />
                      </FormControl>
                      <FormDescription>
                        {t('0 means unlimited')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>

              <FormField
                control={form.control}
                name='sort_order'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Sort Order')}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        type='number'
                        onChange={(e) =>
                          field.onChange(
                            Number.parseInt(e.target.value, 10) || 0
                          )
                        }
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <div className='flex flex-col gap-3'>
                <FormField
                  control={form.control}
                  name='enabled'
                  render={({ field }) => (
                    <FormItem className={sideDrawerSwitchItemClassName()}>
                      <FormLabel className='!mt-0'>
                        {t('Enabled Status')}
                      </FormLabel>
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
                  name='allow_wallet_overflow'
                  render={({ field }) => (
                    <FormItem className={sideDrawerSwitchItemClassName()}>
                      <FormLabel className='!mt-0'>
                        {t('Allow wallet balance after quota used up')}
                      </FormLabel>
                      <FormControl>
                        <Switch
                          checked={field.value}
                          onCheckedChange={field.onChange}
                        />
                      </FormControl>
                    </FormItem>
                  )}
                />
              </div>
            </SideDrawerSection>

            {/* Duration Settings */}
            <SideDrawerSection>
              <h3 className='flex items-center gap-2 text-sm font-medium'>
                <IconBadge tone='chart-4' size='xs'>
                  <CalendarClock />
                </IconBadge>
                {t('Duration Settings')}
              </h3>

              <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='duration_unit'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Duration Unit')}</FormLabel>
                      <Select
                        items={durationUnitOpts.map((o) => ({
                          value: o.value,
                          label: o.label,
                        }))}
                        onValueChange={field.onChange}
                        value={field.value}
                      >
                        <FormControl>
                          <SelectTrigger>
                            <SelectValue />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent alignItemWithTrigger={false}>
                          <SelectGroup>
                            {durationUnitOpts.map((o) => (
                              <SelectItem key={o.value} value={o.value}>
                                {o.label}
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                {durationUnit === 'custom' ? (
                  <FormField
                    control={form.control}
                    name='custom_seconds'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Custom Seconds')}</FormLabel>
                        <FormControl>
                          <Input
                            {...field}
                            type='number'
                            min={1}
                            onChange={(e) =>
                              field.onChange(
                                Number.parseInt(e.target.value, 10) || 0
                              )
                            }
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                ) : (
                  <FormField
                    control={form.control}
                    name='duration_value'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Duration Value')}</FormLabel>
                        <FormControl>
                          <Input
                            {...field}
                            type='number'
                            min={1}
                            onChange={(e) =>
                              field.onChange(
                                Number.parseInt(e.target.value, 10) || 0
                              )
                            }
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                )}
              </div>
            </SideDrawerSection>

            {/* Quota Reset */}
            <SideDrawerSection>
              <h3 className='flex items-center gap-2 text-sm font-medium'>
                <IconBadge tone='success' size='xs'>
                  <RefreshCw />
                </IconBadge>
                {t('Quota Reset')}
              </h3>

              <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='quota_reset_period'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Reset Cycle')}</FormLabel>
                      <Select
                        items={resetPeriodOpts.map((o) => ({
                          value: o.value,
                          label: o.label,
                        }))}
                        onValueChange={field.onChange}
                        value={field.value}
                      >
                        <FormControl>
                          <SelectTrigger>
                            <SelectValue />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent alignItemWithTrigger={false}>
                          <SelectGroup>
                            {resetPeriodOpts.map((o) => (
                              <SelectItem key={o.value} value={o.value}>
                                {o.label}
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='quota_reset_custom_seconds'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Custom Seconds')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          min={0}
                          disabled={resetPeriod !== 'custom'}
                          onChange={(e) =>
                            field.onChange(
                              Number.parseInt(e.target.value, 10) || 0
                            )
                          }
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>
            </SideDrawerSection>

            {/* Payment Config */}
            <SideDrawerSection>
              <h3 className='flex items-center gap-2 text-sm font-medium'>
                <IconBadge tone='warning' size='xs'>
                  <CreditCard />
                </IconBadge>
                {t('Payment Methods')}
              </h3>

              <div className='flex flex-col gap-2 rounded-md border p-3'>
                <span className='text-muted-foreground text-xs'>
                  {t('Payment Channel')}
                </span>
                <BadgeCell>
                  {selectedPaymentMethods.length === 0 ? (
                    <StatusBadge
                      label={t('Not configured')}
                      variant='danger'
                      copyable={false}
                    />
                  ) : (
                    selectedPaymentMethods.map((method) => (
                      <StatusBadge
                        key={method}
                        label={getSubscriptionPaymentMethodLabel(method, t)}
                        variant='neutral'
                        copyable={false}
                      />
                    ))
                  )}
                </BadgeCell>
              </div>

              <FormField
                control={form.control}
                name='allow_balance_pay'
                render={({ field }) => (
                  <FormItem className={sideDrawerSwitchItemClassName()}>
                    <FormLabel className='!mt-0'>
                      {t('Allow balance redemption')}
                    </FormLabel>
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
                name='stripe_price_id'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Stripe Price ID')}</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder='price_...' />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='creem_product_id'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Creem Product ID')}</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder='prod_...' />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <WaffoPancakeProductsEditor
                value={pancakeDraft}
                onChange={setPancakeDraft}
                terms={pancakeTerms}
                products={pancakeProducts}
                disabled={isSubmitting}
                uncertain={pancakeBlockedTypes.size > 0}
                onRefresh={() => void refreshPancakeProducts()}
              />
            </SideDrawerSection>
          </form>
        </Form>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' />}>
            {t('Close')}
          </SheetClose>
          <Button
            form='subscription-form'
            type='submit'
            disabled={isSubmitting}
          >
            {isSubmitting ? t('Saving...') : t('Save changes')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
