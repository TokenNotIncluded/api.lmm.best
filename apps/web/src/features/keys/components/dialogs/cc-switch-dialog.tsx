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
import { useQuery } from '@tanstack/react-query'
import { useState, useEffect, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { ComboboxInput } from '@/components/ui/combobox-input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Switch } from '@/components/ui/switch'
import { getUserModels } from '@/lib/api'
import { buildCCSwitchProviderURL } from '@/lib/cc-switch-deep-link'
import { openExternalUrl } from '@/lib/external-navigation'
import { validatedExternalUrl } from '@/lib/validated-external-url'

import { getApiKey, setAccountBalanceAccess } from '../../api'

const APP_CONFIGS = {
  claude: {
    label: 'Claude',
    defaultName: 'My Claude',
    modelFields: [
      { key: 'model', labelKey: 'Primary Model', required: true },
      { key: 'haikuModel', labelKey: 'Haiku Model', required: false },
      { key: 'sonnetModel', labelKey: 'Sonnet Model', required: false },
      { key: 'opusModel', labelKey: 'Opus Model', required: false },
    ],
  },
  codex: {
    label: 'Codex',
    defaultName: 'My Codex',
    modelFields: [{ key: 'model', labelKey: 'Primary Model', required: true }],
  },
  gemini: {
    label: 'Gemini',
    defaultName: 'My Gemini',
    modelFields: [{ key: 'model', labelKey: 'Primary Model', required: true }],
  },
} as const

type AppType = keyof typeof APP_CONFIGS

function getServerAddress(): string {
  try {
    const raw = localStorage.getItem('status')
    if (raw) {
      const status = JSON.parse(raw)
      if (status.server_address) return status.server_address
    }
  } catch {
    /* empty */
  }
  return window.location.origin
}

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  tokenKey: string
  tokenId?: number
}

