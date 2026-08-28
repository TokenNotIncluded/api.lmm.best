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
import { Lightbulb, Pencil, Plus, Search, Trash2 } from 'lucide-react'
import { useState, useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { StaticRowActions } from '@/components/data-table/static/static-row-actions'
import { ReactIconByName } from '@/components/react-icon-by-name'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { getPlatformCurrencyLabel } from '@/lib/currency'

import { safeJsonParseWithValidation } from '../utils/json-parser'
import { isArray } from '../utils/json-validators'
import {
  PaymentMethodDialog,
  type PaymentMethodData,
} from './payment-method-dialog'
import {
  insertPaymentMethodTemplate,
  PAYMENT_METHOD_TEMPLATES,
} from './payment-method-templates'
import { isValidPaymentMethodData } from './payment-method-validation'

type PaymentMethodsVisualEditorProps = {
  value: string
  onChange: (value: string) => void
}

const PAYMENT_TYPE_ICON_NAMES: Record<string, string> = {
  alipay: 'SiAlipay',
  epay: 'SiLinux',
  stripe: 'SiStripe',
  wxpay: 'SiWechat',
}

function getDefaultIconName(type: string) {
  return PAYMENT_TYPE_ICON_NAMES[type] ?? ''
}

function getEffectiveIconName(method: PaymentMethodData) {
  return method.icon || getDefaultIconName(method.type)
}

function formatSettlementRule(
  method: PaymentMethodData,
  platformCurrencyLabel: string
): string | null {
  const settlementCurrency = method.settlement_currency?.trim()
  const settlementRate = method.settlement_units_per_usd?.trim()
  if (settlementCurrency && settlementRate) {
    return `${settlementRate} ${settlementCurrency.toUpperCase()}/USD`
  }

  const legacyCurrency = method.settlement_unit?.trim()
  const legacyRate =
    method.settlement_units_per_platform_unit?.trim() ||
    method.unit_price?.trim()
  if (legacyCurrency && legacyRate) {
    return `${legacyRate} ${legacyCurrency}/${platformCurrencyLabel}`
  }
  return settlementCurrency || legacyCurrency || null
}

export function PaymentMethodsVisualEditor({
  value,
  onChange,
}: PaymentMethodsVisualEditorProps) {
  const { t } = useTranslation()
  const platformCurrencyLabel = getPlatformCurrencyLabel(t('Platform'))
  const [searchText, setSearchText] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editData, setEditData] = useState<PaymentMethodData | null>(null)

  const paymentMethods = useMemo(() => {
    const parsed = safeJsonParseWithValidation<unknown[]>(value, {
      fallback: [],
      validator: isArray,
      validatorMessage: 'Payment methods must be a JSON array',
      context: 'payment methods',
    })

    return parsed.filter(isValidPaymentMethodData)
  }, [value])

  const filteredMethods = useMemo(() => {
    if (!searchText) return paymentMethods
    const lowerSearch = searchText.toLowerCase()
    return paymentMethods.filter(
      (method) =>
        method.name.toLowerCase().includes(lowerSearch) ||
        method.type.toLowerCase().includes(lowerSearch) ||
        getEffectiveIconName(method).toLowerCase().includes(lowerSearch) ||
        method.description?.toLowerCase().includes(lowerSearch) ||
        method.audience_user_group?.toLowerCase().includes(lowerSearch)
    )
  }, [paymentMethods, searchText])

  const getAccessDetails = (method: PaymentMethodData) => {
    const unlockDays = Number(method.unlock_after_days || 0)
    const unlockLabel =
      Number.isFinite(unlockDays) && unlockDays > 0
        ? t('After {{days}} days', { days: unlockDays })
        : t('Immediate access')
    const audienceLabel = (() => {
      switch (method.audience_mode) {
        case 'all':
          return t('Visible to everyone')
        case 'include':
          return t('Matching users only')
        case 'exclude':
          return t('Hidden from matching users')
        default:
          return t('Legacy account restrictions')
      }
    })()
    return { audienceLabel, unlockLabel }
  }

  const handleSave = (data: PaymentMethodData) => {
    const parsed = safeJsonParseWithValidation<unknown[]>(value, {
      fallback: [],
      validator: isArray,
      silent: true,
    })

    const updatedArray = [...parsed]

    if (editData) {
      const index = updatedArray.findIndex(
        (item): item is PaymentMethodData =>
          typeof item === 'object' &&
          item !== null &&
          'name' in item &&
          'type' in item &&
          item.name === editData.name &&
          item.type === editData.type
      )
      if (index !== -1) {
        updatedArray[index] = data
      } else {
        updatedArray.push(data)
      }
    } else {
      updatedArray.push(data)
    }

    onChange(JSON.stringify(updatedArray, null, 2))
  }

  const handleDelete = (method: PaymentMethodData) => {
    const parsed = safeJsonParseWithValidation<unknown[]>(value, {
      fallback: [],
      validator: isArray,
      silent: true,
    })

    const updatedArray = parsed.filter(
      (item) =>
        !(
          typeof item === 'object' &&
          item !== null &&
          'name' in item &&
          'type' in item &&
          item.name === method.name &&
          item.type === method.type
        )
    )

    onChange(JSON.stringify(updatedArray, null, 2))
  }

  const handleEdit = (method: PaymentMethodData) => {
    setEditData(method)
    setDialogOpen(true)
  }

  const handleAdd = () => {
    setEditData(null)
    setDialogOpen(true)
  }

  const handleInsertTemplate = (template: PaymentMethodData) => {
    const parsed = safeJsonParseWithValidation<unknown[]>(value, {
      fallback: [],
      validator: isArray,
      silent: true,
    })

    const updated = insertPaymentMethodTemplate(parsed, template)
    if (updated.length === parsed.length) return

    onChange(JSON.stringify(updated, null, 2))
  }

  return (
    <div className='space-y-4'>
      <div className='flex flex-col gap-3 sm:flex-row sm:items-center'>
        <div className='relative flex-1'>
          <Search className='text-muted-foreground absolute top-2.5 left-2.5 h-4 w-4' />
          <Input
            placeholder={t('Search payment methods...')}
            value={searchText}
            onChange={(e) => setSearchText(e.target.value)}
            className='pl-9'
          />
        </div>
        <div className='flex gap-2'>
          <Popover>
            <PopoverTrigger
              render={
                <Button variant='outline' className='flex-1 sm:flex-none' />
              }
            >
              <Lightbulb className='h-4 w-4 sm:mr-2' />
              <span className='sm:inline'>{t('Templates')}</span>
            </PopoverTrigger>
            <PopoverContent className='w-72'>
              <div className='space-y-2'>
                <p className='text-muted-foreground text-xs'>
                  {t('Quick insert payment entries')}
                </p>
                <div className='space-y-1'>
                  {PAYMENT_METHOD_TEMPLATES.map((item) => (
                    <Button
                      key={item.method.type}
                      type='button'
                      variant='ghost'
                      className='h-auto w-full justify-start py-2 text-sm'
                      onClick={(e) => {
                        e.preventDefault()
                        e.stopPropagation()
                        handleInsertTemplate(item.method)
                      }}
                    >
                      <Plus className='mr-2 h-3 w-3' />
                      <span className='flex min-w-0 flex-col items-start'>
                        <span className='truncate'>{t(item.labelKey)}</span>
                        <span className='text-muted-foreground font-mono text-[11px]'>
                          {item.method.type}
                          {formatSettlementRule(
                            item.method,
                            platformCurrencyLabel
                          )
                            ? ` · ${formatSettlementRule(item.method, platformCurrencyLabel)}`
                            : ''}
                        </span>
                      </span>
                    </Button>
                  ))}
                  <Button
                    type='button'
                    variant='ghost'
                    className='h-auto w-full justify-start py-2 text-sm'
                    onClick={(e) => {
                      e.preventDefault()
                      e.stopPropagation()
                      handleAdd()
                    }}
                  >
                    <Plus className='mr-2 h-3 w-3' />
                    <span className='flex min-w-0 flex-col items-start'>
                      <span>{t('Custom Epay method')}</span>
                      <span className='text-muted-foreground text-[11px]'>
                        {t('Enter the payment type supported by your provider')}
                      </span>
                    </span>
                  </Button>
                </div>
              </div>
            </PopoverContent>
          </Popover>
          <Button
            type='button'
            onClick={(e) => {
              e.preventDefault()
              e.stopPropagation()
              handleAdd()
            }}
            className='flex-1 sm:flex-none'
          >
            <Plus className='h-4 w-4 sm:mr-2' />
            <span className='sm:inline'>{t('Add method')}</span>
          </Button>
        </div>
      </div>

      {filteredMethods.length === 0 ? (
        <div className='text-muted-foreground rounded-lg border border-dashed p-8 text-center text-sm'>
          {searchText
            ? t('No payment methods match your search')
            : t(
                'No payment methods configured. Click "Add method" or use templates to get started.'
              )}
        </div>
      ) : (
        <div className='rounded-md border'>
          {/* Desktop table view */}
          <StaticDataTable
            className='hidden rounded-none border-0 md:block'
            data={filteredMethods}
            getRowKey={(method, index) => `${method.type}-${index}`}
            columns={[
              {
                id: 'name',
                header: t('Name'),
                cellClassName: 'font-medium',
                cell: (method) => (
                  <div className='flex min-w-0 items-center gap-2'>
                    {method.color && (
                      <span
                        aria-hidden='true'
                        className='size-2 shrink-0 rounded-full'
                        style={{ backgroundColor: method.color }}
                      />
                    )}
                    <span className='truncate'>{method.name}</span>
                    {method.enabled === 'false' && (
                      <Badge variant='secondary'>{t('Disabled')}</Badge>
                    )}
                  </div>
                ),
              },
              {
                id: 'type',
                header: t('Payment type key'),
                cell: (method) => (
                  <code className='bg-muted rounded px-1.5 py-0.5 text-sm'>
                    {method.type}
                  </code>
                ),
              },
              {
                id: 'icon',
                header: t('Icon'),
                cell: (method) => {
                  const iconName = getEffectiveIconName(method)

                  return iconName ? (
                    <div className='flex items-center gap-2'>
                      <ReactIconByName
                        name={iconName}
                        className='text-muted-foreground size-5 shrink-0'
                        title={iconName}
                      />
                      <span className='text-muted-foreground truncate font-mono text-sm'>
                        {iconName}
                      </span>
                    </div>
                  ) : (
                    <span className='text-muted-foreground text-sm'>—</span>
                  )
                },
              },
              {
                id: 'min-top-up',
                header: t('Min Top-up'),
                cell: (method) =>
                  method.min_topup ? (
                    <span className='font-mono text-sm'>
                      {method.min_topup}
                    </span>
                  ) : (
                    <span className='text-muted-foreground text-sm'>—</span>
                  ),
              },
              {
                id: 'settlement',
                header: t('Settlement'),
                cell: (method) => {
                  const settlementRule = formatSettlementRule(
                    method,
                    platformCurrencyLabel
                  )
                  return settlementRule ? (
                    <span className='font-mono text-sm'>{settlementRule}</span>
                  ) : (
                    <span className='text-muted-foreground text-sm'>—</span>
                  )
                },
              },
              {
                id: 'topup-ratio',
                header: t('Payment multiplier'),
                cell: (method) => (
                  <span className='font-mono text-sm'>
                    ×{method.topup_ratio || '1'}
                  </span>
                ),
              },
              {
                id: 'access',
                header: t('Access'),
                cell: (method) => {
                  const details = getAccessDetails(method)
                  return (
                    <span className='flex flex-col text-xs'>
                      <span>{details.unlockLabel}</span>
                      <span className='text-muted-foreground'>
                        {details.audienceLabel}
                      </span>
                      {method.description && (
                        <span className='text-muted-foreground max-w-44 truncate'>
                          {method.description}
                        </span>
                      )}
                    </span>
                  )
                },
              },
              {
                id: 'actions',
                header: t('Actions'),
                className: 'text-right',
                cellClassName: 'text-right',
                cell: (method) => (
                  <StaticRowActions
                    editLabel={t('Edit')}
                    deleteLabel={t('Delete')}
                    menuLabel={t('Open menu')}
                    onEdit={() => handleEdit(method)}
                    onDelete={() => handleDelete(method)}
                  />
                ),
              },
            ]}
          />

          {/* Mobile card view */}
          <div className='divide-y md:hidden'>
            {filteredMethods.map((method) => {
              const iconName = getEffectiveIconName(method)
              const methodKey = [
                method.type,
                method.name,
                method.icon,
                method.min_topup,
                method.unlock_after_days,
                method.audience_mode,
                method.audience_match,
                method.audience_email_contains,
                method.audience_oauth_provider,
                method.audience_linuxdo_score_min,
                method.audience_linuxdo_score_max,
                method.topup_ratio,
                method.settlement_currency,
                method.settlement_units_per_usd,
                method.settlement_unit,
                method.settlement_units_per_platform_unit,
                method.unit_price,
                method.color,
                method.enabled,
                method.description,
                method.audience_user_group,
                method.audience_role,
              ]
                .filter(Boolean)
                .join('-')

              return (
                <div key={methodKey} className='p-4'>
                  <div className='mb-3 flex items-start justify-between'>
                    <div className='flex-1'>
                      <div className='mb-1 flex items-center gap-2 font-medium'>
                        {method.color && (
                          <span
                            aria-hidden='true'
                            className='size-2 shrink-0 rounded-full'
                            style={{ backgroundColor: method.color }}
                          />
                        )}
                        <span className='truncate'>{method.name}</span>
                        {method.enabled === 'false' && (
                          <Badge variant='secondary'>{t('Disabled')}</Badge>
                        )}
                      </div>
                      <code className='bg-muted rounded px-1.5 py-0.5 text-xs'>
                        {method.type}
                      </code>
                    </div>
                    <div className='flex gap-1'>
                      <Button
                        type='button'
                        variant='ghost'
                        size='sm'
                        onClick={(e) => {
                          e.preventDefault()
                          e.stopPropagation()
                          handleEdit(method)
                        }}
                      >
                        <Pencil className='h-4 w-4' />
                      </Button>
                      <Button
                        type='button'
                        variant='ghost'
                        size='sm'
                        onClick={(e) => {
                          e.preventDefault()
                          e.stopPropagation()
                          handleDelete(method)
                        }}
                      >
                        <Trash2 className='h-4 w-4' />
                      </Button>
                    </div>
                  </div>
                  <div className='space-y-2 text-sm'>
                    <div className='flex items-center gap-2'>
                      <span className='text-muted-foreground min-w-20'>
                        {t('Icon')}
                      </span>
                      {iconName ? (
                        <div className='flex min-w-0 items-center gap-2'>
                          <ReactIconByName
                            name={iconName}
                            className='text-muted-foreground size-5 shrink-0'
                            title={iconName}
                          />
                          <span className='text-muted-foreground truncate font-mono text-xs'>
                            {iconName}
                          </span>
                        </div>
                      ) : (
                        <span className='text-muted-foreground text-xs'>—</span>
                      )}
                    </div>
                    {method.description && (
                      <div className='flex items-start gap-2'>
                        <span className='text-muted-foreground min-w-20'>
                          {t('Description')}:
                        </span>
                        <span className='text-muted-foreground line-clamp-2'>
                          {method.description}
                        </span>
                      </div>
                    )}
                    {method.min_topup && (
                      <div className='flex items-center gap-2'>
                        <span className='text-muted-foreground min-w-20'>
                          {t('Min Top-up:')}
                        </span>
                        <span className='font-mono'>{method.min_topup}</span>
                      </div>
                    )}
                    {formatSettlementRule(method, platformCurrencyLabel) && (
                      <div className='flex items-center gap-2'>
                        <span className='text-muted-foreground min-w-20'>
                          {t('Settlement:')}
                        </span>
                        <span className='font-mono'>
                          {formatSettlementRule(method, platformCurrencyLabel)}
                        </span>
                      </div>
                    )}
                    <div className='flex items-center gap-2'>
                      <span className='text-muted-foreground min-w-20'>
                        {t('Payment multiplier')}:
                      </span>
                      <span className='font-mono'>
                        ×{method.topup_ratio || '1'}
                      </span>
                    </div>
                    <div className='flex items-start gap-2'>
                      <span className='text-muted-foreground min-w-20'>
                        {t('Access')}:
                      </span>
                      <span className='flex flex-col text-xs'>
                        <span>{getAccessDetails(method).unlockLabel}</span>
                        <span className='text-muted-foreground'>
                          {getAccessDetails(method).audienceLabel}
                        </span>
                      </span>
                    </div>
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      )}

      <PaymentMethodDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        onSave={handleSave}
        editData={editData}
      />
    </div>
  )
}
