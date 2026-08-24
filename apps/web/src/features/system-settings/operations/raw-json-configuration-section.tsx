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
  AlertCircle,
  CheckCircle2,
  Download,
  FileUp,
  RefreshCw,
  Sparkles,
} from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type ChangeEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  Alert,
  AlertAction,
  AlertDescription,
  AlertTitle,
} from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { requestAssistantOpen } from '@/features/assistant/assistant-events'
import { SystemJsonCodeEditor } from '@/features/system-settings/components/system-json-code-editor'
import type { SystemJsonConfigurationKey } from '@/features/system-settings/components/system-json-configurations'

import { updateSystemOptions, validateSystemOptions } from '../api'
import { FormNavigationGuard } from '../components/form-navigation-guard'
import { SettingsPageActionsPortal } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useSystemOptions } from '../hooks/use-system-options'
import {
  hasUnsavedJsonDraft,
  shouldApplyRawJsonServerValue,
} from './raw-json-draft-state'

type RawJsonDescriptor = {
  key: SystemJsonConfigurationKey
  label: string
}

// This is an explicit allowlist: the raw editor must never expose secrets or
// arbitrary option keys. Examples and field contracts come from the same
// reviewed registry used by inline settings editors.
export const RAW_JSON_DESCRIPTORS = [
  { key: 'group_ratio_setting.group_warnings', label: 'Group warnings' },
  { key: 'GroupRatio', label: 'Group ratios' },
  { key: 'GroupGroupRatio', label: 'Inter-group ratios' },
  { key: 'TopupGroupRatio', label: 'Top-up group ratios' },
  { key: 'UserUsableGroups', label: 'Selectable groups' },
  { key: 'AutoGroups', label: 'Auto group order' },
  {
    key: 'group_ratio_setting.group_special_usable_group',
    label: 'Special usable groups',
  },
  {
    key: 'AssistantReviewGroupPolicies',
    label: 'AI review group policies',
  },
  { key: 'AssistantSkillFiles', label: 'AI skill files' },
  {
    key: 'global.thinking_model_blacklist',
    label: 'Thinking model blacklist',
  },
  {
    key: 'global.chat_completions_to_responses_policy',
    label: 'Chat to Responses policy',
  },
  { key: 'gemini.safety_settings', label: 'Gemini safety settings' },
  { key: 'gemini.version_settings', label: 'Gemini version settings' },
  {
    key: 'gemini.supported_imagine_models',
    label: 'Gemini Imagine models',
  },
  { key: 'claude.model_headers_settings', label: 'Claude model headers' },
  { key: 'claude.default_max_tokens', label: 'Claude default max tokens' },
  { key: 'billing_setting.billing_mode', label: 'Billing modes' },
  { key: 'billing_setting.billing_expr', label: 'Billing expressions' },
  { key: 'tool_price_setting.prices', label: 'Tool prices' },
  { key: 'channel_affinity_setting.rules', label: 'Channel affinity rules' },
  { key: 'AdvancedSecurityRules', label: 'Advanced security rules' },
  {
    key: 'dynamic_pricing_setting.channel_costs',
    label: 'Dynamic pricing channel costs',
  },
  {
    key: 'dynamic_pricing_setting.per_model',
    label: 'Dynamic pricing model overrides',
  },
  { key: 'console_setting.api_info', label: 'API information' },
  { key: 'console_setting.announcements', label: 'Announcements' },
  { key: 'console_setting.faq', label: 'FAQ' },
  {
    key: 'payment_setting.amount_options',
    label: 'Top-up amount options',
  },
  { key: 'payment_setting.amount_discount', label: 'Top-up discounts' },
  { key: 'PayMethods', label: 'Payment methods' },
  { key: 'CreemProducts', label: 'Creem products' },
  { key: 'WaffoPayMethods', label: 'Waffo payment methods' },
] as const satisfies readonly RawJsonDescriptor[]

