/*
Copyright (C) 2026 LIghtJUNction
*/
import { useQuery } from '@tanstack/react-query'
import {
  Copy,
  ImageIcon,
  ImagePlus,
  RefreshCw,
  ServerCog,
  Sparkles,
  X,
} from 'lucide-react'
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
import { Button } from '@/components/ui/button'
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
      <div className='grid gap-5 xl:grid-cols-[minmax(0,1fr)_20rem] xl:items-stretch'>
        <section
          className='relative flex min-h-[620px] min-w-0 flex-col overflow-hidden rounded-2xl border border-white/10 bg-[#111210] shadow-2xl shadow-black/10'
          aria-live='polite'
          aria-labelledby='drawing-canvas-title'
        >
          <div className='pointer-events-none absolute inset-0 bg-[radial-gradient(circle,rgba(255,255,255,0.07)_1px,transparent_1px)] [background-size:24px_24px] opacity-60' />
          <header className='relative flex items-center justify-between gap-4 border-b border-white/10 bg-black/10 px-4 py-3 sm:px-5'>
            <div className='flex min-w-0 items-center gap-3'>
              <div className='bg-primary/15 text-primary flex size-9 shrink-0 items-center justify-center rounded-lg'>
                <Sparkles className='size-4' aria-hidden='true' />
              </div>
              <div className='min-w-0'>
                <p className='text-[10px] font-medium tracking-[0.16em] text-white/45 uppercase'>
                  {t('Canvas')}
                </p>
                <h2
                  id='drawing-canvas-title'
                  className='truncate text-sm font-medium text-white/90'
                >
                  {selectedModel || t('Preview')}
                </h2>
              </div>
            </div>
            <div className='flex shrink-0 items-center gap-2 text-xs'>
              <span className='rounded-full border border-white/10 bg-white/5 px-2.5 py-1 text-white/65'>
                {generating
                  ? referenceImages.length > 0
                    ? t('Editing...')
                    : t('Generating...')
                  : results.length > 0
                    ? t('Preview')
                    : referenceImages.length > 0
                      ? t('Edit image')
                      : t('Draft')}
              </span>
              {selectedGroup ? (
                <span className='hidden rounded-full border border-white/10 px-2.5 py-1 text-white/45 sm:inline'>
                  {selectedGroup}
                </span>
              ) : null}
            </div>
          </header>

          <div className='relative flex min-h-0 flex-1 items-center justify-center overflow-y-auto p-4 sm:p-8'>
            {generating ? (
              <div className='flex flex-col items-center justify-center text-center text-white/65'>
                <div className='bg-primary/15 text-primary mb-5 flex size-16 items-center justify-center rounded-2xl'>
                  <RefreshCw
                    className='size-7 animate-spin'
                    aria-hidden='true'
                  />
                </div>
                <p className='text-sm'>{t('Generation in progress...')}</p>
                <p className='mt-2 max-w-xs text-xs leading-5 text-white/40'>
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
                      className='group relative min-w-0 overflow-hidden rounded-xl border border-white/10 bg-black/30 p-2'
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
              <div className='max-w-md text-center text-white/55'>
                <div className='mx-auto mb-5 flex size-16 items-center justify-center rounded-2xl border border-white/10 bg-white/5'>
                  <ImageIcon
                    className='size-7 text-white/35'
                    aria-hidden='true'
                  />
                </div>
                <p className='text-sm text-white/75'>
                  {t('Your generated images will appear here.')}
                </p>
                <p className='mt-2 text-xs leading-5 text-white/40'>
                  {hasPrompt
                    ? t(
                        'Review the generated images here when the request finishes.'
                      )
                    : t(
                        'Describe an image, choose a group, and generate a preview.'
                      )}
                </p>
              </div>
            )}
          </div>

          <div className='relative border-t border-white/10 p-3 sm:p-4'>
            <div className='rounded-xl border border-white/10 bg-black/55 p-3 shadow-xl shadow-black/20 backdrop-blur sm:p-4'>
              <div className='mb-2 flex items-center justify-between gap-3'>
                <Label htmlFor='drawing-prompt-input' className='text-white/80'>
                  {t('Prompt')}
                </Label>
                <span className='text-xs text-white/35 tabular-nums'>
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
                className='min-h-20 resize-none border-0 bg-transparent px-0 py-1 text-base text-white shadow-none placeholder:text-white/30 focus-visible:ring-0'
              />
              <div className='mt-2 flex items-center justify-between gap-3 text-xs'>
                <span className='truncate text-white/40'>
                  {t('Be specific about the subject, mood, and style.')}
                </span>
                <span className='shrink-0 text-white/55'>
                  {hasPrompt ? t('Ready') : t('Draft')}
                </span>
              </div>
            </div>
          </div>
        </section>

        <aside className='border-border/70 bg-card/30 flex min-w-0 flex-col rounded-2xl border'>
          <div className='border-border/70 border-b p-5'>
            <div className='flex items-start justify-between gap-3'>
              <div>
                <p className='text-muted-foreground mb-2 text-xs font-medium tracking-[0.14em] uppercase'>
                  {t('Inspector')}
                </p>
                <h2 className='text-lg font-medium'>{t('Generation setup')}</h2>
              </div>
              {configurationReady ? (
                <span className='text-primary pt-1 text-xs font-medium'>
                  {t('Ready')}
                </span>
              ) : null}
            </div>
            <p className='text-muted-foreground mt-2 text-xs leading-5'>
              {t('Choose a route and output settings.')}
            </p>
          </div>

          <div className='grid gap-5 p-5'>
            <div className='grid gap-3'>
              <div className='flex items-center justify-between gap-3'>
                <Label htmlFor='drawing-reference-images'>
                  {t('Reference images')}
                </Label>
                <span className='text-muted-foreground text-xs tabular-nums'>
                  {referenceImages.length}/{maxReferenceImages}
                </span>
              </div>
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
              {referenceImages.length === 0 ? (
                <Button
                  type='button'
                  variant='outline'
                  className='text-muted-foreground hover:text-foreground h-24 border-dashed'
                  disabled={generating}
                  onClick={() => referenceInputRef.current?.click()}
                >
                  <ImagePlus className='mr-2 size-4' aria-hidden='true' />
                  {t('Add reference images')}
                </Button>
              ) : (
                <>
                  <div className='grid grid-cols-2 gap-2'>
                    {referenceImages.map((image, index) => {
                      const label = referenceImageLabel(index)
                      return (
                        <figure
                          key={image.id}
                          className='group relative aspect-square min-w-0 overflow-hidden rounded-xl border bg-black/10'
                          title={image.file.name}
                        >
                          <img
                            src={image.previewUrl}
                            alt={label}
                            className='size-full object-cover'
                          />
                          <figcaption className='absolute bottom-1.5 left-1.5 rounded-md bg-black/75 px-2 py-1 text-[11px] font-medium text-white'>
                            {label}
                          </figcaption>
                          <Button
                            type='button'
                            variant='secondary'
                            size='icon'
                            className='absolute top-1.5 right-1.5 size-7 rounded-full bg-black/75 text-white opacity-90 hover:bg-black'
                            disabled={generating}
                            aria-label={t('Remove {{name}}', { name: label })}
                            onClick={() => removeReferenceImage(image.id)}
                          >
                            <X className='size-3.5' aria-hidden='true' />
                          </Button>
                        </figure>
                      )
                    })}
                  </div>
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    disabled={
                      generating || referenceImages.length >= maxReferenceImages
                    }
                    onClick={() => referenceInputRef.current?.click()}
                  >
                    <ImagePlus className='mr-2 size-4' aria-hidden='true' />
                    {t('Add reference images')}
                  </Button>
                </>
              )}
              <p className='text-muted-foreground text-xs leading-5'>
                {t(
                  'PNG, JPEG or WebP, up to {{count}} images and {{size}} MB each.',
                  {
                    count: maxReferenceImages,
                    size: maxReferenceImageBytes / 1024 / 1024,
                  }
                )}
              </p>
              {referenceImages.length > 0 ? (
                <div className='bg-muted/40 grid gap-1 rounded-lg border px-3 py-2 text-xs leading-5'>
                  <span className='font-medium'>
                    {t(
                      'Reference images switch this request to image editing.'
                    )}
                  </span>
                  <span className='text-muted-foreground'>
                    {t(
                      'Use the image labels in your prompt to describe how each reference should be used.'
                    )}
                  </span>
                </div>
              ) : null}
            </div>
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

          <div className='mt-auto grid gap-3 border-t p-5'>
            {error ? (
              <Alert variant='destructive'>
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
            <Button
              type='button'
              className='h-11 w-full'
              onClick={() => void generate()}
              disabled={
                generating || !prompt.trim() || !selectedGroup || !selectedModel
              }
            >
              {generating ? (
                <RefreshCw
                  className='mr-2 size-4 animate-spin'
                  aria-hidden='true'
                />
              ) : (
                <ImageIcon className='mr-2 size-4' aria-hidden='true' />
              )}
              {generating
                ? referenceImages.length > 0
                  ? t('Editing...')
                  : t('Generating...')
                : referenceImages.length > 0
                  ? t('Edit image')
                  : t('Generate image')}
            </Button>
            <p className='text-muted-foreground text-center text-xs leading-5'>
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
      <SectionPageLayout.Content>
        <div className='mx-auto w-full max-w-7xl pb-16'>
          <header className='mb-8 grid gap-2'>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Create images through the same safe, group-aware relay used by the API.'
              )}
            </p>
          </header>
          {accessGranted ? (
            <section className='bg-card mb-5 grid gap-4 border p-4 sm:p-5'>
              <div className='flex flex-wrap items-start justify-between gap-3'>
                <div className='flex min-w-0 items-start gap-3'>
                  <span className='bg-primary/10 text-primary flex size-9 shrink-0 items-center justify-center rounded-lg'>
                    <ServerCog className='size-4' aria-hidden='true' />
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
                  <Copy data-icon='inline-start' />
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
          {content}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
