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
/*
Copyright (C) 2026 LIghtJUNction
*/
import type {
  ColumnFiltersState,
  OnChangeFn,
  PaginationState,
  RowSelectionState,
  VisibilityState,
  SortingState,
} from '@tanstack/react-table'
import { Copy, Plus } from 'lucide-react'
import {
  useState,
  useMemo,
  memo,
  useCallback,
  useEffect,
  forwardRef,
  useImperativeHandle,
  useRef,
} from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  DataTableBulkActions,
  DataTableToolbar,
  DataTablePagination,
  DataTableRow,
  DataTableView,
  useDataTable,
} from '@/components/data-table'
import { Button } from '@/components/ui/button'
import { combineBillingExpr } from '@/features/pricing/lib/billing-expr'
import { useMediaQuery } from '@/hooks'

import { safeJsonParse } from '../utils/json-parser'
import type { PricingMode } from './model-pricing-core'
import {
  ModelPricingEditorPanel,
  type ModelPricingEditorPanelHandle,
  ModelPricingSheet,
  type ModelRatioData,
} from './model-pricing-sheet'
import {
  buildModelSnapshots,
  getSnapshotSignature,
  isBasePricingUnset,
  type ModelRow,
} from './model-pricing-snapshots'
import { buildModelRatioColumns } from './model-ratio-table-columns'
import { useModelPriceLocks } from './use-model-price-locks'

type ModelRatioVisualEditorProps = {
  savedModelPrice: string
  savedModelRatio: string
  savedCacheRatio: string
  savedCreateCacheRatio: string
  savedCompletionRatio: string
  savedImageRatio: string
  savedAudioRatio: string
  savedAudioCompletionRatio: string
  savedBillingMode: string
  savedBillingExpr: string
  modelPrice: string
  modelRatio: string
  cacheRatio: string
  createCacheRatio: string
  completionRatio: string
  imageRatio: string
  audioRatio: string
  audioCompletionRatio: string
  billingMode: string
  billingExpr: string
  candidateModelNames?: string[]
  candidateModelsLoading?: boolean
  filterMode?: 'all' | 'unset'
  onChange: (field: string, value: string) => void
  onSave: () => void | Promise<void>
  isSaving: boolean
}

export type ModelRatioVisualEditorHandle = {
  commitOpenEditor: () => Promise<boolean>
}

const STORAGE_KEY = 'model-ratio-column-visibility'

const ModelRatioVisualEditorComponent = forwardRef<
  ModelRatioVisualEditorHandle,
  ModelRatioVisualEditorProps
