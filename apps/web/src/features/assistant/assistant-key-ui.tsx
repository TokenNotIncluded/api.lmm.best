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
import type { TFunction } from 'i18next'
import { Check, ChevronDown, ChevronUp, KeyRound } from 'lucide-react'
import type { ReactNode } from 'react'

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
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import type { AssistantSelectableGroup } from './assistant-key-contract'
import type {
  AssistantKeyCreationPhase,
  AssistantKeyCreationState,
} from './assistant-key-creation-machine'
import { AssistantPrivateCard } from './assistant-private-card'

function ConnectionField(props: {
  label: string
  value: string
  description: string
}) {
  return (
    <div className='rounded-lg border px-3 py-2.5'>
      <div className='flex items-start justify-between gap-3'>
        <div className='min-w-0'>
          <p className='text-muted-foreground text-xs'>{props.label}</p>
          <code className='block truncate text-sm font-medium'>
            {props.value}
          </code>
        </div>
        <CopyButton value={props.value} />
      </div>
      <p className='text-muted-foreground mt-2 text-xs leading-relaxed'>
        {props.description}
      </p>
    </div>
  )
}

export function AssistantConnectionDetails(props: {
  baseUrl: string
  model: string
  availableModels: string[]
  modelsLoading: boolean
  developerAccessGranted: boolean
  selectedModel: string
  onSelectedModelChange: (model: string) => void
  detailsOpen: boolean
  onDetailsOpenChange: (open: boolean) => void
  t: TFunction
}) {
  const modelOptions = props.availableModels.map((item) => (
    <NativeSelectOption key={item} value={item}>
      {item}
    </NativeSelectOption>
  ))
  return (
    <div className='grid gap-3'>
      <Button
        type='button'
        variant='outline'
        className='w-full justify-between'
        onClick={() => props.onDetailsOpenChange(!props.detailsOpen)}
      >
        <span>{props.t('Connection details')}</span>
        {props.detailsOpen ? (
          <ChevronUp className='size-4' />
        ) : (
          <ChevronDown className='size-4' />
        )}
      </Button>
      {props.detailsOpen ? (
        <div className='grid gap-2'>
          <ConnectionField
            label={props.t('Base URL')}
            value={props.baseUrl}
            description={props.t(
              'Base URL tells your client where to connect. It is not a model identifier.'
            )}
          />
          <div className='rounded-lg border px-3 py-2.5'>
            <p className='text-muted-foreground text-xs'>
              {props.t('Model ID')}
            </p>
            {props.developerAccessGranted &&
            props.availableModels.length > 0 ? (
              <NativeSelect
                className='mt-1 w-full'
                value={props.model}
                disabled={props.modelsLoading}
                onChange={(event) =>
                  props.onSelectedModelChange(event.target.value)
                }
              >
                {modelOptions}
              </NativeSelect>
            ) : (
              <code className='block truncate text-sm font-medium'>
                {props.model}
              </code>
            )}
            <p className='text-muted-foreground mt-2 text-xs leading-relaxed'>
              {props.t(
                'Model ID is the exact enabled model name from the catalogue.'
              )}
            </p>
          </div>
          <ConnectionField
            label={props.t('API key')}
            value='<API_KEY>'
            description={props.t(
              'Use the private key you create below. It is shown only once.'
            )}
          />
        </div>
      ) : null}
    </div>
  )
}

function groupHelpText(
  t: TFunction,
  loading: boolean,
  failed: boolean,
  groupCount: number
) {
  if (loading) return t('Loading available groups...')
  if (failed) return t('Unable to load selectable key groups. Try again.')
  if (groupCount === 0) {
    return t('No selectable key groups are available for this account.')
  }
  return t('The group controls routing and pricing for this key.')
}

