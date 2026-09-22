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
import { useQuery } from '@tanstack/react-query'
import {
  ArrowDown,
  ArrowUp,
  FileText,
  Plus,
  RefreshCw,
  Trash2,
} from 'lucide-react'
import { useEffect, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

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
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { INTERFACE_LANGUAGE_OPTIONS } from '@/i18n/languages'
import { api } from '@/lib/api'

import { FormDirtyIndicator } from '../components/form-dirty-indicator'
import { SettingsDisclosure } from '../components/settings-disclosure'
import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOptions } from '../hooks/use-update-option'
import {
  ASSISTANT_REASONING_EFFORTS,
  ASSISTANT_SEARCH_PROVIDERS,
  type AssistantReasoningEffort,
  type AssistantSearchProvider,
} from '../types'
import { safeNumberFieldProps } from '../utils/numeric-field'
import { AssistantL1ReviewSettings } from './assistant-l1-review-settings'
import {
  assistantSettingsSchema,
  type AssistantSettingsFormValues,
} from './assistant-settings-schema'

type AssistantSkillFile = {
  path: string
  content: string
  enabled: boolean
}

const EMPTY_ASSISTANT_MODEL_IDS: string[] = []

// A starter keeps one administrator default for every language and may add an
// explicit translation per interface language. Stored as "default" plus locale
// keys so the server can select copy without client-side guessing.
type ConversationStarterCopy = Record<string, string>
type ConversationStarter = {
  id: string
  label: ConversationStarterCopy
  prompt: ConversationStarterCopy
}

const DEFAULT_CONVERSATION_STARTERS: ConversationStarter[] = [
  {
    id: 'getting_started',
    label: { default: 'Where should I start?' },
    prompt: { default: 'Where should I start?' },
  },
  {
    id: 'new_user_gift',
    label: { default: 'How do I get the new-user gift?' },
    prompt: { default: 'How do I get the new-user gift?' },
  },
  {
    id: 'weekly_discount',
    label: { default: 'Any top-up discounts this week?' },
    prompt: { default: 'Any top-up discounts this week?' },
  },
  {
    id: 'ai_recommendation',
    label: { default: 'Help me write an L1 recommendation.' },
    prompt: { default: 'Help me write an L1 recommendation.' },
  },
]

