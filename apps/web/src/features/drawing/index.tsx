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
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'
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
import { Button, buttonVariants } from '@/components/ui/button'
import {
  Drawer,
  DrawerContent,
  DrawerHeader,
  DrawerTitle,
} from '@/components/ui/drawer'
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
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { getAssistantStatus, type DrawingWebAccess } from '../assistant/api'
import { rotateMcpToken } from '../open-source-bounties/api'
import { getPricing } from '../pricing/api'
import type { PricingModel } from '../pricing/types'
import { DrawingErrorBoundary } from './drawing-error-boundary'
import { DrawingGallery } from './drawing-gallery'
import { DrawingSnakeGame } from './drawing-snake-game'
import {
  clearActiveDrawingTask,
  getActiveDrawingTask,
  getDrawingDraft,
  getDrawingMinigamePref,
  registerActiveDrawingTask,
  saveDrawingDraft,
  setDrawingMinigamePref,
  subscribeActiveDrawingTask,
  updateActiveDrawingTask,
  type ActiveDrawingTask,
} from './drawing-task-state'
import {
  getDrawingRequestErrorKind,
  getDrawingRequestErrorMessage,
  getDrawingRequestStatus,
} from './error-state'
import { DRAWING_HISTORY_BYTES, DRAWING_HISTORY_LIMIT } from './history-storage'
import { drawingSource, type GeneratedDrawing } from './image-bytes'
import { buildDrawingMcpConfig } from './mcp-config'
import { useDrawingHistory } from './use-drawing-history'
import { getDrawingWebDenial, resolveDrawingWebAccess } from './web-access'

type ImageResponse = {
  data?: GeneratedDrawing[]
  error?: { message?: string; code?: string }
  drawing_web_access?: DrawingWebAccess
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
  const userId = useAuthStore((state) => state.auth.user?.id)
  // Remount every account-owned state, including prompt/reference files and the
  // session-only MCP secret. Never render one account's previews for another.
  return userId ? (
    <DrawingErrorBoundary>
      <DrawingWorkbench key={userId} userId={userId} />
    </DrawingErrorBoundary>
  ) : null
}