type RawJsonConfigurationKey = (typeof RAW_JSON_DESCRIPTORS)[number]['key']

const descriptorMap = new Map(
  RAW_JSON_DESCRIPTORS.map((descriptor) => [descriptor.key, descriptor])
)

function isRawJsonConfigurationKey(
  value: string
): value is RawJsonConfigurationKey {
  return RAW_JSON_DESCRIPTORS.some((descriptor) => descriptor.key === value)
}

function formatJson(value: string) {
  if (!value.trim()) return ''
  try {
    return JSON.stringify(JSON.parse(value), null, 2)
  } catch {
    return value
  }
}

function parseImport(value: string): string {
  try {
    return JSON.stringify(JSON.parse(value), null, 2)
  } catch (error) {
    throw error instanceof Error ? error : new Error('Invalid JSON')
  }
}

function downloadJson(filename: string, value: string) {
  const blob = new Blob([value], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  anchor.click()
  URL.revokeObjectURL(url)
}

export function RawJsonConfigurationSection() {
  const { t } = useTranslation()
  const optionsQuery = useSystemOptions()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const loadedKeyRef = useRef<RawJsonConfigurationKey | null>(null)
  const [selectedKey, setSelectedKey] = useState<RawJsonConfigurationKey>(
    'group_ratio_setting.group_warnings'
  )
  const [editorValue, setEditorValue] = useState('')
  const [baselineValue, setBaselineValue] = useState('')
  const [validationFeedback, setValidationFeedback] = useState<{
    kind: 'success' | 'error'
    message: string
  } | null>(null)
  const [pendingReplacement, setPendingReplacement] = useState<
    | { kind: 'configuration'; key: RawJsonConfigurationKey }
    | { kind: 'import'; value: string }
    | null
  >(null)
  const [isValidating, setIsValidating] = useState(false)
  const [isSaving, setIsSaving] = useState(false)
  const editorValueRef = useRef(editorValue)
  const baselineValueRef = useRef(baselineValue)
  const descriptor = descriptorMap.get(selectedKey)
  const dirty = hasUnsavedJsonDraft(editorValue, baselineValue)
  const isBusy = isValidating || isSaving
  const controlsDisabled =
    optionsQuery.isLoading || optionsQuery.isError || !descriptor || isBusy
  const availableDescriptors = useMemo(
    () =>
      RAW_JSON_DESCRIPTORS.filter((item) =>
        optionsQuery.data?.data?.some((option) => option.key === item.key)
      ),
    [optionsQuery.data?.data]
  )
  const selectItems = useMemo(
    () =>
      availableDescriptors.map((item) => ({
        value: item.key,
        label: `${t(item.label)} · ${item.key}`,
      })),
    [availableDescriptors, t]
  )

  editorValueRef.current = editorValue
  baselineValueRef.current = baselineValue

  useEffect(() => {
    if (
      !dirty &&
      availableDescriptors.length > 0 &&
      !availableDescriptors.some((item) => item.key === selectedKey)
    ) {
      setSelectedKey(availableDescriptors[0].key)
    }
  }, [availableDescriptors, dirty, selectedKey])

  useEffect(() => {
    const raw =
      optionsQuery.data?.data?.find((option) => option.key === selectedKey)
        ?.value ?? ''
    const formatted = formatJson(raw)

    if (
      shouldApplyRawJsonServerValue({
        loadedKey: loadedKeyRef.current,
        selectedKey,
        editorValue: editorValueRef.current,
        baselineValue: baselineValueRef.current,
      })
    ) {
      setEditorValue(formatted)
      setBaselineValue(formatted)
      setValidationFeedback(null)
    }
    loadedKeyRef.current = selectedKey
  }, [optionsQuery.data?.data, selectedKey])

  const replaceEditorValue = (value: string) => {
    setEditorValue(value)
    setValidationFeedback(null)
  }

  const requestConfigurationChange = (value: string | null) => {
    if (!value || !isRawJsonConfigurationKey(value) || value === selectedKey) {
      return
    }
    if (dirty) {
      setPendingReplacement({ kind: 'configuration', key: value })
      return
    }
    setSelectedKey(value)
  }

  const confirmPendingReplacement = () => {
    if (!pendingReplacement) return

    if (pendingReplacement.kind === 'configuration') {
      setSelectedKey(pendingReplacement.key)
    } else {
      replaceEditorValue(pendingReplacement.value)
    }
    setPendingReplacement(null)
  }

  const validate = async () => {
    if (!selectedKey || !editorValue.trim()) {
      setValidationFeedback({
        kind: 'error',
        message: t('A JSON value is required.'),
      })
      return false
    }
    try {
      JSON.parse(editorValue)
    } catch {
      setValidationFeedback({
        kind: 'error',
        message: t('Invalid JSON. Please fix the syntax first.'),
      })
      return false
    }
    setIsValidating(true)
    try {
      const response = await validateSystemOptions({
        [selectedKey]: editorValue,
      })
      if (!response.success) {
        setValidationFeedback({
          kind: 'error',
          message: response.message || t('Configuration is invalid.'),
        })
        return false
      }
      setValidationFeedback({
        kind: 'success',
        message: t('Configuration validated.'),
      })
      return true
    } catch (error) {
      setValidationFeedback({
        kind: 'error',
        message:
          error instanceof Error
            ? error.message
            : t('Configuration check failed.'),
      })
      return false
    } finally {
      setIsValidating(false)
    }
  }

  const save = async () => {
    if (!(await validate())) return
    setIsSaving(true)
    try {
      const response = await updateSystemOptions({
        [selectedKey]: editorValue,
      })
      if (!response.success) {
        throw new Error(response.message || t('Failed to update setting'))
      }
      setBaselineValue(editorValue)
      toast.success(t('Setting updated successfully'))
      await optionsQuery.refetch()
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Failed to update setting')
      )
    } finally {
      setIsSaving(false)
    }
  }

  const handleImport = (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    event.target.value = ''
    if (!file) return

    void file
      .text()
      .then((text) => {
        try {
          const importedValue = parseImport(text)
          if (
            hasUnsavedJsonDraft(
              editorValueRef.current,
              baselineValueRef.current
            )
          ) {
            setPendingReplacement({ kind: 'import', value: importedValue })
            return
          }
          replaceEditorValue(importedValue)
        } catch {
          setValidationFeedback({
            kind: 'error',
            message: t('The imported file is not valid JSON.'),
          })
        }
      })
      .catch((error: unknown) => {
        setValidationFeedback({
          kind: 'error',
          message:
            error instanceof Error
              ? error.message
              : t('Configuration check failed.'),
        })
      })
  }

  const askAssistant = () => {
    const safeValue = editorValue.slice(0, 12_000)
    requestAssistantOpen(
      'service',
      `请帮我编辑管理员配置 ${selectedKey}。先读取当前服务端配置并解释字段，再提出最小变更预览；未经我确认不要写入。当前编辑器内容如下：\n\n${safeValue}`
    )
  }

  return (
    <SettingsSection title={t('Raw JSON Configuration')}>
      <FormNavigationGuard when={dirty} />
      <ConfirmDialog
        open={Boolean(pendingReplacement)}
        onOpenChange={(open) => {
          if (!open) setPendingReplacement(null)
        }}
        title={t('Discard unsaved JSON changes?')}
        desc={t(
          'Continuing will replace the unsaved JSON currently in the editor.'
        )}
        confirmText={t('Replace')}
        destructive
        handleConfirm={confirmPendingReplacement}
      />
      <div className='space-y-4'>
        <p
          id='raw-json-configuration-description'
          className='text-muted-foreground text-sm'
        >
          {t(
            'Edit one safe-listed JSON setting at a time. Imports are checked locally and by the server before any write.'
          )}
        </p>
        {optionsQuery.isError && (
          <Alert variant='destructive'>
            <AlertCircle />
            <AlertTitle>{t('Failed to load')}</AlertTitle>
            <AlertDescription>
              {optionsQuery.error instanceof Error
                ? optionsQuery.error.message
                : t('Configuration check failed.')}
            </AlertDescription>
            <AlertAction>
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={() => void optionsQuery.refetch()}
                disabled={optionsQuery.isFetching}
              >
                <RefreshCw
                  className={optionsQuery.isFetching ? 'animate-spin' : ''}
                  aria-hidden='true'
                />
                {t('Retry')}
              </Button>
            </AlertAction>
          </Alert>
        )}
        <div className='flex flex-wrap items-end gap-3'>
          <div className='min-w-0 flex-1 basis-full space-y-2 sm:basis-64'>
            <Label htmlFor='raw-json-configuration-key'>
              {t('Configuration key')}
            </Label>
            <Select
              items={selectItems}
              value={selectedKey}
              onValueChange={requestConfigurationChange}
              disabled={controlsDisabled || availableDescriptors.length === 0}
            >
              <SelectTrigger
                id='raw-json-configuration-key'
                aria-describedby='raw-json-configuration-description'
                className='w-full max-w-full'
              >
                <SelectValue placeholder={t('Select')} />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  {availableDescriptors.map((item) => (
                    <SelectItem key={item.key} value={item.key}>
                      {t(item.label)} · {item.key}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>
          <input
            ref={fileInputRef}
            type='file'
            accept='application/json,.json'
            className='hidden'
            onChange={handleImport}
          />
          <Button
            type='button'
            variant='outline'
            onClick={() => fileInputRef.current?.click()}
            disabled={controlsDisabled}
          >
            <FileUp data-icon='inline-start' />
            {t('Import JSON')}
          </Button>
          <Button
            type='button'
            variant='outline'
            onClick={() => downloadJson(`${selectedKey}.json`, editorValue)}
            disabled={!editorValue.trim()}
          >
            <Download data-icon='inline-start' />
            {t('Export JSON')}
          </Button>
          <Button
            type='button'
            variant='outline'
            onClick={askAssistant}
            disabled={isBusy}
          >
            <Sparkles data-icon='inline-start' />
            {t('Ask AI to edit')}
          </Button>
        </div>
        <SystemJsonCodeEditor
          configurationKey={selectedKey}
          specificationDefaultOpen
          value={editorValue}
          onChange={replaceEditorValue}
          disabled={controlsDisabled}
          heightClassName='h-80 min-h-80 max-h-80 sm:h-[28rem] sm:min-h-[28rem] sm:max-h-[28rem]'
          ariaLabel={`${t('JSON')} — ${descriptor ? t(descriptor.label) : selectedKey}`}
        />
        {validationFeedback && (
          <Alert
            variant={
              validationFeedback.kind === 'success' ? 'default' : 'destructive'
            }
          >
            {validationFeedback.kind === 'success' ? (
              <CheckCircle2 />
            ) : (
              <AlertCircle />
            )}
            <AlertTitle>{t('Configuration check')}</AlertTitle>
            <AlertDescription>{validationFeedback.message}</AlertDescription>
          </Alert>
        )}
        <SettingsPageActionsPortal>
          <Button
            type='button'
            variant='outline'
            onClick={() => void validate()}
            disabled={controlsDisabled || !dirty}
          >
            {isValidating ? t('Checking...') : t('Validate configuration')}
          </Button>
          <Button
            type='button'
            onClick={() => void save()}
            disabled={controlsDisabled || !dirty}
          >
            {isSaving ? t('Saving...') : t('Save Changes')}
          </Button>
        </SettingsPageActionsPortal>
      </div>
    </SettingsSection>
  )
}