function normalizeConversationStarterCopy(
  value: unknown
): ConversationStarterCopy | null {
  // Legacy rows store one plain string; treat it as the default language.
  if (typeof value === 'string') {
    return { default: value }
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  const copy: ConversationStarterCopy = {}
  for (const [locale, text] of Object.entries(
    value as Record<string, unknown>
  )) {
    if (typeof text !== 'string') continue
    copy[locale] = text
  }
  // A freshly added starter has no copy yet; keep its row so it can be edited.
  if (!Object.hasOwn(copy, 'default')) copy.default = ''
  return copy
}

function parseConversationStarters(value: string): ConversationStarter[] {
  if (!value.trim()) return DEFAULT_CONVERSATION_STARTERS
  try {
    const parsed = JSON.parse(value) as unknown
    if (!Array.isArray(parsed)) return []
    const starters: ConversationStarter[] = []
    for (const item of parsed) {
      if (!item || typeof item !== 'object') continue
      const candidate = item as Record<string, unknown>
      if (typeof candidate.id !== 'string') continue
      const label = normalizeConversationStarterCopy(candidate.label)
      const prompt = normalizeConversationStarterCopy(candidate.prompt)
      if (!label || !prompt) continue
      starters.push({ id: candidate.id, label, prompt })
    }
    return starters
  } catch {
    return []
  }
}

export function ConversationStartersEditor(props: {
  value: string
  disabled: boolean
  onChange: (value: string) => void
}) {
  const { t } = useTranslation()
  const items = parseConversationStarters(props.value)
  const write = (next: ConversationStarter[]) =>
    props.onChange(JSON.stringify(next))
  const update = (index: number, patch: Partial<ConversationStarter>) =>
    write(
      items.map((item, itemIndex) =>
        itemIndex === index ? { ...item, ...patch } : item
      )
    )
  const updateCopy = (
    index: number,
    field: 'label' | 'prompt',
    locale: string,
    text: string
  ) =>
    update(index, {
      [field]: { ...items[index][field], [locale]: text },
    } as Partial<ConversationStarter>)
  const clearCopy = (
    index: number,
    field: 'label' | 'prompt',
    locale: string
  ) =>
    update(index, {
      [field]: Object.fromEntries(
        Object.entries(items[index][field]).filter(([key]) => key !== locale)
      ),
    } as Partial<ConversationStarter>)
  const remove = (index: number) =>
    write(items.filter((_, itemIndex) => itemIndex !== index))
  const move = (index: number, direction: -1 | 1) => {
    const target = index + direction
    if (target < 0 || target >= items.length) return
    const next = [...items]
    ;[next[index], next[target]] = [next[target], next[index]]
    write(next)
  }
  return (
    <div
      className='space-y-3'
      data-testid='assistant-conversation-starters-editor'
    >
      <div className='text-muted-foreground text-sm'>
        {t(
          'Each starter shows in the visitor’s interface language when you provide that language, and otherwise falls back to the default text.'
        )}
      </div>
      {items.map((item, index) => (
        <SettingsDisclosure
          key={item.id}
          className='settings-starter'
          defaultOpen={index === 0 || !item.label.default}
          title={
            <>
              <span className='settings-starter-index'>
                {String(index + 1).padStart(2, '0')}
              </span>
              {item.label.default || t('Button label')}
            </>
          }
        >
          <div className='space-y-3'>
            <div className='flex items-center justify-between gap-2'>
              <span className='text-muted-foreground text-xs'>
                {t('Starter {{number}}', { number: index + 1 })}
              </span>
              <div className='flex gap-1'>
                <Button
                  type='button'
                  variant='ghost'
                  size='icon'
                  aria-label={t('Move starter up')}
                  onClick={() => move(index, -1)}
                  disabled={props.disabled || index === 0}
                >
                  <ArrowUp className='size-4' />
                </Button>
                <Button
                  type='button'
                  variant='ghost'
                  size='icon'
                  aria-label={t('Move starter down')}
                  onClick={() => move(index, 1)}
                  disabled={props.disabled || index === items.length - 1}
                >
                  <ArrowDown className='size-4' />
                </Button>
                <Button
                  type='button'
                  variant='ghost'
                  size='icon'
                  aria-label={t('Delete starter')}
                  onClick={() => remove(index)}
                  disabled={props.disabled}
                >
                  <Trash2 className='size-4' />
                </Button>
              </div>
            </div>
            <Input
              aria-label={t('Button label')}
              value={item.label.default ?? ''}
              maxLength={80}
              onChange={(event) =>
                updateCopy(index, 'label', 'default', event.target.value)
              }
              disabled={props.disabled}
            />
            <Textarea
              aria-label={t('Prompt text')}
              value={item.prompt.default ?? ''}
              maxLength={2000}
              rows={2}
              onChange={(event) =>
                updateCopy(index, 'prompt', 'default', event.target.value)
              }
              disabled={props.disabled}
            />
            <SettingsDisclosure title={t('Translations')}>
              <div className='space-y-4'>
                {INTERFACE_LANGUAGE_OPTIONS.map((language) => (
                  <div
                    key={language.code}
                    className='grid gap-2 sm:grid-cols-[8rem_minmax(0,1fr)] sm:items-center'
                  >
                    <span className='text-muted-foreground text-xs'>
                      {language.label}
                    </span>
                    <Input
                      aria-label={t('{{language}} label', {
                        language: language.label,
                      })}
                      value={item.label[language.code] ?? ''}
                      maxLength={80}
                      placeholder={item.label.default ?? ''}
                      onChange={(event) =>
                        event.target.value
                          ? updateCopy(
                              index,
                              'label',
                              language.code,
                              event.target.value
                            )
                          : clearCopy(index, 'label', language.code)
                      }
                      disabled={props.disabled}
                    />
                    <span className='text-muted-foreground text-xs sm:col-start-1'>
                      {t('Prompt')}
                    </span>
                    <Textarea
                      aria-label={t('{{language}} prompt', {
                        language: language.label,
                      })}
                      value={item.prompt[language.code] ?? ''}
                      maxLength={2000}
                      rows={2}
                      placeholder={item.prompt.default ?? ''}
                      onChange={(event) =>
                        event.target.value
                          ? updateCopy(
                              index,
                              'prompt',
                              language.code,
                              event.target.value
                            )
                          : clearCopy(index, 'prompt', language.code)
                      }
                      disabled={props.disabled}
                    />
                  </div>
                ))}
              </div>
            </SettingsDisclosure>
          </div>
        </SettingsDisclosure>
      ))}
      <div className='flex flex-wrap gap-2'>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() =>
            write([
              ...items,
              {
                id: `custom_${Date.now()}`,
                label: { default: '' },
                prompt: { default: '' },
              },
            ])
          }
          disabled={props.disabled || items.length >= 20}
        >
          <Plus className='mr-1 size-4' />
          {t('Add starter')}
        </Button>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          onClick={() => props.onChange('')}
          disabled={props.disabled}
        >
          {t('Restore defaults')}
        </Button>
      </div>
    </div>
  )
}

