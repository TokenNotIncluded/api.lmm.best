/*
Copyright (C) 2026 LIghtJUNction
*/
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'

import {
  pancakeProductTerms,
  type PancakeDraftProduct,
  type PancakePlanTerms,
} from '../lib/waffo-pancake-products'
import type { WaffoPancakeProductType } from '../types'

type Props = {
  value: PancakeDraftProduct[]
  onChange: (value: PancakeDraftProduct[]) => void
  terms: PancakePlanTerms
  products: { id: string; name: string; product_type: WaffoPancakeProductType }[]
  disabled: boolean
  uncertain: boolean
  onRefresh: () => void
}

export function WaffoPancakeProductsEditor(props: Props) {
  const { t } = useTranslation()
  const change = (index: number, patch: Partial<PancakeDraftProduct>) => {
    props.onChange(props.value.map((product, position) =>
      position === index ? { ...product, ...patch } : product
    ))
  }
  return (
    <fieldset disabled={props.disabled} className='flex min-w-0 flex-col gap-3'>
      <legend className='mb-2 text-sm font-medium'>Waffo Pancake</legend>
      <p className='text-muted-foreground text-sm'>
        {t('Enable one or both options. Missing products are created when you save.')}
      </p>
      <div className='flex flex-wrap gap-2'>
        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={props.disabled}
          onClick={() => props.onChange(props.value.map((product) => ({ ...product, enabled: true })))}
        >
          {t('Enable both purchase options')}
        </Button>
        <Button type='button' variant='ghost' size='sm' disabled={props.disabled} onClick={props.onRefresh}>
          {t('Refresh')}
        </Button>
      </div>
      {props.uncertain && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Product creation could not be confirmed. Refresh the catalog and select the product before retrying. Do not create it again blindly.')}
        </p>
      )}
      {props.value.map((product, index) => {
        const label = product.product_type === 'one_time'
          ? t('One-time purchase — no auto-renewal')
          : t('Subscription — automatic renewal')
        const items = props.products
          .filter((item) => item.product_type === product.product_type)
          .map((item) => ({ value: item.id, label: `${item.name} (${item.id})` }))
        if (product.product_id && !items.some((item) => item.value === product.product_id)) {
          items.push({ value: product.product_id, label: product.product_id })
        }
        return (
          <div key={product.product_type} className='flex min-w-0 flex-col gap-2 rounded-md border p-3'>
            <div className='flex items-start justify-between gap-3'>
              <label htmlFor={`pancake-enable-${product.product_type}`} className='text-sm font-medium'>{label}</label>
              <Switch
                id={`pancake-enable-${product.product_type}`}
                checked={product.enabled}
                disabled={props.disabled}
                onCheckedChange={(enabled) => change(index, { enabled })}
              />
            </div>
            <p className='text-muted-foreground text-xs'>
              {product.product_type === 'one_time'
                ? t('Pay once for this plan period. It ends without another charge.')
                : t('Payment repeats each period until canceled. Available methods are shown by Pancake.')}
            </p>
            <Select
              items={items}
              value={product.product_id || null}
              disabled={props.disabled || !product.enabled || items.length === 0}
              onValueChange={(id) => change(index, {
                product_id: id || '',
                terms: pancakeProductTerms(props.terms, product.product_type),
              })}
            >
              <SelectTrigger aria-label={`${label} — ${t('Select a product')}`} className='w-full min-w-0'>
                <SelectValue placeholder={t('Create automatically on save')} />
              </SelectTrigger>
              <SelectContent>
                {items.map((item) => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}
              </SelectContent>
            </Select>
            {product.product_id && (
              <Button type='button' variant='ghost' size='sm' disabled={props.disabled} onClick={() => change(index, { product_id: '' })}>
                {t('Create a replacement on save')}
              </Button>
            )}
          </div>
        )
      })}
      <p className='text-muted-foreground text-xs'>
        {t('Disabling an option stops new purchases. It does not cancel existing renewals.')}
      </p>
    </fieldset>
  )
}
