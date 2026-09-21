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
import { Check, Loader2 } from 'lucide-react'
import { useCallback, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { BadgeCell, TruncatedCell } from '@/components/data-table'
import { GroupBadge } from '@/components/group-badge'
import { StatusBadge } from '@/components/status-badge'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { cn } from '@/lib/utils'

import { updateApiKey } from '../api'
import { ERROR_MESSAGES, SUCCESS_MESSAGES } from '../constants'
import { buildGroupChangePayload } from '../lib'
import type { ApiKey } from '../types'
import type { ApiKeyGroupOption } from './api-key-group-combobox'
import { useApiKeys } from './api-keys-provider'
import {
  AutoGroupBadge,
  AutoGroupFlowBorder,
  GroupRatioBadge,
  type GroupRatio,
} from './auto-group-visuals'

type ApiKeyGroupQuickSwitchProps = {
  apiKey: ApiKey
  options: ApiKeyGroupOption[]
  optionsLoading: boolean
  ratio?: GroupRatio
  shouldReduceMotion: boolean
}

export function ApiKeyGroupQuickSwitch({
  apiKey,
  options,
  optionsLoading,
  ratio,
  shouldReduceMotion,
}: ApiKeyGroupQuickSwitchProps) {
  const { t } = useTranslation()
  const { triggerRefresh } = useApiKeys()
  const group = apiKey.group || ''
  const isAuto = group === 'auto'

  const [open, setOpen] = useState(false)
  const [searchValue, setSearchValue] = useState('')
  const [isSwitching, setIsSwitching] = useState(false)
  const [pendingGroup, setPendingGroup] = useState<ApiKeyGroupOption | null>(
    null
  )
  const [warningConfirmations, setWarningConfirmations] = useState(0)
  const inFlightRef = useRef(false)

  const warningConfirmationsRequired = pendingGroup?.warning
    ? Math.min(3, Math.max(1, pendingGroup.warning.confirmations || 1))
    : 1

  const filteredOptions = useMemo(() => {
    const search = searchValue.trim().toLowerCase()
    if (!search) return options

    return options.filter((option) => {
      const ratioText = String(option.ratio ?? '').toLowerCase()
      return (
        option.value.toLowerCase().includes(search) ||
        option.label.toLowerCase().includes(search) ||
        option.desc?.toLowerCase().includes(search) ||
        ratioText.includes(search)
      )
    })
  }, [options, searchValue])

  const applyGroupChange = useCallback(
    async (targetGroup: string, confirmations: number) => {
      if (inFlightRef.current) return
      inFlightRef.current = true
      setIsSwitching(true)
      try {
        const payload = buildGroupChangePayload(apiKey, targetGroup)
        const result = await updateApiKey({
          ...payload,
          id: apiKey.id,
          group_warning_confirmations: confirmations,
        })
        if (result.success) {
          toast.success(t(SUCCESS_MESSAGES.API_KEY_UPDATED))
          triggerRefresh()
        } else {
          toast.error(result.message || t(ERROR_MESSAGES.UPDATE_FAILED))
        }
      } catch {
        toast.error(t(ERROR_MESSAGES.UNEXPECTED))
      } finally {
        inFlightRef.current = false
        setIsSwitching(false)
        setPendingGroup(null)
        setWarningConfirmations(0)
      }
    },
    [apiKey, t, triggerRefresh]
  )

  const handleSelect = useCallback(
    (option: ApiKeyGroupOption) => {
      setOpen(false)
      setSearchValue('')
      if (isSwitching || option.value === group) return

      if (option.warning?.enabled) {
        setPendingGroup(option)
        setWarningConfirmations(0)
        return
      }

      void applyGroupChange(option.value, 0)
    },
    [applyGroupChange, group, isSwitching]
  )

  const confirmWarning = useCallback(() => {
    if (!pendingGroup || isSwitching) return
    const next = warningConfirmations + 1
    if (next >= warningConfirmationsRequired) {
      void applyGroupChange(pendingGroup.value, next)
    } else {
      setWarningConfirmations(next)
    }
  }, [
    applyGroupChange,
    isSwitching,
    pendingGroup,
    warningConfirmations,
    warningConfirmationsRequired,
  ])

  const triggerDisabled = isSwitching || optionsLoading

  return (
    <>
      <Popover
        open={open}
        onOpenChange={(next) => {
          if (triggerDisabled) return
          setOpen(next)
          if (!next) setSearchValue('')
        }}
      >
        <PopoverTrigger
          render={
            <button
              type='button'
              disabled={triggerDisabled}
              aria-label={t('Group')}
              data-api-key-group-quick-switch=''
              className='group focus-visible:ring-ring/50 -ml-1.5 inline-flex max-w-full min-w-0 items-center rounded-4xl text-left focus-visible:ring-[3px] focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-70'
            />
          }
        >
          <span className='max-w-full min-w-0'>
            {isAuto ? (
              <BadgeCell
                data-api-key-group-cell='auto'
                className='gap-1.5 overflow-visible text-xs'
              >
                <StatusBadge
                  label={t('Cross-group')}
                  variant='info'
                  copyable={false}
                />
                <AutoGroupBadge shouldReduceMotion={shouldReduceMotion} />
                <GroupRatioBadge
                  ratio={ratio}
                  isAuto
                  shouldReduceMotion={shouldReduceMotion}
                />
              </BadgeCell>
            ) : (
              <TruncatedCell
                tooltipContent={group || '-'}
                tooltipClassName='break-all'
              >
                <GroupBadge
                  group={group}
                  ratio={typeof ratio === 'number' ? ratio : undefined}
                />
              </TruncatedCell>
            )}
          </span>
          {isSwitching && (
            <Loader2
              aria-hidden='true'
              className='text-muted-foreground ml-1 size-3.5 shrink-0 animate-spin'
            />
          )}
        </PopoverTrigger>
        <PopoverContent
          className='data-closed:zoom-out-100 data-open:zoom-in-100 data-[side=bottom]:slide-in-from-top-0 data-[side=left]:slide-in-from-right-0 data-[side=right]:slide-in-from-left-0 data-[side=top]:slide-in-from-bottom-0 w-72 overflow-hidden rounded-xl p-0 shadow-lg data-closed:duration-75 data-open:duration-100'
          align='start'
          onWheel={(event) => event.stopPropagation()}
          onTouchMove={(event) => event.stopPropagation()}
          onPointerDown={(event) => event.stopPropagation()}
        >
          <Command shouldFilter={false}>
            <CommandInput
              placeholder={t('Search...')}
              value={searchValue}
              onValueChange={setSearchValue}
            />
            <CommandList className='max-h-[320px]'>
              <CommandEmpty>{t('No group found.')}</CommandEmpty>
              <CommandGroup>
                {filteredOptions.map((option) => {
                  const isAutoOption = option.value === 'auto'

                  return (
                    <CommandItem
                      key={option.value}
                      value={option.value}
                      data-auto-group-effect={
                        isAutoOption ? 'option' : undefined
                      }
                      onSelect={() => handleSelect(option)}
                      className={cn(
                        'data-[selected=true]:bg-muted items-start gap-3 rounded-lg px-3 py-3 transition-colors',
                        isAutoOption &&
                          'border-primary/35 data-[selected=true]:border-primary/55 relative overflow-visible border shadow-none'
                      )}
                    >
                      {isAutoOption && (
                        <AutoGroupFlowBorder
                          shouldReduceMotion={shouldReduceMotion}
                        />
                      )}
                      <Check
                        aria-hidden='true'
                        className={cn(
                          'mt-0.5 size-4',
                          option.value === group ? 'opacity-100' : 'opacity-0'
                        )}
                      />
                      <span className='min-w-0 flex-1'>
                        <span className='block truncate font-medium'>
                          {option.label}
                        </span>
                        {option.desc && (
                          <span className='text-muted-foreground block truncate text-xs'>
                            {option.desc}
                          </span>
                        )}
                      </span>
                      <GroupRatioBadge
                        ratio={option.ratio}
                        isAuto={isAutoOption}
                        shouldReduceMotion={shouldReduceMotion}
                      />
                    </CommandItem>
                  )
                })}
              </CommandGroup>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>

      <AlertDialog
        open={pendingGroup !== null}
        onOpenChange={(next) => {
          if (!next && !isSwitching) setPendingGroup(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Group warning')}</AlertDialogTitle>
            <AlertDialogDescription className='whitespace-pre-wrap'>
              {pendingGroup?.warning?.message}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <p className='text-muted-foreground text-sm'>
            {t('Confirmation {{current}} of {{total}}', {
              current: Math.min(
                warningConfirmations + 1,
                warningConfirmationsRequired
              ),
              total: warningConfirmationsRequired,
            })}
          </p>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={isSwitching}>
              {t('Cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={isSwitching}
              onClick={(event) => {
                event.preventDefault()
                confirmWarning()
              }}
            >
              {warningConfirmations + 1 >= warningConfirmationsRequired
                ? t('I understand, continue')
                : t('Continue')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