export function AssistantKeyForm(props: {
  state: AssistantKeyCreationState
  groups: AssistantSelectableGroup[]
  selectedGroup: string
  groupsLoading: boolean
  groupsError: boolean
  onNameChange: (name: string) => void
  onGroupChange: (group: string) => void
  onReview: () => void
  t: TFunction
}) {
  const locked = props.state.phase.kind !== 'draft'
  const preparing = props.state.phase.kind === 'preparing'
  return (
    <Card size='sm'>
      <CardHeader>
        <CardTitle>{props.t('Create a default API key')}</CardTitle>
      </CardHeader>
      <CardContent className='grid gap-3'>
        <div className='grid gap-1.5'>
          <Label htmlFor='assistant-key-name'>{props.t('Key name')}</Label>
          <Input
            id='assistant-key-name'
            value={props.state.name}
            maxLength={50}
            autoComplete='off'
            disabled={locked}
            onChange={(event) => props.onNameChange(event.target.value)}
          />
        </div>
        <div className='grid gap-1.5'>
          <Label htmlFor='assistant-key-group'>{props.t('Group')}</Label>
          <NativeSelect
            id='assistant-key-group'
            value={props.selectedGroup}
            disabled={
              locked ||
              props.groupsLoading ||
              props.groupsError ||
              props.groups.length === 0
            }
            onChange={(event) => props.onGroupChange(event.target.value)}
          >
            {props.groups.map((item) => (
              <NativeSelectOption key={item.id} value={item.id}>
                {item.id}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          <p className='text-muted-foreground text-xs'>
            {groupHelpText(
              props.t,
              props.groupsLoading,
              props.groupsError,
              props.groups.length
            )}
          </p>
        </div>
        <div className='flex justify-end'>
          <Button
            type='button'
            onClick={props.onReview}
            disabled={
              preparing ||
              props.groupsLoading ||
              props.groupsError ||
              props.groups.length === 0 ||
              !props.state.name.trim() ||
              !props.selectedGroup
            }
          >
            <KeyRound className='size-4' />
            {preparing
              ? props.t('Preparing key creation...')
              : props.t('Review key creation')}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

function reviewPhase(phase: AssistantKeyCreationPhase) {
  return phase.kind === 'reviewing' || phase.kind === 'confirming'
    ? phase
    : undefined
}

export function AssistantKeyDialogs(props: {
  phase: AssistantKeyCreationPhase
  onTwoFactorCodeChange: (code: string) => void
  onConfirm: () => void
  onAcknowledgeWarning: () => void
  onDismiss: () => void
  t: TFunction
}) {
  const confirmation = reviewPhase(props.phase)
  const warning = props.phase.kind === 'warning' ? props.phase : undefined
  return (
    <>
      <AlertDialog
        open={Boolean(confirmation)}
        onOpenChange={(open) => {
          if (!open) props.onDismiss()
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {props.t('Create this API key?')}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {props.t(
                'A new credential named “{{name}}” will be added to your account. Confirm only if you requested this action.',
                { name: confirmation?.action.name ?? '' }
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <div className='grid gap-1.5'>
            <Label htmlFor='assistant-key-two-factor-code'>
              {props.t('Enter verification code or backup code')}
            </Label>
            <Input
              id='assistant-key-two-factor-code'
              value={confirmation?.twoFactorCode ?? ''}
              disabled={props.phase.kind === 'confirming'}
              autoComplete='one-time-code'
              inputMode='text'
              placeholder={props.t(
                'Only required when two-factor authentication is enabled.'
              )}
              onChange={(event) =>
                props.onTwoFactorCodeChange(event.target.value)
              }
            />
          </div>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={props.phase.kind === 'confirming'}>
              {props.t('Cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={props.onConfirm}
              disabled={props.phase.kind === 'confirming'}
            >
              {props.phase.kind === 'confirming'
                ? props.t('Creating...')
                : props.t('Confirm and create')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog
        open={Boolean(warning)}
        onOpenChange={(open) => {
          if (!open) props.onDismiss()
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {props.t('Confirmation required')}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {warning?.warning.message}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <p className='text-muted-foreground text-sm'>
            {props.t('Confirmation {{current}} of {{total}}', {
              current: Math.min(
                (warning?.confirmations ?? 0) + 1,
                warning?.warning.confirmations ?? 1
              ),
              total: warning?.warning.confirmations ?? 1,
            })}
          </p>
          <AlertDialogFooter>
            <AlertDialogCancel>{props.t('Cancel')}</AlertDialogCancel>
            <Button type='button' onClick={props.onAcknowledgeWarning}>
              {(warning?.confirmations ?? 0) + 1 >=
              (warning?.warning.confirmations ?? 1)
                ? props.t('I understand, continue')
                : props.t('Continue')}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

export function AssistantCreatedKeyView(props: {
  phase: Extract<AssistantKeyCreationPhase, { kind: 'created' }>
  baseUrl: string
  model: string
  onContinueSetup: () => void
  onImportToCCSwitch: (secret: string) => string | null | Promise<string | null>
  t: TFunction
}) {
  return (
    <Card size='sm'>
      <CardHeader>
        <CardTitle className='flex items-center gap-2'>
          <Check className='size-4' />
          {props.t('API key created')}
        </CardTitle>
      </CardHeader>
      <CardContent className='grid gap-3'>
        <p className='text-muted-foreground text-sm'>
          {props.t(
            'This credential is available once. Copy it now, store it safely, then continue setup.'
          )}
        </p>
        <AssistantPrivateCard
          card={props.phase.key.card}
          onContinue={props.onContinueSetup}
          onImportToCCSwitch={props.onImportToCCSwitch}
        />
      </CardContent>
    </Card>
  )
}

export function AssistantAccessNotice(props: {
  modelsLoading: boolean
  availableModels: string[]
  children?: ReactNode
  t: TFunction
}) {
  return (
    <Card size='sm'>
      <CardContent className='grid gap-2'>
        <p className='text-sm font-medium'>
          {props.t('API key creation requires L1')}
        </p>
        <p className='text-muted-foreground text-sm'>
          {props.t(
            'After L1 is enabled, return here to create a private key and continue setup.'
          )}
        </p>
        <p className='text-muted-foreground text-xs'>
          {props.modelsLoading
            ? props.t('Loading available models...')
            : props.t('Available models: {{count}}', {
                count: props.availableModels.length,
              })}
        </p>
        {props.children}
      </CardContent>
    </Card>
  )
}
