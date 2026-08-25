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
import { KeyRound } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { buildCCSwitchProviderURL } from '@/lib/cc-switch-deep-link'

import type { AssistantCreateKeyAction } from './api'
import {
  AssistantAccessNotice,
  AssistantConnectionDetails,
  AssistantCreatedKeyView,
  AssistantKeyDialogs,
  AssistantKeyForm,
} from './assistant-key-ui'
import { useAssistantKeyCreation } from './use-assistant-key-creation'

function selectedConnectionModel(
  developerAccessGranted: boolean,
  availableModels: string[],
  selectedModel: string
) {
  if (!developerAccessGranted || availableModels.length === 0) {
    return '<MODEL_ID>'
  }
  if (availableModels.includes(selectedModel)) return selectedModel
  return availableModels[0]
}

export function AssistantKeyTool(props: {
  developerAccessGranted: boolean
  baseUrl: string
  availableModels: string[]
  modelsLoading: boolean
  confirmationAction?: AssistantCreateKeyAction | null
  autoConfirm?: boolean
  onKeyCreated?: () => void
  onKeyPreparationInvalid?: () => void
  onContinueSetup: () => void
}) {
  const { t } = useTranslation()
  const [selectedModel, setSelectedModel] = useState('')
  const [detailsOpen, setDetailsOpen] = useState(true)
  const creation = useAssistantKeyCreation({
    developerAccessGranted: props.developerAccessGranted,
    confirmationAction: props.confirmationAction,
    autoConfirm: props.autoConfirm,
    onKeyCreated: props.onKeyCreated,
    onKeyPreparationInvalid: props.onKeyPreparationInvalid,
  })
  const model = selectedConnectionModel(
    props.developerAccessGranted,
    props.availableModels,
    selectedModel
  )

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

  if (creation.state.phase.kind === 'created') {
    return (
      <AssistantCreatedKeyView
        phase={creation.state.phase}
        baseUrl={props.baseUrl}
        model={model}
        onContinueSetup={props.onContinueSetup}
        onImportToCCSwitch={importToCCSwitch}
        t={t}
      />
    )
  }

  return (
    <>
      <div className='grid gap-4'>
        <div className='space-y-1'>
          <h3 className='flex items-center gap-2 text-base font-semibold'>
            <KeyRound className='size-4' />
            {t('Create and connect an API key')}
          </h3>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Use the Base URL, an exact model ID, and your private API key as three separate values.'
            )}
          </p>
        </div>

        <AssistantConnectionDetails
          baseUrl={props.baseUrl}
          model={model}
          availableModels={props.availableModels}
          modelsLoading={props.modelsLoading}
          developerAccessGranted={props.developerAccessGranted}
          selectedModel={selectedModel}
          onSelectedModelChange={setSelectedModel}
          detailsOpen={detailsOpen}
          onDetailsOpenChange={setDetailsOpen}
          t={t}
        />

        {!props.developerAccessGranted ? (
          <AssistantAccessNotice
            modelsLoading={props.modelsLoading}
            availableModels={props.availableModels}
            t={t}
          />
        ) : (
          <AssistantKeyForm
            state={creation.state}
            groups={creation.groups}
            selectedGroup={creation.selectedGroup}
            groupsLoading={creation.groupsLoading}
            groupsError={creation.groupsError}
            onNameChange={creation.setName}
            onGroupChange={creation.setGroup}
            onReview={() => void creation.review()}
            t={t}
          />
        )}
      </div>

      <AssistantKeyDialogs
        phase={creation.state.phase}
        onTwoFactorCodeChange={creation.setTwoFactorCode}
        onConfirm={() => void creation.confirm()}
        onAcknowledgeWarning={creation.acknowledgeWarning}
        onDismiss={creation.dismiss}
        t={t}
      />
    </>
  )
}
