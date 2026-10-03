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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState, useId, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { ComboboxInput } from '@/components/ui/combobox-input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Switch } from '@/components/ui/switch'
import { buildCCSwitchProviderURL } from '@/lib/cc-switch-deep-link'
import { openExternalUrl } from '@/lib/external-navigation'
import { validatedExternalUrl } from '@/lib/validated-external-url'

import { getApiKey, setAccountBalanceAccess } from '../../api'
import { getKeyModels } from '../../lib/key-models'

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
  // A changed secret (even for the same token ID) starts a fresh component and selection.
  // The credential remains local; only a nonsecret useId enters the catalogue query key.
  const [credential, setCredential] = useState({
    tokenKey: props.tokenKey,
    tokenId: props.tokenId,
    revision: 0,
  })
  if (
    credential.tokenKey !== props.tokenKey ||
    credential.tokenId !== props.tokenId
  ) {
    setCredential({
      tokenKey: props.tokenKey,
      tokenId: props.tokenId,
      revision: credential.revision + 1,
    })
    return null
  }
  return props.open ? (
    <CCSwitchForm key={credential.revision} {...props} />
  ) : null
}

function CCSwitchForm(props: Props) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const catalogueId = useId()
  const catalogueQueryKey = ['user-models-ccswitch', catalogueId]
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

  const catalogue = useQuery({
    queryKey: catalogueQueryKey,
    queryFn: ({ signal }) => getKeyModels(props.tokenKey, signal),
    enabled: Boolean(props.tokenKey),
    staleTime: 0,
    gcTime: 0,
    retry: false,
  })
  const catalogueReady = catalogue.isSuccess && !catalogue.isFetching

  const modelOptions = useMemo(() => {
    const items = catalogueReady ? (catalogue.data ?? []) : []
    return items.map((m) => ({ value: m, label: m }))
  }, [catalogueReady, catalogue.data])
  const selectedModels = Object.fromEntries(
    Object.entries(models).filter(([, id]) =>
      modelOptions.some((option) => option.value === id)
    )
  )
  const canExport = catalogueReady && Boolean(selectedModels.model)

  const currentConfig = APP_CONFIGS[app]

  const handleAppChange = (val: string) => {
    const appVal = val as AppType
    setApp(appVal)
    setName(APP_CONFIGS[appVal].defaultName)
    setModels({})
  }

  const handleSubmit = async () => {
    // Invalidation changes query state before React's batched notification renders.
    // Recheck that state on click so a stale visible selection cannot slip through.
    const currentCatalogue =
      queryClient.getQueryState<string[]>(catalogueQueryKey)
    if (
      !canExport ||
      currentCatalogue?.status !== 'success' ||
      currentCatalogue.fetchStatus !== 'idle' ||
      Object.values(selectedModels).some(
        (id) => !currentCatalogue.data?.includes(id)
      )
    ) {
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
        models: selectedModels,
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
            disabled={
              !canExport || savingBalanceAccess || balanceAccess.isFetching
            }
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

        {!props.tokenKey || catalogue.isError ? (
          <div className='space-y-2' role='alert'>
            <p className='text-sm'>{t('Failed to fetch models')}</p>
            {props.tokenKey && (
              <Button
                type='button'
                variant='outline'
                onClick={() => void catalogue.refetch()}
              >
                {t('Retry')}
              </Button>
            )}
          </div>
        ) : catalogue.isPending || catalogue.isFetching ? (
          <p className='text-muted-foreground text-sm' role='status'>
            {t('Loading')}
          </p>
        ) : modelOptions.length === 0 ? (
          <p className='text-muted-foreground text-sm'>
            {t('No models found')}
          </p>
        ) : null}
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
              value={selectedModels[field.key] || ''}
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
