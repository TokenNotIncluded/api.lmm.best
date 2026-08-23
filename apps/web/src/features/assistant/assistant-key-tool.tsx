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
import {
  ArrowRight01Icon,
  Key01Icon,
  Loading03Icon,
  ShieldKeyIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { type ReactNode, useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
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
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Separator } from '@/components/ui/separator'
import { getUserGroups } from '@/lib/api'
import { buildCCSwitchProviderURL } from '@/lib/cc-switch-deep-link'

import {
  createAssistantDefaultKey,
  type AssistantCreateKeyAction,
  type AssistantCreatedKey,
} from './api'
import { AssistantPrivateCard } from './assistant-private-card'

function ConnectionValue(props: { label: string; value: string }) {
  return (
    <div className='flex items-center justify-between gap-2 py-2'>
      <span className='text-muted-foreground shrink-0 text-xs'>
        {props.label}
      </span>
      <div className='flex min-w-0 items-center gap-1.5'>
        <code className='min-w-0 flex-1 truncate text-xs'>{props.value}</code>
        <CopyButton value={props.value} size='sm' />
      </div>
    </div>
  )
}

function ConnectionDetails(props: {
  baseUrl: string
  model: string
  group?: string
}) {
  const { t } = useTranslation()
  return (
    <div className='rounded-lg border px-3'>
      <ConnectionValue label={t('Base URL')} value={props.baseUrl} />
      <Separator />
      <ConnectionValue label={t('Model ID')} value={props.model} />
      {props.group ? (
        <>
          <Separator />
          <ConnectionValue label={t('Group')} value={props.group} />
        </>
      ) : null}
    </div>
  )
}

export function AssistantKeyTool(props: {
  baseUrl: string
  availableModels: string[]
  modelsLoading?: boolean
  developerAccessGranted: boolean
  confirmationAction?: AssistantCreateKeyAction | null
  autoConfirm?: boolean
  onKeyCreated?: () => void
  onContinueSetup: () => void
}) {
  const { t } = useTranslation()
  const [name, setName] = useState(
    props.confirmationAction?.name || t('AI assistant key')
  )
  const [group, setGroup] = useState(props.confirmationAction?.group || 'auto')
  const [selectedModel, setSelectedModel] = useState('')
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [creating, setCreating] = useState(false)
  const [created, setCreated] = useState<AssistantCreatedKey | null>(null)

  useEffect(() => {
    if (!props.confirmationAction) return
    setName(props.confirmationAction.name)
    setGroup(props.confirmationAction.group)
    setConfirmOpen(false)
    setCreated(null)
  }, [props.confirmationAction])
  let model = '<MODEL_ID>'
  if (props.developerAccessGranted && props.availableModels.length > 0) {
    model = props.availableModels.includes(selectedModel)
      ? selectedModel
      : props.availableModels[0]
  }
  const groupsQuery = useQuery({
    queryKey: ['assistant-user-groups'],
    queryFn: getUserGroups,
    enabled: props.developerAccessGranted,
    staleTime: 0,
    refetchOnMount: 'always',
    refetchOnWindowFocus: true,
    retry: false,
  })
  const groups = Object.keys(groupsQuery.data?.data ?? {})
  const groupOptions = groups.includes(group) ? groups : [group, ...groups]

  useEffect(() => {
    if (groupsQuery.isLoading || group !== 'auto') return
    if (!groups.includes('auto') && groups.length > 0) setGroup(groups[0])
  }, [group, groups, groupsQuery.isLoading])

  let modelOptions: ReactNode = (
    <NativeSelectOption value='<MODEL_ID>'>
      {t('No available models')}
    </NativeSelectOption>
  )
  if (props.modelsLoading) {
    modelOptions = (
      <NativeSelectOption value='<MODEL_ID>'>
        {t('Loading current models...')}
      </NativeSelectOption>
    )
  } else if (props.availableModels.length > 0) {
    modelOptions = props.availableModels.map((item) => (
      <NativeSelectOption key={item} value={item}>
        {item}
      </NativeSelectOption>
    ))
  }

  const confirmationAction = props.confirmationAction
  const confirmationToken = confirmationAction?.confirmation_token
  const onKeyCreated = props.onKeyCreated
  const autoConfirm = props.autoConfirm
  const createKey = useCallback(async () => {
    if (creating) return
    setCreating(true)
    try {
      const result = await createAssistantDefaultKey(
        name.trim(),
        group.trim(),
        confirmationToken
      )
      setCreated(result)
      onKeyCreated?.()
      setConfirmOpen(false)
      toast.success(t('API key created'))
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Unable to create API key')
      )
    } finally {
      setCreating(false)
    }
  }, [confirmationToken, creating, group, name, onKeyCreated, t])

  useEffect(() => {
    if (!autoConfirm || !confirmationAction || created) return
    void createKey()
  }, [autoConfirm, confirmationAction, createKey, created])

  const importToCCSwitch = (apiKey: string) => {
    if (model === '<MODEL_ID>' || typeof window === 'undefined') return
    const serviceRoot = props.baseUrl
      .replace(/\/v1\/?$/, '')
      .replace(/\/+$/, '')
    const normalizedKey = apiKey.startsWith('sk-') ? apiKey : `sk-${apiKey}`
    const url = buildCCSwitchProviderURL({
      app: 'claude',
      name: 'LMM',
      endpoint: serviceRoot,
      apiKey: normalizedKey,
      models: { model },
      homepage: serviceRoot,
      enabled: true,
    })
    window.open(url, '_blank')
  }

  if (created) {
    return (
      <Card size='sm' className='border-success/40 bg-success/5'>
        <CardHeader>
          <CardTitle className='flex items-center gap-2'>
            <HugeiconsIcon
              icon={ShieldKeyIcon}
              className='text-success size-4'
              strokeWidth={2}
              aria-hidden='true'
            />
            {t('API key created')}
          </CardTitle>
          <CardDescription>
            {t(
              'The credential is shown only after confirmation and is never added to chat history.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className='grid gap-3'>
          <ConnectionDetails
            baseUrl={props.baseUrl}
            model={model}
            group={created.group}
          />
          <AssistantPrivateCard
            card={created.card}
            onContinue={props.onContinueSetup}
            onImportToCCSwitch={
              model === '<MODEL_ID>' ? undefined : importToCCSwitch
            }
          />
        </CardContent>
      </Card>
    )
  }

  return (
    <>
      <Card size='sm'>
        <CardHeader>
          <CardTitle>
            {props.developerAccessGranted
              ? t('Create a default API key')
              : t('Connection details')}
          </CardTitle>
          <CardDescription>
            {t(
              'Base URL tells your client where to connect, Model ID selects the model, and an API key is the secret credential sent with each request.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className='grid gap-3'>
          {props.developerAccessGranted ? (
            <div className='grid gap-1.5'>
              <Label htmlFor='assistant-key-model'>{t('Model ID')}</Label>
              <NativeSelect
                id='assistant-key-model'
                value={model}
                disabled={
                  props.modelsLoading || props.availableModels.length === 0
                }
                onChange={(event) => setSelectedModel(event.target.value)}
              >
                {modelOptions}
              </NativeSelect>
            </div>
          ) : null}
          <ConnectionDetails baseUrl={props.baseUrl} model={model} />
          {props.developerAccessGranted ? (
            <>
              <p className='text-muted-foreground text-xs leading-5'>
                {t(
                  'This creates one unlimited, non-expiring key. Your wallet balance still limits actual usage.'
                )}
              </p>
              <div className='grid gap-1.5'>
                <Label htmlFor='assistant-key-name'>{t('Key name')}</Label>
                <Input
                  id='assistant-key-name'
                  value={name}
                  maxLength={50}
                  autoComplete='off'
                  onChange={(event) => setName(event.target.value)}
                />
              </div>
              <div className='grid gap-1.5'>
                <Label htmlFor='assistant-key-group'>{t('Key group')}</Label>
                <NativeSelect
                  id='assistant-key-group'
                  value={group}
                  disabled={groupsQuery.isLoading || groupOptions.length === 0}
                  onChange={(event) => setGroup(event.target.value)}
                >
                  {groupOptions.map((item) => (
                    <NativeSelectOption key={item} value={item}>
                      {item}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
                <p className='text-muted-foreground text-xs'>
                  {groupsQuery.isLoading
                    ? t('Loading available groups...')
                    : t('The group controls routing and pricing for this key.')}
                </p>
              </div>
              <Button
                type='button'
                onClick={() => setConfirmOpen(true)}
                disabled={!name.trim() || !group.trim()}
              >
                <HugeiconsIcon
                  icon={Key01Icon}
                  strokeWidth={2}
                  data-icon='inline-start'
                  aria-hidden='true'
                />
                {t('Review key creation')}
              </Button>
            </>
          ) : (
            <div className='grid gap-3 rounded-lg border border-dashed p-3'>
              <div>
                <p className='text-xs font-medium'>
                  {t('API key creation requires L1')}
                </p>
                <p className='text-muted-foreground mt-1 text-xs leading-5'>
                  {t(
                    'L0 access is restricted. Ask the assistant to prepare an L1 recommendation; after automatic review approval or human fallback, return here to create a key.'
                  )}
                </p>
              </div>
              <Button
                variant='outline'
                size='sm'
                render={<Link to='/getting-started' />}
              >
                {t('View onboarding status')}
                <HugeiconsIcon
                  icon={ArrowRight01Icon}
                  strokeWidth={2}
                  data-icon='inline-end'
                  aria-hidden='true'
                />
              </Button>
            </div>
          )}
        </CardContent>
      </Card>

      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Create this API key?')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                'A new credential named “{{name}}” will be added to your account. Confirm only if you requested this action.',
                { name: name.trim() }
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={creating}>
              {t('Cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={() => void createKey()}
              disabled={creating}
            >
              {creating ? (
                <HugeiconsIcon
                  icon={Loading03Icon}
                  className='animate-spin'
                  strokeWidth={2}
                  aria-hidden='true'
                />
              ) : null}
              {t('Confirm and create')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
