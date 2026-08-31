/*
Copyright (C) 2026 LIghtJUNction
*/
import {
  Cancel01Icon,
  Copy01Icon,
  Image01Icon,
  ImageAdd01Icon,
  Loading03Icon,
  McpServerIcon,
  SparklesIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useQuery } from '@tanstack/react-query'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import {
  Alert,
  AlertAction,
  AlertDescription,
  AlertTitle,
} from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'
import { api } from '@/lib/api'
import { copyToClipboard } from '@/lib/copy-to-clipboard'

import { getAssistantStatus } from '../assistant/api'
import { rotateMcpToken } from '../open-source-bounties/api'
import { getPricing } from '../pricing/api'
import type { PricingModel } from '../pricing/types'
import {
  getDrawingRequestErrorKind,
  getDrawingRequestStatus,
} from './error-state'
import { buildDrawingMcpConfig } from './mcp-config'

type ImageResult = {
  url?: string
  b64_json?: string
  revised_prompt?: string
}

type ImageResponse = {
  data?: ImageResult[]
  error?: { message?: string }
  message?: string
}

type ReferenceImage = {
  id: string
  file: File
  previewUrl: string
}

const maxReferenceImages = 8
const maxReferenceImageBytes = 10 * 1024 * 1024
const supportedReferenceImageTypes = ['image/jpeg', 'image/png', 'image/webp']
const chineseImageOrdinals = ['一', '二', '三', '四', '五', '六', '七', '八']

function isImageModel(model: PricingModel): boolean {
  return model.supported_endpoint_types?.includes('image-generation') === true
}

function imageSource(image: ImageResult): string | undefined {
  if (image.url?.trim()) return image.url.trim()
  if (image.b64_json?.trim()) return `data:image/png;base64,${image.b64_json}`
  return undefined
}

function modelSupportsGroup(model: PricingModel, group: string): boolean {
  return (
    model.enable_groups.includes('all') || model.enable_groups.includes(group)
  )
}

function DrawingQueryErrorAlert(props: {
  title: string
  error: unknown
  onRetry: () => void | Promise<unknown>
}) {
  const { t } = useTranslation()
  const kind = getDrawingRequestErrorKind(props.error)
  const status = getDrawingRequestStatus(props.error)
  let description: string
  switch (kind) {
    case 'unauthenticated':
      description = t('Session expired!')
      break
    case 'forbidden':
      description = t('No permission to perform this action')
      break
    case 'unavailable':
      description = t('Please try again later.')
      break
    case 'network':
      description = t('Network connection failed or server not responding')
      break
    default:
      description = t('Request failed')
  }

  return (
    <Alert variant={kind === 'forbidden' ? 'default' : 'destructive'}>
      <AlertTitle>{props.title}</AlertTitle>
      <AlertDescription>
        {description}
        {status !== null ? ` (HTTP ${status})` : ''}
      </AlertDescription>
      <AlertAction>
        <Button
          type='button'
          size='sm'
          variant='outline'
          onClick={props.onRetry}
        >
          {t('Retry')}
        </Button>
      </AlertAction>
    </Alert>
  )
}