>(function ModelRatioVisualEditor(
  {
    savedModelPrice,
    savedModelRatio,
    savedCacheRatio,
    savedCreateCacheRatio,
    savedCompletionRatio,
    savedImageRatio,
    savedAudioRatio,
    savedAudioCompletionRatio,
    savedBillingMode,
    savedBillingExpr,
    modelPrice,
    modelRatio,
    cacheRatio,
    createCacheRatio,
    completionRatio,
    imageRatio,
    audioRatio,
    audioCompletionRatio,
    billingMode,
    billingExpr,
    candidateModelNames,
    candidateModelsLoading,
    filterMode = 'all',
    onChange,
    onSave,
    isSaving,
  },
  ref
) {
  const { t } = useTranslation()
  const priceLocks = useModelPriceLocks()
  const { locks, pending: lockPending, toggle: togglePriceLock } = priceLocks
  const isMobile = useMediaQuery('(max-width: 767px)')
  const [sheetOpen, setSheetOpen] = useState(false)
  const [editorOpen, setEditorOpen] = useState(false)
  const [editData, setEditData] = useState<ModelRatioData | null>(null)
  const [sorting, setSorting] = useState<SortingState>([])
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([])
  const [globalFilter, setGlobalFilter] = useState('')
  const [rowSelection, setRowSelection] = useState<RowSelectionState>({})
  const editorPanelRef = useRef<ModelPricingEditorPanelHandle>(null)
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: 20,
  })
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>(
    () => {
      const saved = localStorage.getItem(STORAGE_KEY)
      if (saved) {
        try {
          return safeJsonParse<VisibilityState>(saved, {
            fallback: {
              cacheRatio: false,
              createCacheRatio: false,
              imageRatio: false,
              audioRatio: false,
              audioCompletionRatio: false,
            },
            silent: true,
          })
        } catch {
          return {
            cacheRatio: false,
            createCacheRatio: false,
            imageRatio: false,
            audioRatio: false,
            audioCompletionRatio: false,
          }
        }
      }
      return {
        cacheRatio: false,
        createCacheRatio: false,
        imageRatio: false,
        audioRatio: false,
        audioCompletionRatio: false,
      }
    }
  )

  useEffect(() => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(columnVisibility))
  }, [columnVisibility])

  const savedPricing = useMemo(
    () => ({
      ModelPrice: savedModelPrice,
      ModelRatio: savedModelRatio,
      CacheRatio: savedCacheRatio,
      CreateCacheRatio: savedCreateCacheRatio,
      CompletionRatio: savedCompletionRatio,
      ImageRatio: savedImageRatio,
      AudioRatio: savedAudioRatio,
      AudioCompletionRatio: savedAudioCompletionRatio,
      'billing_setting.billing_mode': savedBillingMode,
      'billing_setting.billing_expr': savedBillingExpr,
      ...Object.fromEntries(
        priceLocks.options?.map(({ key, value }) => [key, value]) || []
      ),
    }),
    [
      savedModelPrice,
      savedModelRatio,
      savedCacheRatio,
      savedCreateCacheRatio,
      savedCompletionRatio,
      savedImageRatio,
      savedAudioRatio,
      savedAudioCompletionRatio,
      savedBillingMode,
      savedBillingExpr,
      priceLocks.options,
    ]
  )

  const changePricingField = useCallback(
    (field: string, value: string) => {
      const next = safeJsonParse<Record<string, unknown>>(value, {
        fallback: {},
        silent: true,
      })
      const saved = safeJsonParse<Record<string, unknown>>(
        savedPricing[field as keyof typeof savedPricing],
        { fallback: {}, silent: true }
      )
      for (const name of Object.keys(locks)) {
        if (Object.hasOwn(saved, name)) next[name] = saved[name]
        else delete next[name]
      }
      onChange(field, JSON.stringify(next, null, 2))
    },
    [locks, onChange, savedPricing]
  )

  const handleToggleLock = useCallback(
    async (name: string) => {
      const result = await togglePriceLock(name)
      if (!result?.locked) return
      // Discard this model's existing draft now, so unlocking cannot revive it.
      const drafts: Record<string, string> = {
        ModelPrice: modelPrice,
        ModelRatio: modelRatio,
        CacheRatio: cacheRatio,
        CreateCacheRatio: createCacheRatio,
        CompletionRatio: completionRatio,
        ImageRatio: imageRatio,
        AudioRatio: audioRatio,
        AudioCompletionRatio: audioCompletionRatio,
        'billing_setting.billing_mode': billingMode,
        'billing_setting.billing_expr': billingExpr,
      }
      const saved = Object.fromEntries(
        result.options.map(({ key, value }) => [key, value])
      )
      for (const [field, value] of Object.entries(drafts)) {
        const next = safeJsonParse<Record<string, unknown>>(value, {
          fallback: {},
          silent: true,
        })
        const canonical = safeJsonParse<Record<string, unknown>>(
          saved[field] || '{}',
          { fallback: {}, silent: true }
        )
        if (Object.hasOwn(canonical, name)) next[name] = canonical[name]
        else delete next[name]
        onChange(field, JSON.stringify(next, null, 2))
      }
    },
    [
      togglePriceLock,
      modelPrice,
      modelRatio,
      cacheRatio,
      createCacheRatio,
      completionRatio,
      imageRatio,
      audioRatio,
      audioCompletionRatio,
      billingMode,
      billingExpr,
      onChange,
    ]
  )

  const models = useMemo(() => {
    const savedRows = buildModelSnapshots({
      modelPrice: savedModelPrice,
      modelRatio: savedModelRatio,
      cacheRatio: savedCacheRatio,
      createCacheRatio: savedCreateCacheRatio,
      completionRatio: savedCompletionRatio,
      imageRatio: savedImageRatio,
      audioRatio: savedAudioRatio,
      audioCompletionRatio: savedAudioCompletionRatio,
      billingMode: savedBillingMode,
      billingExpr: savedBillingExpr,
    })
    const draftRows = buildModelSnapshots({
      modelPrice,
      modelRatio,
      cacheRatio,
      createCacheRatio,
      completionRatio,
      imageRatio,
      audioRatio,
      audioCompletionRatio,
      billingMode,
      billingExpr,
    })

    const savedByName = new Map(
      (priceLocks.snapshots || savedRows).map((row) => [row.name, row])
    )
    const draftByName = new Map(draftRows.map((row) => [row.name, row]))
    const modelNames =
      filterMode === 'unset'
        ? new Set([...(candidateModelNames ?? []), ...Object.keys(locks)])
        : new Set([
            ...savedByName.keys(),
            ...draftByName.keys(),
            ...Object.keys(locks),
          ])

    return [...modelNames]
      .map((name) => {
        const saved = savedByName.get(name)
        const draft = draftByName.get(name)
        const displayed = saved ??
          (locks[name] ? undefined : draft) ?? {
            name,
            billingMode: 'per-token',
            hasConflict: false,
          }
        const savedSignature = getSnapshotSignature(saved)
        const draftSignature = getSnapshotSignature(draft)

        return {
          ...displayed,
          saved,
          draft: locks[name] ? saved : draft,
          isDraftChanged: savedSignature !== draftSignature,
          isDraftDeleted: Boolean(saved && !draft && !locks[name]),
          isDraftNew: Boolean(!saved && draft),
        }
      })
      .filter((row) => !row.isDraftDeleted)
      .filter((row) => filterMode !== 'unset' || isBasePricingUnset(row.saved))
      .sort((a, b) => a.name.localeCompare(b.name))
  }, [
    locks,
    priceLocks.snapshots,
    candidateModelNames,
    filterMode,
    savedModelPrice,
    savedModelRatio,
    savedCacheRatio,
    savedCreateCacheRatio,
    savedCompletionRatio,
    savedImageRatio,
    savedAudioRatio,
    savedAudioCompletionRatio,
    savedBillingMode,
    savedBillingExpr,
    modelPrice,
    modelRatio,
    cacheRatio,
    createCacheRatio,
    completionRatio,
    imageRatio,
    audioRatio,
    audioCompletionRatio,
    billingMode,
    billingExpr,
  ])

  const modeCounts = useMemo(
    () =>
      models.reduce(
        (acc, model) => {
          const mode =
            model.billingMode === 'per-request' ||
            model.billingMode === 'tiered_expr'
              ? model.billingMode
              : 'per-token'
          acc[mode] += 1
          return acc
        },
        {
          'per-token': 0,
          'per-request': 0,
          tiered_expr: 0,
        } as Record<'per-token' | 'per-request' | 'tiered_expr', number>
      ),
    [models]
  )

  const handleEdit = useCallback(
    (model: ModelRow) => {
      const editableModel = locks[model.name]
        ? (model.saved ?? model)
        : (model.draft ?? model.saved ?? model)
      let editBillingMode: PricingMode = 'per-token'
      if (editableModel.billingMode === 'tiered_expr') {
        editBillingMode = 'tiered_expr'
      } else if (editableModel.price && editableModel.price !== '') {
        editBillingMode = 'per-request'
      }
      setEditData({
        name: editableModel.name,
        price: editableModel.price,
        ratio: editableModel.ratio,
        cacheRatio: editableModel.cacheRatio,
        createCacheRatio: editableModel.createCacheRatio,
        completionRatio: editableModel.completionRatio,
        imageRatio: editableModel.imageRatio,
        audioRatio: editableModel.audioRatio,
        audioCompletionRatio: editableModel.audioCompletionRatio,
        billingMode: editBillingMode,
        billingExpr: editableModel.billingExpr,
        requestRuleExpr: editableModel.requestRuleExpr,
      })
      setEditorOpen(true)
      if (isMobile) setSheetOpen(true)
    },
    [isMobile, locks]
  )

  useEffect(() => {
    if (!editData || !locks[editData.name]) return
    const model = models.find((row) => row.name === editData.name)
    if (
      model &&
      getSnapshotSignature({ ...editData, hasConflict: false }) !==
        getSnapshotSignature(model.saved || model)
    ) {
      handleEdit(model)
    }
  }, [editData, locks, models, handleEdit])

  const handleAdd = useCallback(() => {
    setEditData(null)
    setEditorOpen(true)
    if (isMobile) setSheetOpen(true)
  }, [isMobile])

  const handleGlobalFilterChange = useCallback<OnChangeFn<string>>(
    (updater) => {
      setGlobalFilter((previous) => {
        const next = typeof updater === 'function' ? updater(previous) : updater
        if (next !== previous) {
          setEditData(null)
          setEditorOpen(false)
          setSheetOpen(false)
        }
        return next
      })
    },
    []
  )

  const handleDelete = useCallback(
    (name: string) => {
      if (locks[name] || lockPending) {
        toast.warning(
          t(
            'Locked model prices are unchanged. Unlock the model to edit pricing.'
          )
        )
        return
      }
      const priceMap = safeJsonParse<Record<string, number>>(modelPrice, {
        fallback: {},
        silent: true,
      })
      const ratioMap = safeJsonParse<Record<string, number>>(modelRatio, {
        fallback: {},
        silent: true,
      })
      const cacheMap = safeJsonParse<Record<string, number>>(cacheRatio, {
        fallback: {},
        silent: true,
      })
      const createCacheMap = safeJsonParse<Record<string, number>>(
        createCacheRatio,
        { fallback: {}, silent: true }
      )
      const completionMap = safeJsonParse<Record<string, number>>(
        completionRatio,
        { fallback: {}, silent: true }
      )
      const imageMap = safeJsonParse<Record<string, number>>(imageRatio, {
        fallback: {},
        silent: true,
      })
      const audioMap = safeJsonParse<Record<string, number>>(audioRatio, {
        fallback: {},
        silent: true,
      })
      const audioCompletionMap = safeJsonParse<Record<string, number>>(
        audioCompletionRatio,
        { fallback: {}, silent: true }
      )
      const billingModeMap = safeJsonParse<Record<string, string>>(
        billingMode,
        { fallback: {}, silent: true }
      )
      const billingExprMap = safeJsonParse<Record<string, string>>(
        billingExpr,
        { fallback: {}, silent: true }
      )

      delete priceMap[name]
      delete ratioMap[name]
      delete cacheMap[name]
      delete createCacheMap[name]
      delete completionMap[name]
      delete imageMap[name]
      delete audioMap[name]
      delete audioCompletionMap[name]
      delete billingModeMap[name]
      delete billingExprMap[name]

      changePricingField('ModelPrice', JSON.stringify(priceMap, null, 2))
      changePricingField('ModelRatio', JSON.stringify(ratioMap, null, 2))
      changePricingField('CacheRatio', JSON.stringify(cacheMap, null, 2))
      changePricingField(
        'CreateCacheRatio',
        JSON.stringify(createCacheMap, null, 2)
      )
      changePricingField(
        'CompletionRatio',
        JSON.stringify(completionMap, null, 2)
      )
      changePricingField('ImageRatio', JSON.stringify(imageMap, null, 2))
      changePricingField('AudioRatio', JSON.stringify(audioMap, null, 2))
      changePricingField(
        'AudioCompletionRatio',
        JSON.stringify(audioCompletionMap, null, 2)
      )
      changePricingField(
        'billing_setting.billing_mode',
        JSON.stringify(billingModeMap, null, 2)
      )
      changePricingField(
        'billing_setting.billing_expr',
        JSON.stringify(billingExprMap, null, 2)
      )

      if (editData?.name === name) {
        setEditData(null)
        setEditorOpen(false)
        setSheetOpen(false)
      }
    },
    [
      modelPrice,
      modelRatio,
      cacheRatio,
      createCacheRatio,
      completionRatio,
      imageRatio,
      audioRatio,
      audioCompletionRatio,
      billingMode,
      billingExpr,
      changePricingField,
      locks,
      lockPending,
      t,
      editData,
    ]
  )

  const columns = useMemo(
    () =>
      buildModelRatioColumns({
        onDelete: handleDelete,
        onEdit: handleEdit,
        locks,
        lockPending: lockPending || isSaving,
        onToggleLock: handleToggleLock,
        deleteDisabled: filterMode === 'unset',
        t,
      }),
    [
      handleEdit,
      handleDelete,
      filterMode,
      t,
      locks,
      lockPending,
      isSaving,
      handleToggleLock,
    ]
  )

  const ensurePageInRange = useCallback((pageCount: number) => {
    setPagination((prev) =>
      pageCount > 0 && prev.pageIndex >= pageCount
        ? { ...prev, pageIndex: pageCount - 1 }
        : prev
    )
  }, [])

  const { table } = useDataTable({
    data: models,
    columns,
    getRowId: (row) => row.name,
    ensurePageInRange,
    sorting,
    columnFilters,
    globalFilter,
    columnVisibility,
    pagination,
    rowSelection,
    enableRowSelection: true,
    onSortingChange: setSorting,
    onColumnFiltersChange: setColumnFilters,
    onGlobalFilterChange: handleGlobalFilterChange,
    onColumnVisibilityChange: setColumnVisibility,
    onPaginationChange: setPagination,
    onRowSelectionChange: setRowSelection,
    autoResetPageIndex: false,
    globalFilterFn: (row, _columnId, filterValue) => {
      const searchValue = String(filterValue).toLowerCase()
      return row.original.name.toLowerCase().includes(searchValue)
    },
  })

  const persistPricingData = useCallback(
    (data: ModelRatioData, targetNames: string[] = [data.name]) => {
      const priceMap = safeJsonParse<Record<string, number>>(modelPrice, {
        fallback: {},
        silent: true,
      })
      const ratioMap = safeJsonParse<Record<string, number>>(modelRatio, {
        fallback: {},
        silent: true,
      })
      const cacheMap = safeJsonParse<Record<string, number>>(cacheRatio, {
        fallback: {},
        silent: true,
      })
      const createCacheMap = safeJsonParse<Record<string, number>>(
        createCacheRatio,
        { fallback: {}, silent: true }
      )
      const completionMap = safeJsonParse<Record<string, number>>(
        completionRatio,
        { fallback: {}, silent: true }
      )
      const imageMap = safeJsonParse<Record<string, number>>(imageRatio, {
        fallback: {},
        silent: true,
      })
      const audioMap = safeJsonParse<Record<string, number>>(audioRatio, {
        fallback: {},
        silent: true,
      })
      const audioCompletionMap = safeJsonParse<Record<string, number>>(
        audioCompletionRatio,
        { fallback: {}, silent: true }
      )
      const billingModeMap = safeJsonParse<Record<string, string>>(
        billingMode,
        { fallback: {}, silent: true }
      )
      const billingExprMap = safeJsonParse<Record<string, string>>(
        billingExpr,
        { fallback: {}, silent: true }
      )

      const setIfPresent = (
        target: Record<string, number>,
        name: string,
        value: string | undefined
      ) => {
        if (!value || value === '') return
        const parsed = Number.parseFloat(value)
        if (Number.isFinite(parsed)) target[name] = parsed
      }

      const skipped = targetNames.filter((name) => locks[name])
      if (skipped.length) {
        toast.warning(
          t('Skipped locked models: {{models}}', { models: skipped.join(', ') })
        )
      }
      targetNames.forEach((name) => {
        if (locks[name]) return
        delete priceMap[name]
        delete ratioMap[name]
        delete cacheMap[name]
        delete createCacheMap[name]
        delete completionMap[name]
        delete imageMap[name]
        delete audioMap[name]
        delete audioCompletionMap[name]
        delete billingModeMap[name]
        delete billingExprMap[name]

        if (data.billingMode === 'tiered_expr') {
          const combined = combineBillingExpr(
            data.billingExpr || '',
            data.requestRuleExpr || ''
          )
          if (combined) {
            billingModeMap[name] = 'tiered_expr'
            billingExprMap[name] = combined
          }
          // Always serialize ratio/price values for tiered_expr models so they
          // serve as fallback during multi-instance sync delays. The backend's
          // ModelPriceHelper checks billing_mode first, so these values are
          // only consulted when billing_setting hasn't propagated yet.
          setIfPresent(priceMap, name, data.price)
          setIfPresent(ratioMap, name, data.ratio)
          setIfPresent(cacheMap, name, data.cacheRatio)
          setIfPresent(createCacheMap, name, data.createCacheRatio)
          setIfPresent(completionMap, name, data.completionRatio)
          setIfPresent(imageMap, name, data.imageRatio)
          setIfPresent(audioMap, name, data.audioRatio)
          setIfPresent(audioCompletionMap, name, data.audioCompletionRatio)
        } else if (data.price && data.price !== '') {
          setIfPresent(priceMap, name, data.price)
        } else {
          setIfPresent(ratioMap, name, data.ratio)
          setIfPresent(cacheMap, name, data.cacheRatio)
          setIfPresent(createCacheMap, name, data.createCacheRatio)
          setIfPresent(completionMap, name, data.completionRatio)
          setIfPresent(imageMap, name, data.imageRatio)
          setIfPresent(audioMap, name, data.audioRatio)
          setIfPresent(audioCompletionMap, name, data.audioCompletionRatio)
        }
      })

      changePricingField('ModelPrice', JSON.stringify(priceMap, null, 2))
      changePricingField('ModelRatio', JSON.stringify(ratioMap, null, 2))
      changePricingField('CacheRatio', JSON.stringify(cacheMap, null, 2))
      changePricingField(
        'CreateCacheRatio',
        JSON.stringify(createCacheMap, null, 2)
      )
      changePricingField(
        'CompletionRatio',
        JSON.stringify(completionMap, null, 2)
      )
      changePricingField('ImageRatio', JSON.stringify(imageMap, null, 2))
      changePricingField('AudioRatio', JSON.stringify(audioMap, null, 2))
      changePricingField(
        'AudioCompletionRatio',
        JSON.stringify(audioCompletionMap, null, 2)
      )
      changePricingField(
        'billing_setting.billing_mode',
        JSON.stringify(billingModeMap, null, 2)
      )
      changePricingField(
        'billing_setting.billing_expr',
        JSON.stringify(billingExprMap, null, 2)
      )
      return skipped
    },
    [
      modelPrice,
      modelRatio,
      cacheRatio,
      createCacheRatio,
      completionRatio,
      imageRatio,
      audioRatio,
      audioCompletionRatio,
      billingMode,
      billingExpr,
      changePricingField,
      locks,
      t,
    ]
  )

  const handleBatchCopy = useCallback(async () => {
    if (lockPending || isSaving) return
    if (!editData) {
      toast.error(t('Open a source model first'))
      return
    }

    let sourceData = editData
    if (locks[editData.name]) {
      const saved = models.find((model) => model.name === editData.name)?.saved
      sourceData = saved
        ? { ...saved, billingMode: saved.billingMode as PricingMode }
        : { name: editData.name, billingMode: 'per-token' }
    }
    if (editorOpen && editorPanelRef.current && !locks[editData.name]) {
      const committed = await editorPanelRef.current.commitDraft()
      if (!committed) return
      sourceData = committed
      setEditData(committed)
    }

    const targetNames = table
      .getFilteredSelectedRowModel()
      .rows.map((row) => row.original.name)

    if (targetNames.length === 0) {
      toast.error(t('Select at least one target model'))
      return
    }

    // Persist to the source model too, so targets never carry pricing the
    // source itself would lose if the editor draft were abandoned.
    const skipped = persistPricingData(sourceData, [
      ...new Set([sourceData.name, ...targetNames]),
    ])
    table.resetRowSelection()
    if (skipped.length === 0) {
      toast.success(
        t('Applied {{name}} pricing to {{count}} models', {
          name: sourceData.name,
          count: targetNames.length,
        })
      )
    }
  }, [
    editData,
    editorOpen,
    persistPricingData,
    t,
    table,
    locks,
    lockPending,
    isSaving,
    models,
  ])

  useImperativeHandle(
    ref,
    () => ({
      commitOpenEditor: async () => {
        if (lockPending) return false
        if (editData && locks[editData.name]) {
          toast.warning(
            t(
              'Locked model prices are unchanged. Unlock the model to edit pricing.'
            )
          )
          return true
        }
        if (!editorOpen || !editorPanelRef.current) return true
        const data = await editorPanelRef.current.commitDraft()
        if (!data) return false
        persistPricingData(data)
        setEditData(data)
        return true
      },
    }),
    [editorOpen, persistPricingData, editData, locks, lockPending, t]
  )

  const hasRows = table.getRowModel().rows.length > 0

  let emptyStateText = t('No models configured. Use Add model to get started.')
  if (table.getState().globalFilter) {
    emptyStateText = t('No models match your search')
  } else if (filterMode === 'unset') {
    emptyStateText = candidateModelsLoading
      ? t('Loading...')
      : t('No models with unset prices')
  }

  return (
    <div className='flex flex-col gap-4'>
      <div className='grid h-[clamp(720px,calc(100vh-12rem),900px)] min-h-0 gap-4 md:grid-cols-[minmax(300px,0.72fr)_minmax(520px,1.28fr)] xl:grid-cols-[minmax(320px,0.68fr)_minmax(640px,1.32fr)]'>
        <div className='flex min-h-0 min-w-0 flex-col gap-3'>
          <DataTableToolbar
            table={table}
            searchPlaceholder={t('Search models...')}
            filters={[
              {
                columnId: 'billingMode',
                title: t('Mode'),
                options: [
                  {
                    label: 'Per-token',
                    value: 'per-token',
                    count: modeCounts['per-token'],
                  },
                  {
                    label: 'Per-request',
                    value: 'per-request',
                    count: modeCounts['per-request'],
                  },
                  {
                    label: 'Expression',
                    value: 'tiered_expr',
                    count: modeCounts.tiered_expr,
                  },
                ],
              },
            ]}
            preActions={
              filterMode === 'unset' ? undefined : (
                <Button onClick={handleAdd}>
                  <Plus data-icon='inline-start' />
                  {t('Add model')}
                </Button>
              )
            }
          />

          {!hasRows ? (
            <div className='text-muted-foreground rounded-lg border border-dashed p-8 text-center'>
              {emptyStateText}
            </div>
          ) : (
            <DataTableView
              table={table}
              containerClassName='min-h-0 flex-1 rounded-md'
              tableContainerClassName='h-full'
              tableClassName='min-w-[852px] table-fixed'
              tableHeaderClassName='[&_tr]:border-b-0'
              splitHeaderScrollClassName='h-full'
              bodyContainerClassName='[scrollbar-gutter:stable]'
              splitHeader
              pinnedColumns={[
                {
                  columnId: 'actions',
                  side: 'right',
                },
              ]}
              colgroup={
                <colgroup>
                  <col className='w-9' />
                  <col className='w-[300px]' />
                  <col className='w-[120px]' />
                  <col className='w-[300px]' />
                  <col className='w-auto' />
                </colgroup>
              }
              renderRow={(row, { getCellClassName }) => (
                <DataTableRow
                  key={row.id}
                  row={row}
                  className={
                    editData?.name === row.original.name
                      ? 'bg-muted/45 hover:bg-muted/50 data-[state=selected]:bg-muted group'
                      : 'group'
                  }
                  getColumnClassName={(columnId) =>
                    columnId === 'actions' &&
                    editData?.name === row.original.name
                      ? getCellClassName(columnId, 'bg-muted')
                      : getCellClassName(columnId)
                  }
                  onClick={(event) => {
                    const target = event.target as HTMLElement
                    if (target.closest('button, input, [role="checkbox"]')) {
                      return
                    }
                    handleEdit(row.original)
                  }}
                />
              )}
            />
          )}

          {hasRows && <DataTablePagination table={table} />}
        </div>

        <div className='hidden min-h-0 min-w-0 md:block'>
          {editorOpen ? (
            <ModelPricingEditorPanel
              ref={editorPanelRef}
              editData={editData}
              onSave={onSave}
              isSaving={isSaving || lockPending}
              locked={!!editData && locks[editData.name] === true}
              lockPending={lockPending || isSaving}
              onToggleLock={
                editData ? () => handleToggleLock(editData.name) : undefined
              }
              className='h-full min-h-0'
            />
          ) : (
            <div className='bg-card text-muted-foreground flex h-full min-h-0 flex-col items-center justify-center gap-3 rounded-none border border-dashed p-6 text-center'>
              <div className='text-foreground text-base font-medium'>
                {t('Select a model to edit pricing')}
              </div>
              <p className='max-w-sm text-sm'>
                {t(
                  'Use the full-width table to scan prices, then select a row to edit it here.'
                )}
              </p>
              {filterMode !== 'unset' && (
                <Button variant='outline' onClick={handleAdd}>
                  <Plus data-icon='inline-start' />
                  {t('Add model')}
                </Button>
              )}
            </div>
          )}
        </div>
      </div>

      <DataTableBulkActions table={table} entityName={t('model')}>
        <Button
          size='sm'
          disabled={!editData || lockPending || isSaving}
          onClick={handleBatchCopy}
        >
          <Copy data-icon='inline-start' />
          {editData
            ? t('Copy {{name}} pricing', { name: editData.name })
            : t('Open a source model first')}
        </Button>
      </DataTableBulkActions>

      {isMobile && (
        <ModelPricingSheet
          ref={editorPanelRef}
          open={sheetOpen}
          onOpenChange={setSheetOpen}
          editData={editData}
          onSave={onSave}
          isSaving={isSaving || lockPending}
          locked={!!editData && locks[editData.name] === true}
          lockPending={lockPending || isSaving}
          onToggleLock={
            editData ? () => handleToggleLock(editData.name) : undefined
          }
        />
      )}
    </div>
  )
})