async function getEnabledAssistantModelIDs(group: string) {
  const response = await api.get<{ data?: unknown }>('/api/assistant/models', {
    params: { group },
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  const models = Array.isArray(response.data.data)
    ? response.data.data.filter(
        (modelID): modelID is string =>
          typeof modelID === 'string' && modelID.trim().length > 0
      )
    : []
  return [...new Set(models)].sort((left, right) => left.localeCompare(right))
}

function parseSkillFiles(value: string): AssistantSkillFile[] {
  try {
    const parsed = JSON.parse(value) as unknown
    if (!Array.isArray(parsed)) return []
    return parsed
      .filter((item): item is AssistantSkillFile => {
        if (!item || typeof item !== 'object') return false
        const candidate = item as Record<string, unknown>
        return (
          typeof candidate.path === 'string' &&
          typeof candidate.content === 'string' &&
          typeof candidate.enabled === 'boolean'
        )
      })
      .sort((left, right) => left.path.localeCompare(right.path))
  } catch {
    return []
  }
}

function AssistantSkillFilesEditor(props: {
  value: string
  disabled: boolean
  onChange: (value: string) => void
}) {
  const { t } = useTranslation()
  const [selectedPath, setSelectedPath] = useState<string | null>(null)
  const files = parseSkillFiles(props.value)
  const selected = Math.max(
    files.findIndex((file) => file.path === selectedPath),
    0
  )
  const selectedFile = files[selected]

  const updateFiles = (next: AssistantSkillFile[]) => {
    props.onChange(
      JSON.stringify(next.sort((a, b) => a.path.localeCompare(b.path)))
    )
  }

  const addFile = () => {
    const existing = new Set(files.map((file) => file.path))
    let index = files.length + 1
    let path = `skills/skill-${index}/SKILL.md`
    while (existing.has(path)) {
      index += 1
      path = `skills/skill-${index}/SKILL.md`
    }
    updateFiles([
      ...files,
      {
        path,
        content: `---\nname: skill-${index}\ndescription: Describe one bounded workflow.\n---\n\n# New platform skill\n\nDescribe one bounded workflow here.`,
        enabled: true,
      },
    ])
    setSelectedPath(path)
  }

  const updateSelected = (changes: Partial<AssistantSkillFile>) => {
    if (!selectedFile) return
    updateFiles(
      files.map((file, index) =>
        index === selected ? { ...file, ...changes } : file
      )
    )
  }

  const removeSelected = () => {
    if (!selectedFile) return
    const next = files.filter((_, index) => index !== selected)
    updateFiles(next)
    setSelectedPath(next[Math.min(selected, next.length - 1)]?.path ?? null)
  }

  return (
    <div className='space-y-3'>
      <div className='flex items-start justify-between gap-3'>
        <div>
          <p className='text-sm font-medium'>{t('Platform skill files')}</p>
          <p className='text-muted-foreground mt-1 text-sm'>
            {t(
              'These are bounded virtual files shared by the platform assistant. They never grant filesystem or tool permissions.'
            )}
          </p>
        </div>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={addFile}
          disabled={props.disabled || files.length >= 32}
        >
          <Plus className='mr-1.5 h-4 w-4' />
          {t('Add file')}
        </Button>
      </div>

      <div className='border-border/60 grid min-h-64 overflow-hidden rounded-lg border md:grid-cols-[13rem_1fr]'>
        <div className='bg-muted/20 border-border/60 space-y-1 border-b p-2 md:border-r md:border-b-0'>
          {files.length === 0 ? (
            <p className='text-muted-foreground px-2 py-5 text-sm'>
              {t('No platform skill files yet.')}
            </p>
          ) : (
            files.map((file, index) => (
              <button
                key={file.path}
                type='button'
                className={`flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-sm transition-colors ${index === selected ? 'bg-accent text-accent-foreground' : 'hover:bg-accent/50'}`}
                onClick={() => setSelectedPath(file.path)}
                disabled={props.disabled}
              >
                <FileText className='h-4 w-4 shrink-0' />
                <span className='truncate'>
                  {file.path.replace(/^skills\//, '')}
                </span>
                {!file.enabled && (
                  <span className='text-muted-foreground ml-auto text-xs'>
                    {t('Off')}
                  </span>
                )}
              </button>
            ))
          )}
        </div>

        <div className='space-y-3 p-3'>
          {selectedFile ? (
            <>
              <div className='flex items-center gap-2'>
                <Input
                  value={selectedFile.path}
                  onChange={(event) =>
                    updateSelected({ path: event.target.value })
                  }
                  disabled={props.disabled}
                  aria-label={t('Skill file path')}
                  autoComplete='off'
                />
                <Button
                  type='button'
                  variant='ghost'
                  size='icon'
                  onClick={removeSelected}
                  disabled={props.disabled}
                  aria-label={t('Delete skill file')}
                >
                  <Trash2 className='h-4 w-4' />
                </Button>
              </div>
              <Textarea
                value={selectedFile.content}
                onChange={(event) =>
                  updateSelected({ content: event.target.value })
                }
                disabled={props.disabled}
                rows={9}
                maxLength={12000}
                className='font-mono text-sm'
                aria-label={t('Skill file content')}
              />
              <div className='flex items-center justify-between gap-3'>
                <label className='text-muted-foreground flex items-center gap-2 text-sm'>
                  <input
                    type='checkbox'
                    checked={selectedFile.enabled}
                    onChange={(event) =>
                      updateSelected({ enabled: event.target.checked })
                    }
                    disabled={props.disabled}
                  />
                  {t('Use this platform skill')}
                </label>
                <span className='text-muted-foreground text-xs'>
                  {t('Maximum 32 files / 32000 characters total')}
                </span>
              </div>
            </>
          ) : (
            <p className='text-muted-foreground py-10 text-sm'>
              {t('Add a file to edit a platform skill.')}
            </p>
          )}
        </div>
      </div>
    </div>
  )
}

export function AssistantSettingsSection(props: {
  defaultValues: AssistantSettingsFormValues
}) {
  const { t } = useTranslation()
  const updateOptions = useUpdateOptions()
  const form = useForm<AssistantSettingsFormValues>({
    resolver: zodResolver(assistantSettingsSchema),
    defaultValues: props.defaultValues,
  })

  useEffect(() => {
    form.reset(props.defaultValues)
  }, [form, props.defaultValues])

  const onSubmit = async (values: AssistantSettingsFormValues) => {
    const updates = Object.fromEntries(
      Object.entries(values)
        .filter(
          ([key, value]) =>
            value !==
            props.defaultValues[key as keyof AssistantSettingsFormValues]
        )
        .map(([key, value]) => [key, String(value)])
    )

    if (Object.keys(updates).length > 0) {
      await updateOptions.mutateAsync(updates)
    }
  }

  const [
    enabled,
    agentLoopEnabled,
    cacheEnabled,
    reviewEnabled,
    retentionEnabled,
    searchProvider,
    selectedGroup,
    selectedModel,
  ] = useWatch({
    control: form.control,
    name: [
      'AssistantEnabled',
      'AssistantAgentLoopEnabled',
      'AssistantCacheEnabled',
      'AssistantReviewEnabled',
      'AssistantRetentionEnabled',
      'AssistantSearchProvider',
      'AssistantGroup',
      'AssistantModel',
    ],
  })
  const groupsQuery = useQuery({
    queryKey: ['assistant-routing-groups'],
    queryFn: async () => {
      const response = await api.get<{ data?: unknown }>('/api/group/')
      const groups = Array.isArray(response.data.data)
        ? response.data.data.filter(
            (group): group is string =>
              typeof group === 'string' && group.trim().length > 0
          )
        : []
      return [...new Set(groups)].sort((left, right) =>
        left.localeCompare(right)
      )
    },
    staleTime: 60_000,
  })
  const assistantGroups = [
    ...new Set([
      props.defaultValues.AssistantGroup || 'default',
      props.defaultValues.AssistantReviewGroup || 'default',
      ...(groupsQuery.data ?? []),
    ]),
  ].sort((left, right) => left.localeCompare(right))
  const assistantModelsQuery = useQuery({
    queryKey: ['assistant-routing-models', selectedGroup],
    queryFn: () => getEnabledAssistantModelIDs(selectedGroup),
    enabled: false,
    staleTime: 60_000,
    retry: false,
  })
  const assistantModels = assistantModelsQuery.data ?? EMPTY_ASSISTANT_MODEL_IDS
  const assistantModelListLoaded = assistantModelsQuery.data !== undefined
  const assistantModelOptions = [
    ...new Set([...assistantModels, selectedModel].filter(Boolean)),
  ]
  const selectedModelIsUnavailable =
    Boolean(selectedModel) &&
    assistantModelListLoaded &&
    !assistantModels.includes(selectedModel)
  let modelDescription = t(
    'Choose a group, then click Get model list to load its enabled model IDs.'
  )
  if (assistantModelsQuery.isError) {
    modelDescription = t(
      'The built-in AI assistant is under maintenance. Please try again later.'
    )
  } else if (assistantModelsQuery.isFetching) {
    modelDescription = t('Loading model list...')
  } else if (assistantModelListLoaded && assistantModels.length === 0) {
    modelDescription = t('This group has no enabled model IDs.')
  } else if (assistantModelListLoaded) {
    modelDescription = t(
      'The assistant sends requests with this exact enabled model ID and the selected routing group.'
    )
  }
  const searchProviderDescription: Record<AssistantSearchProvider, string> = {
    none: t('Disable assistant web search.'),
    exa: t('Uses the official Exa Search API from the server.'),
    tavily: t('Uses the official Tavily Search API from the server.'),
    brave: t('Uses the official Brave Search API from the server.'),
    generic_http: t(
      'Send a GET request with the q query parameter to a custom endpoint.'
    ),
    mcp_streamable_http: t(
      'Connect to an MCP server over Streamable HTTP from the server.'
    ),
  }

  return (
    <SettingsSection title={t('AI assistant settings')}>
      <Form {...form}>
        <SettingsForm
          className='settings-stack'
          onSubmit={form.handleSubmit(onSubmit)}
        >
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            onReset={() => form.reset(props.defaultValues)}
            isResetDisabled={!form.formState.isDirty}
            isSaveDisabled={!form.formState.isDirty}
            isSaving={updateOptions.isPending}
            saveLabel='Save assistant settings'
          />
          <FormDirtyIndicator isDirty={form.formState.isDirty} />
          <FormField
            control={form.control}
            name='AssistantEnabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable AI assistant')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Show the assistant launcher and allow assistant conversations.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
                <FormMessage />
              </SettingsSwitchItem>
            )}
          />
          <SettingsDisclosure
            title={t('Model & response')}
            description={t('Routing, streaming and response length')}
            defaultOpen
          >
            <div className='settings-fields grid gap-6 md:grid-cols-2'>
              <FormField
                control={form.control}
                name='AssistantGroup'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Routing group')}</FormLabel>
                    <div className='flex flex-col gap-2 sm:flex-row sm:items-center'>
                      <Select
                        value={field.value}
                        onValueChange={(value) => {
                          field.onChange(value)
                          form.setValue('AssistantModel', '', {
                            shouldDirty: true,
                            shouldValidate: true,
                          })
                        }}
                      >
                        <FormControl>
                          <SelectTrigger
                            className='w-full sm:flex-1'
                            disabled={!enabled || groupsQuery.isLoading}
                          >
                            <SelectValue placeholder={t('Select a group')} />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent alignItemWithTrigger={false}>
                          <SelectGroup>
                            {assistantGroups.map((group) => (
                              <SelectItem key={group} value={group}>
                                {group}
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                      <Button
                        type='button'
                        variant='outline'
                        className='w-full sm:w-auto'
                        onClick={() => {
                          void assistantModelsQuery.refetch()
                        }}
                        disabled={
                          !enabled ||
                          !selectedGroup ||
                          assistantModelsQuery.isFetching
                        }
                        data-testid='assistant-get-model-list'
                      >
                        <RefreshCw
                          data-icon='inline-start'
                          className={
                            assistantModelsQuery.isFetching
                              ? 'animate-spin'
                              : undefined
                          }
                        />
                        <span>{t('Get model list')}</span>
                      </Button>
                    </div>
                    <FormDescription>
                      {t(
                        'Select the routing group used by the assistant, then get its enabled model IDs.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='AssistantModel'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Assistant model ID')}</FormLabel>
                    <Select
                      value={field.value}
                      onValueChange={(value) => {
                        if (typeof value !== 'string' || value.trim() === '') {
                          return
                        }
                        form.setValue('AssistantModel', value, {
                          shouldDirty: true,
                          shouldTouch: true,
                          shouldValidate: true,
                        })
                        form.clearErrors('AssistantModel')
                      }}
                    >
                      <FormControl>
                        <SelectTrigger
                          className='w-full'
                          disabled={
                            !enabled ||
                            !assistantModelListLoaded ||
                            assistantModelsQuery.isFetching ||
                            assistantModelsQuery.isError ||
                            assistantModels.length === 0
                          }
                        >
                          <SelectValue placeholder={t('Select a model ID')} />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent alignItemWithTrigger={false}>
                        <SelectGroup>
                          {assistantModelOptions.map((modelID) => (
                            <SelectItem key={modelID} value={modelID}>
                              {modelID}
                              {modelID === selectedModel &&
                              selectedModelIsUnavailable
                                ? ` · ${t('not enabled')}`
                                : null}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FormDescription>{modelDescription}</FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='AssistantReasoningEffort'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Reasoning Effort')}</FormLabel>
                    <Select
                      value={field.value}
                      onValueChange={(value) => {
                        if (
                          typeof value === 'string' &&
                          (
                            ASSISTANT_REASONING_EFFORTS as readonly string[]
                          ).includes(value)
                        ) {
                          field.onChange(value as AssistantReasoningEffort)
                        }
                      }}
                    >
                      <FormControl>
                        <SelectTrigger className='w-full' disabled={!enabled}>
                          <SelectValue />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent alignItemWithTrigger={false}>
                        {ASSISTANT_REASONING_EFFORTS.map((effort) => (
                          <SelectItem key={effort} value={effort}>
                            {effort}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <FormDescription>
                      {t(
                        'Controls the default reasoning hint sent with assistant requests. Auto lets each model use its native default.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
            <div className='grid gap-6'>
              <SettingsSwitchItem className='border-0 p-0'>
                <SettingsSwitchContent>
                  <FormLabel>{t('Stream responses')}</FormLabel>
                  <FormDescription>
                    {t('Stream tokens incrementally as they are generated')}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormField
                  control={form.control}
                  name='AssistantStreamEnabled'
                  render={({ field }) => (
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                        disabled={!enabled}
                      />
                    </FormControl>
                  )}
                />
              </SettingsSwitchItem>

              <div className='grid gap-5 sm:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='AssistantTemperature'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Response temperature')}</FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min={0}
                          max={2}
                          step={0.1}
                          {...safeNumberFieldProps(field)}
                          disabled={!enabled}
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Controls how varied the assistant response can be (0–2).'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='AssistantMaxTokens'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Maximum output tokens')}</FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min={64}
                          max={8192}
                          step={1}
                          {...safeNumberFieldProps(field)}
                          disabled={!enabled}
                        />
                      </FormControl>
                      <FormDescription>
                        {t('Limits each final response to 64–8192 tokens.')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>
            </div>
          </SettingsDisclosure>
          <SettingsDisclosure
            title={t('Assistant behavior')}
            description={t('Personality and instructions')}
          >
            <div className='space-y-6'>
              <div>
                <p className='text-muted-foreground mt-1 text-sm'>
                  {t(
                    'Customize the assistant without changing its built-in privacy and confirmation rules.'
                  )}
                </p>
              </div>
              <FormField
                control={form.control}
                name='AssistantPersona'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Personality')}</FormLabel>
                    <FormControl>
                      <Textarea
                        {...field}
                        disabled={!enabled}
                        rows={3}
                        maxLength={2000}
                        placeholder={t(
                          'Helpful onboarding coach, concise technical writer, and honest product guide.'
                        )}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('Describe the tone, role, and communication style.')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='AssistantSystemPrompt'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Administrator instructions')}</FormLabel>
                    <FormControl>
                      <Textarea
                        {...field}
                        disabled={!enabled}
                        rows={5}
                        maxLength={8000}
                        placeholder={t(
                          'Add product policies, support workflow, and facts the assistant should follow.'
                        )}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'These instructions are appended to the assistant context.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
          </SettingsDisclosure>
          <SettingsDisclosure
            title={t('Conversation starter prompts')}
            description={t('Questions shown before a conversation')}
          >
            <FormField
              control={form.control}
              name='AssistantPreConversationPresets'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Conversation starter prompts')}</FormLabel>
                  <FormControl>
                    <ConversationStartersEditor
                      value={field.value}
                      disabled={!enabled}
                      onChange={field.onChange}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Optional JSON list of starter buttons. Leave empty for reviewed defaults; use [] to hide them.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </SettingsDisclosure>
          <SettingsDisclosure
            title={t('Search & skills')}
            description={t('Search providers, tools and playbooks')}
          >
            <div className='space-y-6'>
              <FormField
                control={form.control}
                name='AssistantSearchProvider'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Search provider')}</FormLabel>
                    <Select
                      value={field.value}
                      onValueChange={(value) => {
                        if (
                          typeof value === 'string' &&
                          (
                            ASSISTANT_SEARCH_PROVIDERS as readonly string[]
                          ).includes(value)
                        ) {
                          field.onChange(value)
                        }
                      }}
                    >
                      <FormControl>
                        <SelectTrigger className='w-full' disabled={!enabled}>
                          <SelectValue
                            placeholder={t('Select a search provider')}
                          />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent alignItemWithTrigger={false}>
                        <SelectGroup>
                          <SelectItem value='none'>{t('Disabled')}</SelectItem>
                          <SelectItem value='exa'>Exa</SelectItem>
                          <SelectItem value='tavily'>Tavily</SelectItem>
                          <SelectItem value='brave'>Brave Search</SelectItem>
                          <SelectItem value='generic_http'>
                            {t('Custom HTTP')}
                          </SelectItem>
                          <SelectItem value='mcp_streamable_http'>
                            {t('MCP (Streamable HTTP)')}
                          </SelectItem>
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FormDescription>
                      {searchProviderDescription[searchProvider]}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              {searchProvider === 'generic_http' && (
                <FormField
                  control={form.control}
                  name='AssistantSearchURL'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Search tool API URL')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          disabled={!enabled}
                          placeholder='https://search.example/api/search'
                          autoComplete='off'
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'The assistant sends a GET request with the query parameter q.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              )}
              {searchProvider === 'mcp_streamable_http' && (
                <>
                  <FormField
                    control={form.control}
                    name='AssistantSearchURL'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>
                          {t('MCP Streamable HTTP endpoint')}
                        </FormLabel>
                        <FormControl>
                          <Input
                            {...field}
                            disabled={!enabled}
                            placeholder='https://search.example/mcp'
                            autoComplete='off'
                          />
                        </FormControl>
                        <FormDescription>
                          {t(
                            'The endpoint and credentials are used only by the server.'
                          )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='AssistantSearchMCPTool'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>
                          {t('Optional MCP search tool name')}
                        </FormLabel>
                        <FormControl>
                          <Input
                            {...field}
                            disabled={!enabled}
                            placeholder='web_search'
                            autoComplete='off'
                          />
                        </FormControl>
                        <FormDescription>
                          {t(
                            'Leave empty to automatically find a search tool.'
                          )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </>
              )}
              <FormField
                control={form.control}
                name='AssistantSearchAPIKey'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Search tool API key')}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        type='password'
                        disabled={!enabled}
                        placeholder={t('Leave blank to keep the existing key')}
                        autoComplete='new-password'
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'The key is stored server-side and is never shown in the options response.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='AssistantSkills'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Skills and playbooks')}</FormLabel>
                    <FormControl>
                      <Textarea
                        {...field}
                        disabled={!enabled}
                        rows={6}
                        maxLength={12000}
                        placeholder={t(
                          'One skill or workflow per line. Example: CC Switch troubleshooting: check endpoint, model ID, key, then run a small request.'
                        )}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Give the agent reusable guidance for platform setup and support workflows.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='AssistantSkillFiles'
                render={({ field }) => (
                  <FormItem>
                    <FormControl>
                      <AssistantSkillFilesEditor
                        value={field.value}
                        disabled={!enabled}
                        onChange={field.onChange}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
          </SettingsDisclosure>
          <SettingsDisclosure
            title={t('Agent runtime')}
            description={t('Tool calls, timeouts and caching')}
          >
            <div className='space-y-6'>
              <FormField
                control={form.control}
                name='AssistantAgentLoopEnabled'
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>{t('Enable agent tool loop')}</FormLabel>
                      <FormDescription>
                        {t(
                          'Allow the assistant to call safe information and calculation tools before producing its final answer.'
                        )}
                      </FormDescription>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                        disabled={!enabled}
                      />
                    </FormControl>
                    <FormMessage />
                  </SettingsSwitchItem>
                )}
              />
              <div className='grid gap-6 sm:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='AssistantMaxSteps'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Maximum agent steps')}</FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min={1}
                          max={32}
                          step={1}
                          {...safeNumberFieldProps(field)}
                          disabled={!enabled || !agentLoopEnabled}
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Maximum number of model/tool turns in one assistant request (1–32).'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='AssistantTimeoutSeconds'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Agent timeout (seconds)')}</FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min={5}
                          max={300}
                          step={1}
                          {...safeNumberFieldProps(field)}
                          disabled={!enabled || !agentLoopEnabled}
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Hard limit for the complete agent loop (5–300 seconds).'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>
              <FormField
                control={form.control}
                name='AssistantCacheEnabled'
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>
                        {t('Cache identical first questions')}
                      </FormLabel>
                      <FormDescription>
                        {t(
                          'Return the same successful answer for an identical first question during the cache window without calling an upstream model.'
                        )}
                      </FormDescription>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                        disabled={!enabled}
                      />
                    </FormControl>
                    <FormMessage />
                  </SettingsSwitchItem>
                )}
              />
              <FormField
                control={form.control}
                name='AssistantCacheTTLMinutes'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Cache window (minutes)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={0}
                        max={10080}
                        step={1}
                        {...safeNumberFieldProps(field)}
                        disabled={!enabled || !cacheEnabled}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('Set to 0 to disable caching; the maximum is 7 days.')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
          </SettingsDisclosure>
          <SettingsDisclosure
            title={t('Access & safety')}
            description={t('Registration protection and L1 review')}
          >
            <AssistantL1ReviewSettings
              groups={assistantGroups}
              groupsLoading={groupsQuery.isLoading}
              getModels={getEnabledAssistantModelIDs}
            />
          </SettingsDisclosure>
          <SettingsDisclosure
            title={t('Automatic review')}
            description={t('Anonymous metrics and review frequency')}
          >
            <div className='space-y-6'>
              <FormField
                control={form.control}
                name='AssistantReviewEnabled'
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>{t('Enable scheduled review')}</FormLabel>
                      <FormDescription>
                        {t(
                          'Create a bounded background review without copying conversations or user identities.'
                        )}
                      </FormDescription>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                    <FormMessage />
                  </SettingsSwitchItem>
                )}
              />
              <div className='grid gap-5 sm:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='AssistantReviewWindowDays'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Review window (days)')}</FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min={1}
                          max={90}
                          step={1}
                          {...safeNumberFieldProps(field)}
                          disabled={!reviewEnabled}
                        />
                      </FormControl>
                      <FormDescription>
                        {t('Summarize the last 1–90 days.')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='AssistantReviewIntervalHours'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Review interval (hours)')}</FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min={1}
                          max={168}
                          step={1}
                          {...safeNumberFieldProps(field)}
                          disabled={!reviewEnabled}
                        />
                      </FormControl>
                      <FormDescription>
                        {t('Run every 1–168 hours.')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>
            </div>
          </SettingsDisclosure>
          <SettingsDisclosure
            title={t('Conversation retention')}
            description={t('Retention periods and cleanup')}
          >
            <div className='space-y-6'>
              <FormField
                control={form.control}
                name='AssistantRetentionEnabled'
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>{t('Enable scheduled cleanup')}</FormLabel>
                      <FormDescription>
                        {t(
                          'Run conversation cleanup as a background system task.'
                        )}
                      </FormDescription>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                    <FormMessage />
                  </SettingsSwitchItem>
                )}
              />
              <div className='grid gap-5 sm:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='AssistantActiveRetentionDays'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Active conversations (days)')}</FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min={7}
                          max={3650}
                          step={1}
                          {...safeNumberFieldProps(field)}
                          disabled={!retentionEnabled}
                        />
                      </FormControl>
                      <FormDescription>
                        {t('Keep inactive conversations for 7–3650 days.')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='AssistantArchivedRetentionDays'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {t('Archived conversations (days)')}
                      </FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min={1}
                          max={3650}
                          step={1}
                          {...safeNumberFieldProps(field)}
                          disabled={!retentionEnabled}
                        />
                      </FormControl>
                      <FormDescription>
                        {t('Keep archived conversations for 1–3650 days.')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='AssistantSecurityRetentionDays'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Security reports (days)')}</FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min={30}
                          max={3650}
                          step={1}
                          {...safeNumberFieldProps(field)}
                          disabled={!retentionEnabled}
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Keep terminated security conversations for 30–3650 days.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='AssistantRetentionIntervalHours'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Cleanup interval (hours)')}</FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min={1}
                          max={168}
                          step={1}
                          {...safeNumberFieldProps(field)}
                          disabled={!retentionEnabled}
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Run every 1–168 hours; each pass is memory-bounded.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>
            </div>
          </SettingsDisclosure>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