export function CCSwitchDialog(props: Props) {
  const { t } = useTranslation()
  const [app, setApp] = useState<AppType>('claude')
  const [name, setName] = useState<string>(APP_CONFIGS.claude.defaultName)
  const [models, setModels] = useState<Record<string, string>>({})
  const [savingBalanceAccess, setSavingBalanceAccess] = useState(false)
  const balanceAccess = useQuery({
    queryKey: ['cc-switch-balance-access', props.tokenId],
    queryFn: () => {
      if (props.tokenId === undefined) throw new Error('Missing API key ID')
      return getApiKey(props.tokenId)
    },
    enabled: props.open && props.tokenId !== undefined,
    staleTime: 0,
  })
  const canReadBalance = balanceAccess.data?.data?.account_balance_read === true
  const changeBalanceAccess = async (enabled: boolean) => {
    if (props.tokenId === undefined) return
    setSavingBalanceAccess(true)
    try {
      const result = await setAccountBalanceAccess(props.tokenId, enabled)
      if (!result.success) throw new Error('Balance access update failed')
      await balanceAccess.refetch()
    } catch {
      toast.error(t('Unable to update account balance access'))
    } finally {
      setSavingBalanceAccess(false)
    }
  }

  const { data: modelsData } = useQuery({
    queryKey: ['user-models-ccswitch'],
    queryFn: getUserModels,
    enabled: props.open,
    staleTime: 5 * 60 * 1000,
  })

  const modelOptions = useMemo(() => {
    const items = modelsData?.data ?? []
    return items.map((m) => ({ value: m, label: m }))
  }, [modelsData?.data])

  useEffect(() => {
    if (props.open) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setModels({})

      setApp('claude')

      setName(APP_CONFIGS.claude.defaultName)
    }
  }, [props.open])

  const currentConfig = APP_CONFIGS[app]

  const handleAppChange = (val: string) => {
    const appVal = val as AppType
    setApp(appVal)
    setName(APP_CONFIGS[appVal].defaultName)
    setModels({})
  }

  const handleSubmit = async () => {
    if (!models.model) {
      toast.warning(t('Please select a primary model'))
      return
    }
    const key = props.tokenKey.startsWith('sk-')
      ? props.tokenKey
      : `sk-${props.tokenKey}`
    const serverAddress = getServerAddress()
    const endpoint = app === 'codex' ? `${serverAddress}/v1` : serverAddress
    const url = validatedExternalUrl(
      buildCCSwitchProviderURL({
        app,
        name,
        endpoint,
        apiKey: key,
        models,
        homepage: serverAddress,
        enabled: true,
        accountBalanceURL: canReadBalance
          ? `${serverAddress.replace(/\/+$/, '')}/v1/balance`
          : undefined,
      }),
      {
        protocols: ['ccswitch:'],
        origins: 'any',
        hosts: ['v1'],
        paths: { exact: ['/import'] },
        allowHash: false,
      }
    )
    if (!url) {
      toast.error(t('Unable to open CC Switch'))
      return
    }
    // Invariant: url is ccswitch://v1/import with no credentials or fragment.
    // pi-lens-ignore: ts-open-redirect, no-open-redirect
    const opened = await openExternalUrl(url)
    if (!opened) {
      toast.error(t('Unable to open CC Switch'))
      return
    }
    props.onOpenChange(false)
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Import to CC Switch')}
      contentClassName='sm:max-w-md'
      contentHeight='auto'
      bodyClassName={
        currentConfig.modelFields.length === 1 ? 'space-y-4 pb-52' : 'space-y-4'
      }
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button
            onClick={handleSubmit}
            disabled={savingBalanceAccess || balanceAccess.isFetching}
          >
            {t('Open CC Switch')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        {props.tokenId !== undefined && (
          <div className='space-y-2'>
            <div className='flex items-center justify-between gap-4'>
              <Label htmlFor='cc-switch-balance-access'>
                {t('Allow this key to read account balance')}
              </Label>
              <Switch
                id='cc-switch-balance-access'
                checked={canReadBalance}
                disabled={
                  savingBalanceAccess ||
                  balanceAccess.isFetching ||
                  !balanceAccess.data?.success
                }
                onCheckedChange={changeBalanceAccess}
              />
            </div>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Anyone holding this key can read your wallet balance. Turn off to revoke access. Subscription and key quotas are separate.'
              )}
            </p>
          </div>
        )}
        <div className='space-y-2'>
          <Label>{t('Application')}</Label>
          <RadioGroup
            value={app}
            onValueChange={handleAppChange}
            className='flex gap-4'
          >
            {(
              Object.entries(APP_CONFIGS) as [
                AppType,
                (typeof APP_CONFIGS)[AppType],
              ][]
            ).map(([key, cfg]) => (
              <div key={key} className='flex items-center gap-2'>
                <RadioGroupItem value={key} id={`app-${key}`} />
                <Label htmlFor={`app-${key}`} className='cursor-pointer'>
                  {cfg.label}
                </Label>
              </div>
            ))}
          </RadioGroup>
        </div>

        <div className='space-y-2'>
          <Label>{t('Name')}</Label>
          <ComboboxInput
            options={[]}
            value={name}
            onValueChange={setName}
            placeholder={currentConfig.defaultName}
            emptyText=''
            allowCustomValue
          />
        </div>

        {currentConfig.modelFields.map((field) => (
          <div key={field.key} className='space-y-2'>
            <Label>
              {t(field.labelKey)}
              {field.required && (
                <span className='text-destructive ml-0.5'>*</span>
              )}
            </Label>
            <ComboboxInput
              options={modelOptions}
              value={models[field.key] || ''}
              onValueChange={(v) =>
                setModels((prev) => ({ ...prev, [field.key]: v }))
              }
              placeholder={t('Select or enter model name')}
              emptyText={t('No models found')}
            />
          </div>
        ))}
      </div>
    </Dialog>
  )
}