export function Drawing() {
  const { t, i18n } = useTranslation()
  const [prompt, setPrompt] = useState('')
  const [group, setGroup] = useState('')
  const [model, setModel] = useState('')
  const [size, setSize] = useState('')
  const [quality, setQuality] = useState('')
  const [count, setCount] = useState('1')
  const [results, setResults] = useState<ImageResult[]>([])
  const [referenceImages, setReferenceImages] = useState<ReferenceImage[]>([])
  const [error, setError] = useState<string | null>(null)
  const [generating, setGenerating] = useState(false)
  const [drawingMcpToken, setDrawingMcpToken] = useState('')
  const [drawingMcpPending, setDrawingMcpPending] = useState(false)
  const [drawingMcpOpen, setDrawingMcpOpen] = useState(false)
  const referenceInputRef = useRef<HTMLInputElement>(null)
  const previewUrlsRef = useRef(new Set<string>())

  const accessQuery = useQuery({
    queryKey: ['assistant-status'],
    queryFn: getAssistantStatus,
    staleTime: 30_000,
    retry: false,
  })
  const pricingQuery = useQuery({
    queryKey: ['drawing-pricing'],
    queryFn: getPricing,
    staleTime: 5 * 60_000,
    retry: false,
  })
  const groupsQuery = useQuery({
    queryKey: ['drawing-user-groups'],
    queryFn: async () => {
      const response = await api.get<{
        success: boolean
        data?: Record<string, { desc: string; ratio: number | string }>
        message?: string
      }>('/api/user/self/groups')
      return response.data
    },
    staleTime: 60_000,
    retry: false,
  })

  const imageModels = useMemo(
    () =>
      (pricingQuery.data?.data ?? [])
        .filter(isImageModel)
        .sort((left, right) => left.model_name.localeCompare(right.model_name)),
    [pricingQuery.data?.data]
  )
  const groups = useMemo(() => {
    const usable =
      groupsQuery.data?.data ?? pricingQuery.data?.usable_group ?? {}
    return Object.keys(usable)
      .filter((name) =>
        imageModels.some((item) => modelSupportsGroup(item, name))
      )
      .sort((left, right) => left.localeCompare(right))
  }, [groupsQuery.data?.data, imageModels, pricingQuery.data?.usable_group])
  let selectedGroup = groups[0] ?? ''
  if (groups.includes('image-2')) selectedGroup = 'image-2'
  if (groups.includes(group)) selectedGroup = group
  const modelsForGroup = imageModels.filter((item) =>
    modelSupportsGroup(item, selectedGroup)
  )
  let selectedModel = modelsForGroup[0]?.model_name ?? ''
  if (modelsForGroup.some((item) => item.model_name === 'image-2')) {
    selectedModel = 'image-2'
  }
  if (modelsForGroup.some((item) => item.model_name === model)) {
    selectedModel = model
  }
  const groupDescription =
    groupsQuery.data?.data?.[selectedGroup]?.desc ??
    pricingQuery.data?.usable_group?.[selectedGroup]?.desc
  const accessGranted = accessQuery.data?.developer_access_granted === true
  const hasPrompt = prompt.trim().length > 0
  const configurationReady = Boolean(selectedGroup && selectedModel)
  const drawingMcpEndpoint =
    typeof window === 'undefined'
      ? '/mcp/drawing'
      : `${window.location.origin}/mcp/drawing`
  const drawingMcpConfig = drawingMcpToken
    ? buildDrawingMcpConfig(drawingMcpEndpoint, drawingMcpToken)
    : ''

  const sizePresets = useMemo(() => {
    const defaults = [{ value: '', label: t('Default') }]
    if (selectedModel === 'dall-e-2' || selectedModel === 'dall-e') {
      return [
        ...defaults,
        ...['256x256', '512x512', '1024x1024'].map((value) => ({
          value,
          label: value,
        })),
      ]
    }
    if (selectedModel === 'dall-e-3') {
      return [
        ...defaults,
        ...['1024x1024', '1024x1792', '1792x1024'].map((value) => ({
          value,
          label: value,
        })),
      ]
    }
    return [
      ...defaults,
      ...['1024x1024', '1024x1536', '1536x1024'].map((value) => ({
        value,
        label: value,
      })),
    ]
  }, [selectedModel, t])

  const qualityPresets = useMemo(() => {
    const defaults = [{ value: '', label: t('Default') }]
    if (selectedModel === 'dall-e-3') {
      return [
        ...defaults,
        ...['standard', 'hd'].map((value) => ({ value, label: value })),
      ]
    }
    if (selectedModel === 'gpt-image-1') {
      return [
        ...defaults,
        ...['auto', 'low', 'medium', 'high'].map((value) => ({
          value,
          label: value,
        })),
      ]
    }
    return [
      ...defaults,
      ...['standard', 'hd'].map((value) => ({ value, label: value })),
    ]
  }, [selectedModel, t])

  useEffect(() => {
    setSize((current) =>
      sizePresets.some((option) => option.value === current) ? current : ''
    )
    setQuality((current) =>
      qualityPresets.some((option) => option.value === current) ? current : ''
    )
  }, [qualityPresets, sizePresets])

  useEffect(
    () => () => {
      for (const previewUrl of previewUrlsRef.current) {
        URL.revokeObjectURL(previewUrl)
      }
      previewUrlsRef.current.clear()
    },
    []
  )

  const referenceImageLabel = (index: number) => {
    const language = i18n.resolvedLanguage ?? i18n.language
    const ordinal = language.startsWith('zh')
      ? (chineseImageOrdinals[index] ?? String(index + 1))
      : String(index + 1)
    return t('Reference image {{index}}', { index: ordinal })
  }

  const addReferenceImages = (files: FileList | null) => {
    const selected = [...(files ?? [])]
    if (selected.length === 0) return
    if (referenceImages.length + selected.length > maxReferenceImages) {
      setError(
        t('You can upload up to {{count}} reference images.', {
          count: maxReferenceImages,
        })
      )
      return
    }
    const unsupported = selected.find(
      (file) => !supportedReferenceImageTypes.includes(file.type)
    )
    if (unsupported) {
      setError(
        t('{{name}} is not a supported image file.', {
          name: unsupported.name,
        })
      )
      return
    }
    const oversized = selected.find(
      (file) => file.size > maxReferenceImageBytes
    )
    if (oversized) {
      setError(
        t('{{name}} exceeds the {{size}} MB limit.', {
          name: oversized.name,
          size: maxReferenceImageBytes / 1024 / 1024,
        })
      )
      return
    }
    const additions = selected.map((file) => {
      const previewUrl = URL.createObjectURL(file)
      previewUrlsRef.current.add(previewUrl)
      return { id: crypto.randomUUID(), file, previewUrl }
    })
    setReferenceImages((current) => [...current, ...additions])
    setError(null)
  }

  const removeReferenceImage = (id: string) => {
    const target = referenceImages.find((image) => image.id === id)
    if (target) {
      URL.revokeObjectURL(target.previewUrl)
      previewUrlsRef.current.delete(target.previewUrl)
    }
    setReferenceImages((current) => current.filter((image) => image.id !== id))
  }

  const generate = async () => {
    const cleanPrompt = prompt.trim()
    if (generating || !cleanPrompt || !selectedGroup || !selectedModel) return
    setGenerating(true)
    setError(null)
    setResults([])
    try {
      let response
      if (referenceImages.length > 0) {
        const form = new FormData()
        form.append('prompt', cleanPrompt)
        form.append('model', selectedModel)
        form.append('n', count)
        if (size.trim()) form.append('size', size.trim())
        if (quality.trim()) form.append('quality', quality.trim())
        for (const image of referenceImages) {
          form.append('image', image.file, image.file.name)
        }
        response = await api.post<ImageResponse>(
          `/pg/images/edits?group=${encodeURIComponent(selectedGroup)}`,
          form,
          { skipBusinessError: true, skipErrorHandler: true }
        )
      } else {
        response = await api.post<ImageResponse>(
          `/pg/images/generations?group=${encodeURIComponent(selectedGroup)}`,
          {
            prompt: cleanPrompt,
            model: selectedModel,
            n: Number(count),
            ...(size.trim() ? { size: size.trim() } : {}),
            ...(quality.trim() ? { quality: quality.trim() } : {}),
          },
          { skipBusinessError: true, skipErrorHandler: true }
        )
      }
      if (response.data.error || !Array.isArray(response.data.data)) {
        throw new Error(
          response.data.error?.message ||
            response.data.message ||
            t('Unable to generate the image')
        )
      }
      const usableResults = response.data.data.filter(
        (image) => imageSource(image) !== undefined
      )
      setResults(usableResults)
      if (usableResults.length === 0) {
        setError(t('No images were returned'))
      }
    } catch (cause) {
      setError(
        cause instanceof Error
          ? cause.message
          : t('Unable to generate the image')
      )
    } finally {
      setGenerating(false)
    }
  }

  const copyDrawingMcpConfig = async () => {
    if (drawingMcpPending) return
    setDrawingMcpPending(true)
    try {
      let token = drawingMcpToken
      if (!token) {
        const confirmed = window.confirm(
          t(
            'Generate or rotate the personal MCP token? Existing MCP agents using the old token will stop working immediately.'
          )
        )
        if (!confirmed) return
        const connection = await rotateMcpToken()
        token = connection.token
        setDrawingMcpToken(token)
      }
      const copied = await copyToClipboard(
        buildDrawingMcpConfig(drawingMcpEndpoint, token)
      )
      if (copied) {
        toast.success(t('Drawing MCP configuration copied.'))
      } else {
        toast.error(t('Unable to copy the drawing MCP configuration.'))
      }
    } catch {
      toast.error(t('Unable to create the drawing MCP configuration.'))
    } finally {
      setDrawingMcpPending(false)
    }
  }

  let content: ReactNode
  if (
    accessQuery.isLoading ||
    pricingQuery.isLoading ||
    groupsQuery.isLoading
  ) {
    content = (
      <div className='grid gap-4 sm:grid-cols-2'>
        <Skeleton className='h-10 w-full' />
        <Skeleton className='h-10 w-full' />
        <Skeleton className='h-32 w-full sm:col-span-2' />
      </div>
    )
  } else if (accessQuery.isError) {
    content = (
      <DrawingQueryErrorAlert
        title={t('Failed to load')}
        error={accessQuery.error}
        onRetry={() => accessQuery.refetch()}
      />
    )
  } else if (!accessGranted) {
    content = (
      <Alert>
        <AlertTitle>{t('L1 access required')}</AlertTitle>
        <AlertDescription>
          {t(
            'The drawing workbench is available after developer access is approved.'
          )}
        </AlertDescription>
      </Alert>
    )
  } else if (pricingQuery.isError || groupsQuery.isError) {
    content = (
      <div className='grid gap-3'>
        {pricingQuery.isError ? (
          <DrawingQueryErrorAlert
            title={t('Failed to load playground models')}
            error={pricingQuery.error}
            onRetry={() => pricingQuery.refetch()}
          />
        ) : null}
        {groupsQuery.isError ? (
          <DrawingQueryErrorAlert
            title={t('Failed to load playground groups')}
            error={groupsQuery.error}
            onRetry={() => groupsQuery.refetch()}
          />
        ) : null}
      </div>
    )
  } else if (groups.length === 0) {
    content = (
      <Alert variant='destructive'>
        <AlertTitle>{t('Image catalog unavailable')}</AlertTitle>
        <AlertDescription>
          {t(
            'No image-capable model and routing group is currently available.'
          )}
        </AlertDescription>
        <AlertAction>
          <Button
            type='button'
            size='sm'
            variant='outline'
            onClick={() => {
              void Promise.all([pricingQuery.refetch(), groupsQuery.refetch()])
            }}
          >
            {t('Retry')}
          </Button>
        </AlertAction>
      </Alert>
    )
  } else {
    content = (
      <div className='grid gap-4 xl:grid-cols-[minmax(0,1fr)_19rem] xl:items-stretch'>
        <section
          data-slot='drawing-canvas'
          className='relative flex min-h-[36rem] min-w-0 flex-col overflow-hidden rounded-lg border border-white/10 bg-[#111210]'
          aria-live='polite'
          aria-labelledby='drawing-canvas-title'
        >
          <header className='flex items-center justify-between gap-4 border-b border-white/10 px-4 py-3 sm:px-5'>
            <div className='flex min-w-0 items-center gap-3'>
              <div className='bg-primary/15 text-primary flex size-8 shrink-0 items-center justify-center rounded-md'>
                <HugeiconsIcon
                  icon={SparklesIcon}
                  className='size-4'
                  strokeWidth={2}
                  aria-hidden='true'
                />
              </div>
              <h2
                id='drawing-canvas-title'
                className='truncate text-sm font-medium text-white/90'
              >
                {selectedModel || t('Preview')}
              </h2>
            </div>
            <div className='flex shrink-0 items-center gap-2'>
              <Badge
                variant='outline'
                className='border-white/15 bg-white/5 text-white/80'
              >
                {generating
                  ? referenceImages.length > 0
                    ? t('Editing...')
                    : t('Generating...')
                  : results.length > 0
                    ? t('Preview')
                    : referenceImages.length > 0
                      ? t('Edit image')
                      : t('Draft')}
              </Badge>
              {selectedGroup ? (
                <Badge
                  variant='outline'
                  className='hidden border-white/15 text-white/70 sm:inline-flex'
                >
                  {selectedGroup}
                </Badge>
              ) : null}
            </div>
          </header>

          <div className='flex min-h-0 flex-1 items-center justify-center overflow-y-auto p-4 sm:p-8'>
            {generating ? (
              <div className='flex flex-col items-center justify-center text-center text-white/80'>
                <div className='bg-primary/15 text-primary mb-4 flex size-12 items-center justify-center rounded-lg'>
                  <HugeiconsIcon
                    icon={Loading03Icon}
                    className='size-7 animate-spin'
                    strokeWidth={2}
                    aria-hidden='true'
                  />
                </div>
                <p className='text-sm'>{t('Generation in progress...')}</p>
                <p className='mt-2 max-w-xs text-xs leading-5 text-white/65'>
                  {t('Your request is ready to run.')}
                </p>
              </div>
            ) : results.length > 0 ? (
              <div className='grid max-h-[min(58vh,42rem)] w-full max-w-4xl gap-4 overflow-y-auto sm:grid-cols-2'>
                {results.map((image) => {
                  const src = imageSource(image)
                  if (!src) return null
                  return (
                    <figure
                      className='group relative min-w-0 overflow-hidden rounded-lg border border-white/10 bg-black/30 p-2'
                      key={image.url ?? image.b64_json ?? image.revised_prompt}
                    >
                      <img
                        src={src}
                        alt={image.revised_prompt || prompt}
                        className='h-auto max-h-[38rem] w-full rounded-lg object-contain'
                        loading='lazy'
                      />
                      {image.revised_prompt ? (
                        <figcaption className='absolute inset-x-2 bottom-2 rounded-md bg-black/70 px-2 py-1.5 text-xs leading-5 text-white/70 opacity-0 transition-opacity group-hover:opacity-100'>
                          {image.revised_prompt}
                        </figcaption>
                      ) : null}
                    </figure>
                  )
                })}
              </div>
            ) : (
              <Empty className='max-w-md text-white'>
                <EmptyHeader>
                  <EmptyMedia
                    variant='icon'
                    className='size-10 bg-white/10 text-white/75'
                  >
                    <HugeiconsIcon
                      icon={Image01Icon}
                      className='size-5'
                      strokeWidth={2}
                      aria-hidden='true'
                    />
                  </EmptyMedia>
                  <EmptyTitle className='text-white/85'>
                    {t('Your generated images will appear here.')}
                  </EmptyTitle>
                  <EmptyDescription className='text-xs text-white/65'>
                    {hasPrompt
                      ? t(
                          'Review the generated images here when the request finishes.'
                        )
                      : t(
                          'Describe an image, choose a group, and generate a preview.'
                        )}
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            )}
          </div>

          <div
            data-slot='drawing-composer'
            className='border-t border-white/10 bg-black/20 p-3 sm:p-4'
          >
            <div className='rounded-lg border border-white/10 bg-black/35 p-3 sm:p-4'>
              <div className='mb-2 flex items-center justify-between gap-3'>
                <Label htmlFor='drawing-prompt-input' className='text-white/85'>
                  {t('Prompt')}
                </Label>
                <span className='text-xs text-white/60 tabular-nums'>
                  {prompt.length}/2000
                </span>
              </div>
              <Textarea
                id='drawing-prompt-input'
                value={prompt}
                onChange={(event) => setPrompt(event.target.value)}
                placeholder={t('Describe what you want to see...')}
                maxLength={2000}
                rows={3}
                style={{ backgroundColor: 'transparent' }}
                className='min-h-20 resize-none border-0 bg-transparent px-0 py-1 text-base text-white shadow-none placeholder:text-white/50 focus-visible:ring-0'
              />

              <input
                ref={referenceInputRef}
                id='drawing-reference-images'
                type='file'
                accept={supportedReferenceImageTypes.join(',')}
                multiple
                disabled={generating}
                className='sr-only'
                onChange={(event) => {
                  addReferenceImages(event.target.files)
                  event.target.value = ''
                }}
              />

              {referenceImages.length > 0 ? (
                <div className='mt-3 border-t border-white/10 pt-3'>
                  <div className='mb-2 flex items-center justify-between gap-3'>
                    <Label
                      htmlFor='drawing-reference-images'
                      className='text-xs text-white/75'
                    >
                      {t('Reference images')}
                    </Label>
                    <span className='text-xs text-white/60 tabular-nums'>
                      {referenceImages.length}/{maxReferenceImages}
                    </span>
                  </div>
                  <div className='flex gap-2 overflow-x-auto pb-1'>
                    {referenceImages.map((image, index) => {
                      const label = referenceImageLabel(index)
                      return (
                        <figure
                          key={image.id}
                          className='group relative size-16 shrink-0 overflow-hidden rounded-md border border-white/15 bg-black/30 sm:size-20'
                          title={image.file.name}
                        >
                          <img
                            src={image.previewUrl}
                            alt={label}
                            className='size-full object-cover'
                          />
                          <figcaption className='absolute inset-x-1 bottom-1 truncate rounded-sm bg-black/80 px-1.5 py-0.5 text-[10px] font-medium text-white'>
                            {label}
                          </figcaption>
                          <Button
                            type='button'
                            variant='secondary'
                            size='icon-xs'
                            className='absolute top-1 right-1 bg-black/80 text-white hover:bg-black'
                            disabled={generating}
                            aria-label={t('Remove {{name}}', { name: label })}
                            onClick={() => removeReferenceImage(image.id)}
                          >
                            <HugeiconsIcon
                              icon={Cancel01Icon}
                              strokeWidth={2}
                              aria-hidden='true'
                            />
                          </Button>
                        </figure>
                      )
                    })}
                  </div>
                  <p className='mt-2 text-xs leading-5 text-white/65'>
                    {t(
                      'Use the image labels in your prompt to describe how each reference should be used.'
                    )}
                  </p>
                </div>
              ) : null}

              {error ? (
                <Alert variant='destructive' className='mt-3'>
                  <AlertTitle>{t('Request failed')}</AlertTitle>
                  <AlertDescription>{error}</AlertDescription>
                  <AlertAction>
                    <Button
                      type='button'
                      size='sm'
                      variant='outline'
                      onClick={() => void generate()}
                      disabled={generating}
                    >
                      {t('Retry')}
                    </Button>
                  </AlertAction>
                </Alert>
              ) : null}

              <div className='mt-3 flex flex-col gap-3 border-t border-white/10 pt-3 sm:flex-row sm:items-center sm:justify-between'>
                <div className='flex min-w-0 items-center gap-3'>
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    className='border-white/15 bg-white/5 text-white hover:bg-white/10 hover:text-white'
                    disabled={
                      generating || referenceImages.length >= maxReferenceImages
                    }
                    onClick={() => referenceInputRef.current?.click()}
                  >
                    <HugeiconsIcon
                      icon={ImageAdd01Icon}
                      data-icon='inline-start'
                      strokeWidth={2}
                      aria-hidden='true'
                    />
                    {t('Add reference images')}
                  </Button>
                  <span className='hidden truncate text-xs text-white/65 md:block'>
                    {referenceImages.length > 0
                      ? t(
                          'Reference images switch this request to image editing.'
                        )
                      : t('Be specific about the subject, mood, and style.')}
                  </span>
                </div>
                <Button
                  type='button'
                  size='lg'
                  className='w-full sm:w-auto sm:min-w-36'
                  onClick={() => void generate()}
                  disabled={
                    generating ||
                    !prompt.trim() ||
                    !selectedGroup ||
                    !selectedModel
                  }
                >
                  <HugeiconsIcon
                    icon={generating ? Loading03Icon : Image01Icon}
                    data-icon='inline-start'
                    className={generating ? 'animate-spin' : undefined}
                    strokeWidth={2}
                    aria-hidden='true'
                  />
                  {generating
                    ? referenceImages.length > 0
                      ? t('Editing...')
                      : t('Generating...')
                    : referenceImages.length > 0
                      ? t('Edit image')
                      : t('Generate image')}
                </Button>
              </div>
            </div>
          </div>
        </section>

        <aside
          data-slot='drawing-inspector'
          className='bg-card flex min-w-0 flex-col rounded-lg border'
        >
          <div className='border-b p-4'>
            <div className='flex items-start justify-between gap-3'>
              <h2 className='text-base font-semibold'>
                {t('Generation setup')}
              </h2>
              {configurationReady ? (
                <Badge variant='secondary'>{t('Ready')}</Badge>
              ) : null}
            </div>
            <p className='text-muted-foreground mt-2 text-xs leading-5'>
              {t('Choose a route and output settings.')}
            </p>
          </div>

          <div className='grid gap-5 p-4'>
            <div className='grid gap-2'>
              <Label htmlFor='drawing-group'>{t('Routing group')}</Label>
              <NativeSelect
                id='drawing-group'
                value={selectedGroup}
                className='w-full'
                onChange={(event) => setGroup(event.target.value)}
              >
                {groups.map((item) => (
                  <NativeSelectOption key={item} value={item}>
                    {item}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
              {groupDescription ? (
                <p className='text-muted-foreground text-xs leading-relaxed break-words'>
                  {groupDescription}
                </p>
              ) : null}
            </div>
            <div className='grid gap-2'>
              <Label htmlFor='drawing-model'>{t('Image model')}</Label>
              <NativeSelect
                id='drawing-model'
                value={selectedModel}
                className='w-full'
                onChange={(event) => setModel(event.target.value)}
              >
                {modelsForGroup.map((item) => (
                  <NativeSelectOption
                    key={item.model_name}
                    value={item.model_name}
                  >
                    {item.model_name}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
            <div className='grid gap-4 sm:grid-cols-2 xl:grid-cols-1'>
              <div className='grid gap-2'>
                <Label htmlFor='drawing-size'>{t('Size (optional)')}</Label>
                <NativeSelect
                  id='drawing-size'
                  value={size}
                  className='w-full'
                  onChange={(event) => setSize(event.target.value)}
                >
                  {sizePresets.map((option) => (
                    <NativeSelectOption
                      key={option.value || 'default'}
                      value={option.value}
                    >
                      {option.label}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </div>
              <div className='grid gap-2'>
                <Label htmlFor='drawing-quality'>
                  {t('Quality (optional)')}
                </Label>
                <NativeSelect
                  id='drawing-quality'
                  value={quality}
                  className='w-full'
                  onChange={(event) => setQuality(event.target.value)}
                >
                  {qualityPresets.map((option) => (
                    <NativeSelectOption
                      key={option.value || 'default'}
                      value={option.value}
                    >
                      {option.label}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </div>
            </div>
            <div className='grid max-w-40 gap-2'>
              <Label htmlFor='drawing-count'>{t('Images')}</Label>
              <NativeSelect
                id='drawing-count'
                value={count}
                className='w-full'
                onChange={(event) => setCount(event.target.value)}
              >
                {[1, 2, 3, 4].map((value) => (
                  <NativeSelectOption key={value} value={String(value)}>
                    {value}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
          </div>

          <div className='mt-auto border-t p-4'>
            <p className='text-muted-foreground text-xs leading-5'>
              {t('Billing follows the selected group configuration.')}
            </p>
          </div>
        </aside>
      </div>
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Drawing studio')}</SectionPageLayout.Title>
      {accessGranted ? (
        <SectionPageLayout.Actions>
          <Button
            type='button'
            size='sm'
            variant='outline'
            aria-expanded={drawingMcpOpen}
            aria-controls='drawing-mcp-panel'
            onClick={() => setDrawingMcpOpen((open) => !open)}
          >
            <HugeiconsIcon
              icon={McpServerIcon}
              data-icon='inline-start'
              strokeWidth={2}
              aria-hidden='true'
            />
            {t('Drawing MCP')}
          </Button>
        </SectionPageLayout.Actions>
      ) : null}
      <SectionPageLayout.Content>
        <div className='mx-auto w-full max-w-7xl pb-16'>
          <header className='mb-4 grid gap-2'>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Create images through the same safe, group-aware relay used by the API.'
              )}
            </p>
          </header>
          {content}
          {accessGranted && drawingMcpOpen ? (
            <section
              id='drawing-mcp-panel'
              className='bg-card mt-4 grid gap-4 rounded-lg border p-4 sm:p-5'
            >
              <div className='flex flex-wrap items-start justify-between gap-3'>
                <div className='flex min-w-0 items-start gap-3'>
                  <span className='bg-primary/10 text-primary flex size-9 shrink-0 items-center justify-center rounded-md'>
                    <HugeiconsIcon
                      icon={McpServerIcon}
                      className='size-4'
                      strokeWidth={2}
                      aria-hidden='true'
                    />
                  </span>
                  <div className='min-w-0'>
                    <h2 className='text-sm font-semibold'>
                      {t('Drawing MCP')}
                    </h2>
                    <p className='text-muted-foreground mt-1 max-w-2xl text-xs leading-5'>
                      {t(
                        'Connect an Agent to this drawing workbench with the dedicated MCP endpoint. Generation keeps the same group permissions and billing as this page.'
                      )}
                    </p>
                  </div>
                </div>
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  onClick={() => void copyDrawingMcpConfig()}
                  disabled={drawingMcpPending}
                >
                  <HugeiconsIcon
                    icon={drawingMcpPending ? Loading03Icon : Copy01Icon}
                    data-icon='inline-start'
                    className={drawingMcpPending ? 'animate-spin' : undefined}
                    strokeWidth={2}
                    aria-hidden='true'
                  />
                  {drawingMcpPending
                    ? t('Loading')
                    : drawingMcpToken
                      ? t('Copy drawing MCP config')
                      : t('Generate token and copy config')}
                </Button>
              </div>
              <div className='grid gap-2'>
                <Label htmlFor='drawing-mcp-endpoint'>
                  {t('MCP endpoint')}
                </Label>
                <Input
                  id='drawing-mcp-endpoint'
                  value={drawingMcpEndpoint}
                  readOnly
                  className='font-mono text-xs'
                />
              </div>
              {drawingMcpConfig ? (
                <div className='grid gap-2'>
                  <Label htmlFor='drawing-mcp-config'>
                    {t('Agent configuration')}
                  </Label>
                  <Textarea
                    id='drawing-mcp-config'
                    value={drawingMcpConfig}
                    readOnly
                    rows={9}
                    className='font-mono text-xs'
                  />
                  <p className='text-muted-foreground text-xs leading-5'>
                    {t(
                      'The personal token is shown only in this session. Store the copied configuration in your Agent securely.'
                    )}
                  </p>
                </div>
              ) : null}
            </section>
          ) : null}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