function DrawingWorkbench({ userId }: { userId: number }) {
  const { t, i18n } = useTranslation()
  const history = useDrawingHistory(userId)
  const results = history.images
  const quotaPerUSD = useSystemConfigStore(
    (state) => state.config.currency.quotaPerUnit
  )
  const [webDenial, setWebDenial] = useState<DrawingWebAccess | null>(null)
  const [keyPending, setKeyPending] = useState(false)
  const [keyReady, setKeyReady] = useState(false)
  const [keyError, setKeyError] = useState<string | null>(null)
  const activeRef = useRef(true)
  const requestPendingRef = useRef(false)
  const currentAbortRef = useRef<AbortController | null>(null)
  const isCurrentUser = useCallback(
    () => activeRef.current && useAuthStore.getState().auth.user?.id === userId,
    [userId]
  )
  useEffect(() => {
    activeRef.current = true
    return () => {
      activeRef.current = false
    }
  }, [])

  const initialDraft = useMemo(() => getDrawingDraft(userId), [userId])
  const [prompt, setPrompt] = useState(initialDraft.prompt || '')
  const [group, setGroup] = useState(initialDraft.group || '')
  const [model, setModel] = useState(initialDraft.model || '')
  const [size, setSize] = useState(initialDraft.size || '')
  const [quality, setQuality] = useState(initialDraft.quality || '')
  const [count, setCount] = useState(initialDraft.count || '1')
  const [referenceImages, setReferenceImages] = useState<ReferenceImage[]>([])
  const [error, setError] = useState<string | null>(null)
  const [errorStatus, setErrorStatus] = useState<number | null>(null)
  const [stoppedMessage, setStoppedMessage] = useState<string | null>(null)
  const [generating, setGenerating] = useState(false)
  const [elapsedSeconds, setElapsedSeconds] = useState(0)
  const [minigameEnabled, setMinigameEnabled] = useState(getDrawingMinigamePref)
  const [minigameExpanded, setMinigameExpanded] = useState(false)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [activeTask, setActiveTask] = useState<ActiveDrawingTask | null>(() =>
    getActiveDrawingTask(userId)
  )

  const [drawingMcpToken, setDrawingMcpToken] = useState('')
  const [drawingMcpPending, setDrawingMcpPending] = useState(false)
  const [drawingMcpOpen, setDrawingMcpOpen] = useState(false)
  const referenceInputRef = useRef<HTMLInputElement>(null)
  const previewUrlsRef = useRef(new Set<string>())

  // Save prompt draft automatically
  useEffect(() => {
    saveDrawingDraft(userId, { prompt })
  }, [userId, prompt])

  // Subscribe to background active task across SPA navigations
  useEffect(() => {
    const existing = getActiveDrawingTask(userId)
    if (existing && existing.status === 'generating') {
      setGenerating(true)
      setActiveTask(existing)
      if (existing.abortController) {
        currentAbortRef.current = existing.abortController
      }
    }
    return subscribeActiveDrawingTask(userId, (task) => {
      if (!isCurrentUser()) return
      setActiveTask(task)
      if (task && task.status === 'generating') {
        setGenerating(true)
        if (task.abortController) {
          currentAbortRef.current = task.abortController
        }
      } else if (task && task.status === 'failed') {
        setGenerating(false)
        if (task.error) setError(task.error)
        if (task.errorStatus) setErrorStatus(task.errorStatus)
      } else if (
        !task ||
        task.status === 'completed' ||
        task.status === 'stopped'
      ) {
        setGenerating(false)
      }
    })
  }, [userId, isCurrentUser])

  // Live elapsed timer
  useEffect(() => {
    if (!generating) {
      setElapsedSeconds(0)
      return
    }
    const started = activeTask?.startedAt || Date.now()
    const updateTime = () => {
      const sec = Math.max(1, Math.floor((Date.now() - started) / 1000))
      setElapsedSeconds(sec)
    }
    updateTime()
    const timer = setInterval(updateTime, 1000)
    return () => clearInterval(timer)
  }, [generating, activeTask?.startedAt])

  // Auto expand minigame when waiting > 3s if enabled
  useEffect(() => {
    if (
      generating &&
      elapsedSeconds >= 3 &&
      minigameEnabled &&
      !minigameExpanded
    ) {
      setMinigameExpanded(true)
    }
    if (!generating) {
      setMinigameExpanded(false)
    }
  }, [generating, elapsedSeconds, minigameEnabled, minigameExpanded])

  const accessQuery = useQuery({
    queryKey: ['assistant-status', 'drawing', userId],
    queryFn: getAssistantStatus,
    staleTime: 30_000,
    retry: false,
  })
  const walletQuery = useQuery({
    queryKey: ['drawing-wallet', userId],
    enabled: accessQuery.isSuccess && !accessQuery.data.drawing_web_access,
    queryFn: async () => {
      const response = await api.get<{
        success: boolean
        data?: { quota?: number }
      }>('/api/user/self', {
        skipBusinessError: true,
        skipErrorHandler: true,
      })
      if (!response.data.success) {
        throw new Error('Unable to load wallet balance')
      }
      return response.data.data ?? {}
    },
    staleTime: 30_000,
    retry: false,
  })
  const webAccess =
    webDenial ??
    resolveDrawingWebAccess(
      accessQuery.data?.drawing_web_access,
      walletQuery.isError ? undefined : walletQuery.data?.quota,
      quotaPerUSD
    )
  const refreshBalance = async () => {
    const [status, wallet] = await Promise.all([
      accessQuery.refetch(),
      accessQuery.data?.drawing_web_access
        ? Promise.resolve(null)
        : walletQuery.refetch(),
    ])
    if (!isCurrentUser()) return
    if (
      status.isError ||
      (!status.data?.drawing_web_access && wallet?.isError)
    ) {
      setWebDenial({
        minimum_balance_usd: 10,
        balance_usd: null,
        allowed: false,
      })
    } else {
      setWebDenial(
        resolveDrawingWebAccess(
          status.data?.drawing_web_access,
          wallet?.data?.quota,
          quotaPerUSD
        )
      )
    }
  }
  const pricingQuery = useQuery({
    queryKey: ['drawing-pricing'],
    queryFn: getPricing,
    staleTime: 5 * 60_000,
    retry: false,
  })
  const groupsQuery = useQuery({
    queryKey: ['drawing-user-groups', userId],
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
  const selectedGroup = groups.includes(group)
    ? group
    : groups.includes('image-2')
      ? 'image-2'
      : (groups[0] ?? '')
  const modelsForGroup = imageModels.filter((item) =>
    modelSupportsGroup(item, selectedGroup)
  )
  const selectedModel = modelsForGroup.some((item) => item.model_name === model)
    ? model
    : modelsForGroup.some((item) => item.model_name === 'image-2')
      ? 'image-2'
      : (modelsForGroup[0]?.model_name ?? '')
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

  const sizes =
    selectedModel === 'dall-e-2' || selectedModel === 'dall-e'
      ? ['256x256', '512x512', '1024x1024']
      : selectedModel === 'dall-e-3'
        ? ['1024x1024', '1024x1792', '1792x1024']
        : ['1024x1024', '1024x1536', '1536x1024']
  const qualities = selectedModel.startsWith('gpt-image-')
    ? ['auto', 'low', 'medium', 'high']
    : ['standard', 'hd']
  const sizePresets = ['', ...sizes].map((value) => ({
    value,
    label: value || t('Default'),
  }))
  const qualityPresets = ['', ...qualities].map((value) => ({
    value,
    label: value || t('Default'),
  }))

  const selectedSize = sizePresets.some((option) => option.value === size)
    ? size
    : ''
  const selectedQuality = qualityPresets.some(
    (option) => option.value === quality
  )
    ? quality
    : ''

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

  useEffect(() => {
    if (selectedGroup) {
      saveDrawingDraft(userId, {
        group: selectedGroup,
        model: selectedModel,
        size: selectedSize,
        quality: selectedQuality,
        count,
      })
    }
  }, [
    userId,
    selectedGroup,
    selectedModel,
    selectedSize,
    selectedQuality,
    count,
  ])

  const stopGeneration = () => {
    if (currentAbortRef.current) {
      currentAbortRef.current.abort()
      currentAbortRef.current = null
    }
    const currentTask = getActiveDrawingTask(userId)
    if (currentTask?.abortController) {
      currentTask.abortController.abort()
    }
    clearActiveDrawingTask(userId)
    requestPendingRef.current = false
    setGenerating(false)
    setError(null)
    setErrorStatus(null)
    setStoppedMessage(
      t(
        'Stopped waiting. If the server is already processing, the generated image may appear in your history later.'
      )
    )
  }

  const copyErrorDetails = async () => {
    const details = {
      timestamp: new Date().toISOString(),
      model: selectedModel,
      group: selectedGroup,
      prompt: prompt.trim(),
      status: errorStatus,
      error,
    }
    const success = await copyToClipboard(JSON.stringify(details, null, 2))
    if (success) {
      toast.success(t('Error details copied'))
    }
  }

  const generate = async () => {
    const cleanPrompt = prompt.trim()
    if (
      requestPendingRef.current ||
      !isCurrentUser() ||
      !accessGranted ||
      !webAccess.allowed ||
      history.clearing ||
      !cleanPrompt ||
      !selectedGroup ||
      !selectedModel
    ) {
      return
    }
    requestPendingRef.current = true
    const abortController = new AbortController()
    currentAbortRef.current = abortController
    const ticket = history.capture()
    const metadata = {
      prompt: cleanPrompt,
      model: selectedModel,
      group: selectedGroup,
      createdAt: Date.now(),
    }
    const taskId = crypto.randomUUID()
    registerActiveDrawingTask({
      id: taskId,
      userId,
      prompt: cleanPrompt,
      group: selectedGroup,
      model: selectedModel,
      size: selectedSize,
      quality: selectedQuality,
      count,
      referenceCount: referenceImages.length,
      startedAt: Date.now(),
      abortController,
      status: 'generating',
    })

    setGenerating(true)
    setError(null)
    setErrorStatus(null)
    setStoppedMessage(null)

    try {
      let response
      if (referenceImages.length > 0) {
        const form = new FormData()
        form.append('prompt', cleanPrompt)
        form.append('model', selectedModel)
        form.append('n', count)
        if (selectedSize) form.append('size', selectedSize)
        if (selectedQuality) form.append('quality', selectedQuality)
        for (const image of referenceImages) {
          form.append('image', image.file, image.file.name)
        }
        response = await api.post<ImageResponse>(
          `/pg/images/edits?group=${encodeURIComponent(selectedGroup)}`,
          form,
          {
            signal: abortController.signal,
            skipBusinessError: true,
            skipErrorHandler: true,
          }
        )
      } else {
        response = await api.post<ImageResponse>(
          `/pg/images/generations?group=${encodeURIComponent(selectedGroup)}`,
          {
            prompt: cleanPrompt,
            model: selectedModel,
            n: Number(count),
            ...(selectedSize ? { size: selectedSize } : {}),
            ...(selectedQuality ? { quality: selectedQuality } : {}),
          },
          {
            signal: abortController.signal,
            skipBusinessError: true,
            skipErrorHandler: true,
          }
        )
      }

      if (abortController.signal.aborted) return
      if (!isCurrentUser() || ticket !== history.capture()) return

      const denial = getDrawingWebDenial({ response })
      if (denial) {
        clearActiveDrawingTask(userId, taskId)
        setWebDenial(denial)
        return
      }
      if (
        !response.data ||
        response.data.error ||
        !Array.isArray(response.data.data)
      ) {
        const status = getDrawingRequestStatus({ response })
        const errorMsg = getDrawingRequestErrorMessage(
          { response },
          t('Unable to generate the image')
        )
        setError(errorMsg)
        setErrorStatus(status)
        updateActiveDrawingTask(userId, {
          status: 'failed',
          error: errorMsg,
          errorStatus: status,
        })
        return
      }
      const usableResults = response.data.data.filter(
        (image) => drawingSource(image) !== undefined
      )
      if (usableResults.length === 0) {
        const msg = t('No images were returned')
        setError(msg)
        updateActiveDrawingTask(userId, { status: 'failed', error: msg })
      } else {
        clearActiveDrawingTask(userId, taskId)
        // Cache failures are handled separately: successful generation is never
        // an API error or an invitation to regenerate (and pay again).
        void history.remember(usableResults, metadata, ticket)
      }
    } catch (cause: unknown) {
      if (
        abortController.signal.aborted ||
        (cause instanceof Error && cause.name === 'AbortError')
      ) {
        return
      }
      if (!isCurrentUser() || ticket !== history.capture()) return
      const denial = getDrawingWebDenial(cause)
      if (denial) {
        clearActiveDrawingTask(userId, taskId)
        setWebDenial(denial)
        return
      }
      const fallbackMessages = {
        unauthenticated: t('Session expired!'),
        forbidden: t('No permission to perform this action'),
        unavailable: t('Please try again later.'),
        network: t('Network connection failed or server not responding'),
        http: t('Unable to generate the image'),
      }
      const status = getDrawingRequestStatus(cause)
      const errorMsg = getDrawingRequestErrorMessage(
        cause,
        fallbackMessages[getDrawingRequestErrorKind(cause)]
      )
      setError(errorMsg)
      setErrorStatus(status)
      updateActiveDrawingTask(userId, {
        status: 'failed',
        error: errorMsg,
        errorStatus: status,
      })
    } finally {
      requestPendingRef.current = false
      if (currentAbortRef.current === abortController) {
        currentAbortRef.current = null
      }
      if (isCurrentUser()) {
        setGenerating(false)
        try {
          void refreshBalance()
        } catch {
          // Balance refresh failures should never break drawing studio
        }
      }
    }
  }

  const ensureDrawingKey = async () => {
    if (keyPending || !accessGranted || !isCurrentUser()) return
    setKeyPending(true)
    setKeyError(null)
    try {
      const response = await api.post<{
        success: boolean
        data?: { id: number; name: string; group: 'image-2'; created: boolean }
      }>(
        '/api/assistant/drawing/key',
        {},
        { skipBusinessError: true, skipErrorHandler: true }
      )
      if (!isCurrentUser()) return
      if (
        !response.data.success ||
        !response.data.data?.id ||
        response.data.data.group !== 'image-2'
      ) {
        throw new Error('Unable to prepare drawing key')
      }
      setKeyReady(true)
    } catch (cause) {
      if (isCurrentUser()) {
        setKeyError(
          getDrawingRequestErrorMessage(
            cause,
            t('Unable to prepare the image-2 API Key.')
          )
        )
      }
    } finally {
      if (isCurrentUser()) setKeyPending(false)
    }
  }

  const copyDrawingMcpConfig = async () => {
    if (drawingMcpPending || !isCurrentUser()) return
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
        if (!isCurrentUser()) return
        token = connection.token
        setDrawingMcpToken(token)
      }
      const copied = await copyToClipboard(
        buildDrawingMcpConfig(drawingMcpEndpoint, token)
      )
      if (!isCurrentUser()) return
      if (copied) {
        toast.success(t('Drawing MCP configuration copied.'))
      } else {
        toast.error(t('Unable to copy the drawing MCP configuration.'))
      }
    } catch {
      if (isCurrentUser()) {
        toast.error(t('Unable to create the drawing MCP configuration.'))
      }
    } finally {
      if (isCurrentUser()) setDrawingMcpPending(false)
    }
  }

  let content: ReactNode
  const standaloneHistory = true
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
    const settingsForm = (
      <div className='grid gap-5'>
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
              <NativeSelectOption key={item.model_name} value={item.model_name}>
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
              value={selectedSize}
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
            <Label htmlFor='drawing-quality'>{t('Quality (optional)')}</Label>
            <NativeSelect
              id='drawing-quality'
              value={selectedQuality}
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
    )

    content = (
      <div className='grid gap-4 xl:grid-cols-[minmax(0,1fr)_19rem] xl:items-stretch'>
        <section
          data-slot='drawing-canvas'
          className='relative flex min-h-[20rem] min-w-0 flex-col overflow-hidden rounded-lg border border-white/10 bg-[#111210] sm:min-h-[26rem] xl:min-h-[36rem]'
          aria-live='polite'
          aria-labelledby='drawing-canvas-title'
        >
          <header className='flex items-center justify-between gap-3 border-b border-white/10 px-4 py-3 sm:px-5'>
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
              {/* Mobile generation settings trigger */}
              <Button
                type='button'
                variant='outline'
                size='xs'
                className='border-white/15 bg-white/5 text-xs text-white/80 xl:hidden'
                onClick={() => setSettingsOpen(true)}
              >
                <span className='max-w-[7rem] truncate font-mono'>
                  {selectedModel || t('Preview')}
                </span>
                {selectedGroup ? (
                  <>
                    <span className='text-white/40'>·</span>
                    <span className='max-w-[5rem] truncate text-white/70'>
                      {selectedGroup}
                    </span>
                  </>
                ) : null}
                <span className='ml-1 text-xs'>⚙️</span>
              </Button>

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
              <div className='mx-auto flex max-w-md flex-col items-center justify-center p-4 text-center text-white/90'>
                <div className='bg-primary/15 text-primary mb-3 flex size-12 items-center justify-center rounded-lg'>
                  <HugeiconsIcon
                    icon={Loading03Icon}
                    className='size-7 animate-spin'
                    strokeWidth={2}
                    aria-hidden='true'
                  />
                </div>
                <p className='text-sm font-medium'>
                  {t('Request submitted · Waiting {{seconds}}s', {
                    seconds: elapsedSeconds,
                  })}
                </p>
                <p className='mt-1 text-xs text-white/60'>
                  {t('Waiting for image generation result...')}
                </p>
                <p className='mt-1 max-w-xs text-[11px] text-white/40'>
                  {t(
                    'You can continue waiting, or stop waiting. Results will also be saved to history.'
                  )}
                </p>

                {/* Minigame section */}
                {elapsedSeconds >= 3 ? (
                  <div className='mt-4 flex w-full flex-col items-center'>
                    {minigameExpanded ? (
                      <div className='flex flex-col items-center gap-2'>
                        <DrawingSnakeGame />
                        <Button
                          type='button'
                          variant='ghost'
                          size='xs'
                          className='mt-1 text-white/60 hover:text-white'
                          onClick={() => setMinigameExpanded(false)}
                        >
                          {t('Collapse minigame')}
                        </Button>
                      </div>
                    ) : (
                      <Button
                        type='button'
                        variant='outline'
                        size='xs'
                        className='border-white/15 bg-white/5 text-white hover:bg-white/10'
                        onClick={() => {
                          setMinigameExpanded(true)
                          setMinigameEnabled(true)
                          setDrawingMinigamePref(true)
                        }}
                      >
                        {t('Play dot-matrix snake while waiting')}
                      </Button>
                    )}
                  </div>
                ) : null}

                <div className='mt-5 flex items-center gap-3'>
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    className='border-white/20 bg-white/5 text-white hover:bg-white/15'
                    onClick={stopGeneration}
                  >
                    {t('Stop waiting')}
                  </Button>
                </div>
              </div>
            ) : stoppedMessage ? (
              <div className='w-full max-w-md p-4 text-center'>
                <Empty className='max-w-md text-white'>
                  <EmptyHeader>
                    <EmptyMedia
                      variant='icon'
                      className='size-10 bg-white/10 text-white/75'
                    >
                      <HugeiconsIcon
                        icon={Cancel01Icon}
                        className='size-5'
                        strokeWidth={2}
                        aria-hidden='true'
                      />
                    </EmptyMedia>
                    <EmptyTitle className='text-white/85'>
                      {t('Stop waiting')}
                    </EmptyTitle>
                    <EmptyDescription className='mt-2 text-xs text-white/65'>
                      {stoppedMessage}
                    </EmptyDescription>
                  </EmptyHeader>
                </Empty>
              </div>
            ) : results.length > 0 ? (
              <DrawingGallery images={results} />
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
                  <AlertTitle className='flex items-center justify-between'>
                    <span>{t('Request failed')}</span>
                    {errorStatus ? (
                      <Badge
                        variant='outline'
                        className='text-destructive-foreground font-mono text-xs'
                      >
                        HTTP {errorStatus}
                      </Badge>
                    ) : null}
                  </AlertTitle>
                  <AlertDescription>{error}</AlertDescription>
                  <AlertAction className='gap-2'>
                    <Button
                      type='button'
                      size='sm'
                      variant='outline'
                      onClick={() => void generate()}
                      disabled={
                        generating || !webAccess.allowed || history.clearing
                      }
                    >
                      {t('Retry')}
                    </Button>
                    <Button
                      type='button'
                      size='sm'
                      variant='secondary'
                      onClick={() => void copyErrorDetails()}
                    >
                      {t('Copy error details')}
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
                {generating ? (
                  <Button
                    type='button'
                    size='lg'
                    variant='outline'
                    className='w-full border-white/20 text-white hover:bg-white/10 sm:w-auto sm:min-w-36'
                    onClick={stopGeneration}
                  >
                    <HugeiconsIcon
                      icon={Cancel01Icon}
                      data-icon='inline-start'
                      strokeWidth={2}
                      aria-hidden='true'
                    />
                    {t('Stop waiting')}
                  </Button>
                ) : (
                  <Button
                    type='button'
                    size='lg'
                    className='w-full sm:w-auto sm:min-w-36'
                    onClick={() => void generate()}
                    disabled={
                      generating ||
                      !webAccess.allowed ||
                      history.clearing ||
                      !prompt.trim() ||
                      !selectedGroup ||
                      !selectedModel
                    }
                  >
                    <HugeiconsIcon
                      icon={Image01Icon}
                      data-icon='inline-start'
                      strokeWidth={2}
                      aria-hidden='true'
                    />
                    {referenceImages.length > 0
                      ? t('Edit image')
                      : t('Generate image')}
                  </Button>
                )}
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

          <div className='p-4'>{settingsForm}</div>

          <div className='mt-auto border-t p-4'>
            <p className='text-muted-foreground text-xs leading-5'>
              {t('Billing follows the selected group configuration.')}
            </p>
          </div>
        </aside>

        {/* Mobile settings drawer */}
        <Drawer open={settingsOpen} onOpenChange={setSettingsOpen}>
          <DrawerContent className='max-h-[85vh] overflow-y-auto p-4 pb-8'>
            <DrawerHeader className='px-0 pt-0'>
              <DrawerTitle>{t('Generation settings')}</DrawerTitle>
            </DrawerHeader>
            <div className='mt-2'>{settingsForm}</div>
          </DrawerContent>
        </Drawer>
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
            disabled={keyPending}
            onClick={() => void ensureDrawingKey()}
          >
            {keyPending ? t('Loading') : t('Prepare image-2 API Key')}
          </Button>
          <a
            href='/keys'
            className={buttonVariants({ variant: 'outline', size: 'sm' })}
          >
            {t('Manage API Keys')}
          </a>
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
          {accessGranted && !webAccess.allowed ? (
            <Alert
              variant='destructive'
              className='mb-4'
              data-slot='drawing-web-access'
            >
              <AlertTitle>
                {webAccess.balance_usd === null
                  ? t('Web image generation balance unavailable')
                  : t('Insufficient balance for web image generation')}
              </AlertTitle>
              <AlertDescription>
                <p>
                  {webAccess.balance_usd === null
                    ? t(
                        'Web image generation requires a minimum balance of USD {{minimum}}. Your current USD balance is unavailable. Refresh the balance to continue.',
                        { minimum: '10.00' }
                      )
                    : t(
                        'Web image generation requires a minimum balance of USD {{minimum}}. Current balance: USD {{balance}}.',
                        {
                          minimum: '10.00',
                          balance: webAccess.balance_usd.toFixed(2),
                        }
                      )}
                </p>
                <p>
                  {t(
                    'You can still create an image-2 API Key and use image-2 through the API or MCP, subject to existing permissions and available quota. API and MCP usage is billed normally, not free.'
                  )}
                </p>
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  disabled={accessQuery.isFetching || walletQuery.isFetching}
                  onClick={() => void refreshBalance()}
                >
                  {t('Refresh balance')}
                </Button>
              </AlertDescription>
            </Alert>
          ) : null}
          {keyReady || keyError ? (
            <Alert
              className='mb-4'
              variant={keyError ? 'destructive' : 'default'}
            >
              <AlertTitle>
                {keyError ? t('Request failed') : t('image-2 API Key ready')}
              </AlertTitle>
              <AlertDescription>
                {keyError ||
                  t(
                    'Open API Key management to reveal or copy your key. No key secret is displayed here.'
                  )}
              </AlertDescription>
            </Alert>
          ) : null}
          <div
            className='mb-4 flex flex-wrap items-center justify-between gap-2'
            data-slot='drawing-history-controls'
          >
            <p className='text-muted-foreground max-w-3xl text-xs'>
              {t(
                'This browser keeps up to {{count}} images or {{size}} MB per account, newest first. Older images are removed automatically. Clearing browser data also removes this history.',
                {
                  count: DRAWING_HISTORY_LIMIT,
                  size: DRAWING_HISTORY_BYTES / 1024 / 1024,
                }
              )}
            </p>
            <Button
              type='button'
              size='sm'
              variant='outline'
              disabled={history.clearing}
              onClick={() => {
                if (
                  window.confirm(
                    t(
                      'Clear image history for this account in this browser? Pending results will not be saved.'
                    )
                  )
                ) {
                  void history.clear()
                }
              }}
            >
              {history.clearing
                ? t('Clearing history...')
                : t('Clear image history')}
            </Button>
          </div>
          {history.loading || history.saving ? (
            <p role='status' className='text-muted-foreground mb-3 text-xs'>
              {history.loading
                ? t('Loading image history...')
                : t('Saving image bytes in this browser...')}
            </p>
          ) : null}
          {history.warning ? (
            <Alert className='mb-4' data-slot='drawing-history-warning'>
              <AlertTitle>{t('Browser image history unavailable')}</AlertTitle>
              <AlertDescription>
                {history.warning === 'clear'
                  ? t(
                      'Browser history could not be cleared. Saved images may return after refresh. Try clearing history again; this will not generate or bill any images.'
                    )
                  : history.warning === 'load'
                    ? t(
                        'Saved image history could not be loaded. You can still generate images, but browser storage may be unavailable.'
                      )
                    : t(
                        'Generation succeeded, but some image bytes could not be saved in this browser (storage quota or image-host CORS restrictions). Download them now. Unsaved or URL-only previews may disappear after refresh. Do not regenerate to repair the cache; another generation is billed again.'
                      )}
              </AlertDescription>
            </Alert>
          ) : null}
          {content}
          {standaloneHistory && results.length > 0 ? (
            <section
              className='mt-4 rounded-lg bg-[#111210] p-4'
              aria-label={t('Image history')}
            >
              <DrawingGallery images={results} />
            </section>
          ) : null}
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
                        'Connect an Agent with the dedicated drawing MCP endpoint. MCP uses the same group permissions and normal API billing, without the web-only USD 10 minimum balance.'
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