export const ModelRatioVisualEditor = memo(
  ModelRatioVisualEditorComponent,
  // Custom equality check - only re-render if JSON props actually changed
  (prevProps, nextProps) => {
    return (
      prevProps.savedModelPrice === nextProps.savedModelPrice &&
      prevProps.savedModelRatio === nextProps.savedModelRatio &&
      prevProps.savedCacheRatio === nextProps.savedCacheRatio &&
      prevProps.savedCreateCacheRatio === nextProps.savedCreateCacheRatio &&
      prevProps.savedCompletionRatio === nextProps.savedCompletionRatio &&
      prevProps.savedImageRatio === nextProps.savedImageRatio &&
      prevProps.savedAudioRatio === nextProps.savedAudioRatio &&
      prevProps.savedAudioCompletionRatio ===
        nextProps.savedAudioCompletionRatio &&
      prevProps.savedBillingMode === nextProps.savedBillingMode &&
      prevProps.savedBillingExpr === nextProps.savedBillingExpr &&
      prevProps.modelPrice === nextProps.modelPrice &&
      prevProps.modelRatio === nextProps.modelRatio &&
      prevProps.cacheRatio === nextProps.cacheRatio &&
      prevProps.createCacheRatio === nextProps.createCacheRatio &&
      prevProps.completionRatio === nextProps.completionRatio &&
      prevProps.imageRatio === nextProps.imageRatio &&
      prevProps.audioRatio === nextProps.audioRatio &&
      prevProps.audioCompletionRatio === nextProps.audioCompletionRatio &&
      prevProps.billingMode === nextProps.billingMode &&
      prevProps.billingExpr === nextProps.billingExpr &&
      prevProps.candidateModelNames === nextProps.candidateModelNames &&
      prevProps.candidateModelsLoading === nextProps.candidateModelsLoading &&
      prevProps.filterMode === nextProps.filterMode &&
      prevProps.onChange === nextProps.onChange &&
      prevProps.onSave === nextProps.onSave &&
      prevProps.isSaving === nextProps.isSaving
    )
  }
)
