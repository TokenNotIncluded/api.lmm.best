/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  AlertCircle,
  ArrowLeft,
  ArrowRight,
  Copy,
  Layers,
  Pencil,
  Trash2,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { useMediaQuery } from '@/hooks'
import { cn } from '@/lib/utils'

import {
  createDiscountCodes,
  deleteDiscountCode,
  listDiscountCodes,
  updateDiscountCode,
  updateDiscountCodeStatus,
} from './api.js'
import {
  DISCOUNT_CODE_ENABLED_STATUS,
  parseDiscountCodeMaxUses,
} from './availability.js'
import { DiscountCodeDeleteDialog } from './components/discount-code-delete-dialog.js'
import { DiscountCodeStatusBadge } from './components/discount-code-status-badge.js'
import { DiscountCodesMobileList } from './components/discount-codes-mobile-list.js'
import {
  CleanupExhaustedCodesDialog,
  DiscountCodesActions,
} from './discount-codes-actions.js'
import {
  type DiscountCodeFormErrors,
  type DiscountCodeFormValues,
  isDiscountCodeFormValid,
  validateDiscountCodeForm,
} from './form-validation.js'
import { buildDiscountCodeLink } from './share-link.js'
import type {
  DiscountCode,
  DiscountCodeBatchInput,
  DiscountCodeInput,
} from './types.js'
import { useDiscountCodeTranslations } from './use-discount-code-translations.js'
import { useExhaustedDiscountCodeCleanup } from './use-exhausted-discount-code-cleanup.js'

const DISABLED = 2
const PAGE_SIZE = 20

type FormState = DiscountCodeFormValues

async function copyDiscountLinks(
  codes: string[],
  t: ReturnType<typeof useTranslation>['t']
) {
  if (codes.length === 0 || !navigator.clipboard) return
  try {
    await navigator.clipboard.writeText(
      codes.map((code) => buildDiscountCodeLink(code)).join('\n')
    )
    toast.success(t('Copied to clipboard'))
  } catch {
    toast.error(t('Unable to copy'))
  }
}

const emptyForm: FormState = {
  code: '',
  name: '',
  count: '1',
  discount_percent: '10',
  min_amount: '0',
  max_uses: '1',
  starts_time: '',
  expired_time: '',
}

function toDateInput(timestamp: number) {
  if (!timestamp) return ''
  const date = new Date(timestamp * 1000)
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function toTimestamp(value: string) {
  if (!value) return 0
  const timestamp = Math.floor(new Date(value).getTime() / 1000)
  return Number.isFinite(timestamp) ? timestamp : 0
}

function formFromRow(row?: DiscountCode): FormState {
  if (!row) return emptyForm
  return {
    code: row.code,
    name: row.name,
    count: '1',
    discount_percent: String(row.discount_percent),
    min_amount: String(row.min_amount),
    max_uses: String(row.max_uses),
    starts_time: toDateInput(row.starts_time),
    expired_time: toDateInput(row.expired_time),
  }
}

function formatDate(timestamp: number) {
  if (!timestamp) return '—'
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(timestamp * 1000))
}

/** Inline, field-level message shown directly under its own input. */
function FieldError({ id, message }: { id: string; message?: string }) {
  if (!message) return null
  return (
    <p id={id} role='alert' className='text-destructive text-xs'>
      {message}
    </p>
  )
}

function TableSkeleton() {
  return (
    <div className='divide-border overflow-hidden rounded-lg border'>
      {[1, 2, 3, 4, 5].map((item) => (
        <div
          key={item}
          className='flex items-center gap-4 border-b px-3 py-4 last:border-b-0'
        >
          <Skeleton className='size-5 shrink-0 rounded-[4px]' />
          <Skeleton className='h-4 w-40' />
          <Skeleton className='hidden h-4 w-32 sm:block' />
          <Skeleton className='ml-auto h-5 w-20 rounded-md' />
        </div>
      ))}
    </div>
  )
}

// pi-lens-ignore: high-fan-out, high-complexity
export function DiscountCodes() {
  const { t } = useTranslation()
  useDiscountCodeTranslations()
  const queryClient = useQueryClient()
  const isMobile = useMediaQuery('(max-width: 640px)')
  const [keyword, setKeyword] = useState('')
  const [page, setPage] = useState(1)
  const [sheetOpen, setSheetOpen] = useState(false)
  const [editing, setEditing] = useState<DiscountCode>()
  const [form, setForm] = useState<FormState>(emptyForm)
  const [formErrors, setFormErrors] = useState<DiscountCodeFormErrors>({})
  const [selectedIds, setSelectedIds] = useState<Set<number>>(new Set())
  const [generatedCodes, setGeneratedCodes] = useState<string[]>([])
  const [generatedCodesOpen, setGeneratedCodesOpen] = useState(false)
  const [pendingDeleteId, setPendingDeleteId] = useState<number | null>(null)

  const query = useQuery({
    queryKey: ['discount-codes', page, keyword],
    queryFn: () => listDiscountCodes({ page, pageSize: PAGE_SIZE, keyword }),
    placeholderData: (previous) => previous,
  })
  const rows = query.data?.data?.items ?? []
  const total = query.data?.data?.total ?? 0
  const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE))
  const allRowsSelected =
    rows.length > 0 && rows.every((row) => selectedIds.has(row.id))
  const pendingDeleteRow =
    rows.find((row) => row.id === pendingDeleteId) ?? null

  const refresh = () =>
    queryClient.invalidateQueries({ queryKey: ['discount-codes'] })
  const cleanup = useExhaustedDiscountCodeCleanup(() => {
    setSelectedIds(new Set())
    void refresh()
  })

  const saveMutation = useMutation({
    mutationFn: async (input: DiscountCodeInput | DiscountCodeBatchInput) =>
      editing
        ? updateDiscountCode({
            ...(input as DiscountCodeInput),
            id: editing.id,
          })
        : createDiscountCodes(input as DiscountCodeBatchInput),
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(result.message || t('Unable to save discount code'))
        return
      }
      const createdCount = Array.isArray(result.data) ? result.data.length : 0
      toast.success(
        editing
          ? t('Discount code saved')
          : t('Created {{count}} discount codes.', { count: createdCount })
      )
      if (!editing && Array.isArray(result.data)) {
        setGeneratedCodes(result.data.map((code) => code.code))
        setSelectedIds(new Set(result.data.map((code) => code.id)))
        setGeneratedCodesOpen(true)
      }
      setSheetOpen(false)
      refresh()
    },
    onError: (error) =>
      toast.error(
        error instanceof Error
          ? error.message
          : t('Unable to save discount code')
      ),
  })

  const statusMutation = useMutation({
    mutationFn: ({ id, status }: { id: number; status: number }) =>
      updateDiscountCodeStatus(id, status),
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(result.message || t('Unable to update discount code'))
        return
      }
      refresh()
    },
  })

  const deleteMutation = useMutation({
    mutationFn: deleteDiscountCode,
    onSuccess: (result) => {
      setPendingDeleteId(null)
      if (!result.success) {
        toast.error(result.message || t('Unable to delete discount code'))
        return
      }
      toast.success(t('Discount code deleted'))
      refresh()
    },
    onError: () => {
      setPendingDeleteId(null)
      toast.error(t('Unable to delete discount code'))
    },
  })

  const isSaving = saveMutation.isPending
  const maxUses = parseDiscountCodeMaxUses(form.max_uses)
  const batchCount = Number(form.count)

  const validationErrors = useMemo(
    () => validateDiscountCodeForm(form, { editing: Boolean(editing) }),
    [editing, form]
  )
  // Show a field's error only once the operator has touched it, or after a
  // submit attempt, so a pristine form never opens covered in red.
  const [showAllErrors, setShowAllErrors] = useState(false)
  const visibleErrors: DiscountCodeFormErrors = showAllErrors
    ? validationErrors
    : formErrors
  const canSave = isDiscountCodeFormValid(validationErrors)

  const markTouched = (field: keyof FormState) => {
    setFormErrors((current) => ({
      ...current,
      [field]: validationErrors[field],
    }))
  }

  const openCreate = () => {
    setEditing(undefined)
    setForm({
      ...emptyForm,
      starts_time: toDateInput(Math.floor(Date.now() / 1000)),
      expired_time: '',
    })
    setFormErrors({})
    setShowAllErrors(false)
    setSheetOpen(true)
  }

  const openEdit = (row: DiscountCode) => {
    setEditing(row)
    setForm(formFromRow(row))
    setFormErrors({})
    setShowAllErrors(false)
    setSheetOpen(true)
  }

  const toggleRowSelection = (id: number, checked: boolean) => {
    setSelectedIds((current) => {
      const next = new Set(current)
      if (checked) next.add(id)
      else next.delete(id)
      return next
    })
  }

  const toggleAllRows = (checked: boolean) => {
    setSelectedIds((current) => {
      const next = new Set(current)
      for (const row of rows) {
        if (checked) next.add(row.id)
        else next.delete(row.id)
      }
      return next
    })
  }

  const handleToggleStatus = (row: DiscountCode, enabled: boolean) => {
    statusMutation.mutate({
      id: row.id,
      status: enabled ? DISCOUNT_CODE_ENABLED_STATUS : DISABLED,
    })
  }

  const submit = () => {
    if (!canSave || maxUses === undefined) {
      setShowAllErrors(true)
      return
    }
    if (editing) {
      saveMutation.mutate({
        code: form.code.trim().toUpperCase(),
        name: form.name.trim(),
        discount_percent: Number(form.discount_percent),
        min_amount: Math.max(0, Math.floor(Number(form.min_amount))),
        max_uses: maxUses,
        starts_time: toTimestamp(form.starts_time),
        expired_time: toTimestamp(form.expired_time),
      })
      return
    }
    saveMutation.mutate({
      name: form.name.trim(),
      count: batchCount,
      discount_percent: Number(form.discount_percent),
      min_amount: Math.max(0, Math.floor(Number(form.min_amount))),
      max_uses: maxUses,
      starts_time: toTimestamp(form.starts_time),
      expired_time: toTimestamp(form.expired_time),
    })
  }

  const copyCode = () => {
    void copyDiscountLinks(form.code ? [form.code] : [], t)
  }

  const copySelectedCodes = () => {
    void copyDiscountLinks(
      rows.flatMap((row) => (selectedIds.has(row.id) ? [row.code] : [])),
      t
    )
  }

  const copyOneCode = (code: string) => {
    void copyDiscountLinks([code], t)
  }

  const clearFilters = () => {
    setKeyword('')
    setPage(1)
  }

  const selectedCount = selectedIds.size

  const emptyState = (
    <div className='rounded-lg border p-6'>
      <Empty className='border-none p-0'>
        <EmptyHeader>
          <EmptyMedia variant='icon'>
            {keyword ? (
              <Layers className='size-6' />
            ) : (
              <Copy className='size-6' />
            )}
          </EmptyMedia>
          <EmptyTitle>
            {keyword ? t('No matching discount codes') : t('No discount codes')}
          </EmptyTitle>
          <EmptyDescription>
            {keyword
              ? t(
                  'No codes match "{{keyword}}". Try a different code or name.',
                  { keyword }
                )
              : t(
                  'Create a code and share the link to offer a percentage discount at checkout.'
                )}
          </EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <div className='flex w-full flex-col gap-2 sm:w-auto sm:flex-row sm:justify-center'>
            <Button className='h-11 gap-2 sm:h-9' onClick={openCreate}>
              Create discount code
            </Button>
            {keyword ? (
              <Button
                variant='outline'
                className='h-11 gap-2 sm:h-9'
                onClick={clearFilters}
              >
                Clear search
              </Button>
            ) : null}
          </div>
        </EmptyContent>
      </Empty>
    </div>
  )

  const errorState = (
    <div className='rounded-lg border p-6'>
      <Empty className='border-none p-0'>
        <EmptyHeader>
          <EmptyMedia variant='icon'>
            <AlertCircle className='size-6' />
          </EmptyMedia>
          <EmptyTitle>{t('Unable to load discount codes')}</EmptyTitle>
          <EmptyDescription>
            {query.error instanceof Error && query.error.message
              ? query.error.message
              : t('The discount code service did not respond. Try again.')}
          </EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Button
            className='h-11 w-full gap-2 sm:h-9 sm:w-auto'
            onClick={() => void query.refetch()}
            disabled={query.isFetching}
          >
            {query.isFetching ? t('Retrying...') : t('Try again')}
          </Button>
        </EmptyContent>
      </Empty>
    </div>
  )

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Discount Codes')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <DiscountCodesActions
            selectedCount={selectedCount}
            cleanupPending={cleanup.pending}
            onRefresh={() => void query.refetch()}
            onCopySelected={copySelectedCodes}
            onOpenCleanup={() => cleanup.setOpen(true)}
            onCreate={openCreate}
          />
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='mx-auto w-full max-w-6xl space-y-5'>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Manage percentage discounts for checkout. Codes are validated and applied by the server.'
              )}
            </p>

            <div className='flex flex-wrap items-center gap-2'>
              <Input
                value={keyword}
                onChange={(event) => {
                  setKeyword(event.target.value)
                  setPage(1)
                }}
                placeholder={t('Filter by code or name...')}
                aria-label={t('Filter by code or name...')}
                className='w-full sm:max-w-sm'
              />
              {total > 0 && (
                <span className='text-muted-foreground text-xs tabular-nums'>
                  {t('{{count}} codes', { count: total })}
                </span>
              )}
            </div>

            {query.isLoading ? (
              <TableSkeleton />
            ) : query.isError ? (
              errorState
            ) : rows.length === 0 ? (
              emptyState
            ) : isMobile ? (
              <DiscountCodesMobileList
                rows={rows}
                selectedIds={selectedIds}
                disabled={statusMutation.isPending}
                onToggleRow={toggleRowSelection}
                onToggleStatus={handleToggleStatus}
                onEdit={openEdit}
                onDelete={(row) => setPendingDeleteId(row.id)}
                onCopy={copyOneCode}
              />
            ) : (
              <div className='overflow-hidden rounded-lg border'>
                <table className='w-full table-fixed border-collapse text-sm'>
                  <caption className='sr-only'>
                    {t('Discount codes, {{count}} total', { count: total })}
                  </caption>
                  <thead>
                    <tr className='text-muted-foreground border-b text-xs tracking-wider uppercase'>
                      <th scope='col' className='w-10 px-3 py-3'>
                        <Checkbox
                          checked={allRowsSelected}
                          indeterminate={
                            selectedIds.size > 0 && !allRowsSelected
                          }
                          onCheckedChange={(checked) =>
                            toggleAllRows(checked === true)
                          }
                          aria-label={t('Select all codes')}
                        />
                      </th>
                      <th scope='col' className='px-3 py-3 text-left'>
                        {t('Code')}
                      </th>
                      <th
                        scope='col'
                        className='hidden px-3 py-3 text-left lg:table-cell'
                      >
                        {t('Name')}
                      </th>
                      <th scope='col' className='px-3 py-3 text-right'>
                        {t('Discount')}
                      </th>
                      <th
                        scope='col'
                        className='hidden px-3 py-3 text-right md:table-cell'
                      >
                        {t('Used')}
                      </th>
                      <th scope='col' className='px-3 py-3 text-left'>
                        {t('Status')}
                      </th>
                      <th
                        scope='col'
                        className='hidden px-3 py-3 text-left xl:table-cell'
                      >
                        {t('Validity')}
                      </th>
                      <th scope='col' className='w-24 px-3 py-3 text-right'>
                        <span className='sr-only'>{t('Actions')}</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((row) => (
                      <tr
                        key={row.id}
                        className={cn(
                          'border-b transition-colors last:border-b-0',
                          'hover:bg-muted/50',
                          selectedIds.has(row.id) && 'bg-muted/40'
                        )}
                      >
                        <td className='px-3 py-3 align-middle'>
                          <Checkbox
                            checked={selectedIds.has(row.id)}
                            onCheckedChange={(checked) =>
                              toggleRowSelection(row.id, checked === true)
                            }
                            aria-label={`${t('Select code')} ${row.code}`}
                          />
                        </td>
                        <td className='px-3 py-3 align-middle'>
                          <div className='truncate font-mono font-medium'>
                            {row.code}
                          </div>
                          <div className='text-muted-foreground truncate text-xs lg:hidden'>
                            {row.name}
                          </div>
                        </td>
                        <td className='hidden truncate px-3 py-3 align-middle lg:table-cell'>
                          {row.name}
                        </td>
                        <td className='px-3 py-3 text-right align-middle font-medium tabular-nums'>
                          {row.discount_percent}%
                        </td>
                        <td className='hidden px-3 py-3 text-right align-middle tabular-nums md:table-cell'>
                          {row.used_count}{' '}
                          <span className='text-muted-foreground'>
                            /{' '}
                            {row.max_uses > 0 ? row.max_uses : t('No maximum')}
                          </span>
                        </td>
                        <td className='px-3 py-3 align-middle'>
                          <div className='flex flex-wrap items-center gap-2'>
                            <Switch
                              size='sm'
                              checked={
                                row.status === DISCOUNT_CODE_ENABLED_STATUS
                              }
                              disabled={statusMutation.isPending}
                              aria-label={`${row.code} ${t('Enabled')}`}
                              onCheckedChange={(checked) =>
                                handleToggleStatus(row, checked)
                              }
                            />
                            <DiscountCodeStatusBadge code={row} />
                          </div>
                        </td>
                        <td className='text-muted-foreground hidden px-3 py-3 align-middle text-xs xl:table-cell'>
                          <p>
                            {t('Starts')}: {formatDate(row.starts_time)}
                          </p>
                          <p>
                            {t('Expires')}:{' '}
                            {row.expired_time
                              ? formatDate(row.expired_time)
                              : t('Never expires')}
                          </p>
                        </td>
                        <td className='px-3 py-3 align-middle'>
                          <div className='flex justify-end gap-1'>
                            <Button
                              variant='ghost'
                              size='icon-sm'
                              className='size-9 sm:size-7'
                              onClick={() => copyOneCode(row.code)}
                              aria-label={t('Copy')}
                              title={t('Copy share link')}
                            >
                              <Copy className='size-4' />
                            </Button>
                            <Button
                              variant='ghost'
                              size='icon-sm'
                              className='size-9 sm:size-7'
                              onClick={() => openEdit(row)}
                              aria-label={t('Edit')}
                            >
                              <Pencil className='size-4' />
                            </Button>
                            <Button
                              variant='ghost'
                              size='icon-sm'
                              className='text-destructive size-9 sm:size-7'
                              onClick={() => setPendingDeleteId(row.id)}
                              aria-label={t('Delete')}
                            >
                              <Trash2 className='size-4' />
                            </Button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}

            {pageCount > 1 ? (
              <nav
                aria-label={t('Pagination')}
                className='flex items-center justify-between border-t pt-4 text-sm'
              >
                <span className='text-muted-foreground tabular-nums'>
                  {t('Page {{page}} of {{total}}', { page, total: pageCount })}
                </span>
                <div className='flex gap-2'>
                  <Button
                    variant='outline'
                    size='sm'
                    className='h-11 gap-1 px-3 sm:h-8'
                    disabled={page <= 1 || query.isFetching}
                    onClick={() => setPage((value) => value - 1)}
                  >
                    <ArrowLeft className='size-4' />
                    {t('Previous')}
                  </Button>
                  <Button
                    variant='outline'
                    size='sm'
                    className='h-11 gap-1 px-3 sm:h-8'
                    disabled={page >= pageCount || query.isFetching}
                    onClick={() => setPage((value) => value + 1)}
                  >
                    {t('Next')}
                    <ArrowRight className='size-4' />
                  </Button>
                </div>
              </nav>
            ) : null}
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <CleanupExhaustedCodesDialog
        open={cleanup.open}
        pending={cleanup.pending}
        onOpenChange={cleanup.setOpen}
        onConfirm={cleanup.confirm}
      />

      <DiscountCodeDeleteDialog
        row={pendingDeleteRow}
        pending={deleteMutation.isPending}
        onOpenChange={(open) => {
          if (!open) setPendingDeleteId(null)
        }}
        onConfirm={() => {
          if (pendingDeleteId !== null) deleteMutation.mutate(pendingDeleteId)
        }}
      />

      <Sheet open={sheetOpen} onOpenChange={setSheetOpen}>
        <SheetContent className='sm:max-w-[520px]'>
          <SheetHeader>
            <SheetTitle>
              {editing ? t('Edit discount code') : t('Create discount code')}
            </SheetTitle>
            <SheetDescription>
              {t(
                'Set a percentage discount. The server checks dates and minimum amount at checkout.'
              )}
            </SheetDescription>
          </SheetHeader>
          <div className='grid gap-5 px-5 py-6'>
            {editing ? (
              <div className='grid gap-2'>
                <Label htmlFor='discount-form-code'>{t('Code')}</Label>
                <div className='flex gap-2'>
                  <Input
                    id='discount-form-code'
                    value={form.code}
                    readOnly
                    className='font-mono tracking-wider'
                    maxLength={64}
                    autoComplete='off'
                    aria-invalid={Boolean(visibleErrors.code)}
                    aria-describedby={
                      visibleErrors.code
                        ? 'discount-form-code-error'
                        : undefined
                    }
                  />
                  <Button
                    type='button'
                    variant='outline'
                    size='icon'
                    onClick={copyCode}
                    disabled={!form.code}
                    aria-label={t('Copy')}
                    title={t('Copy share link')}
                  >
                    <Copy className='size-4' />
                  </Button>
                </div>
                <FieldError
                  id='discount-form-code-error'
                  message={
                    visibleErrors.code ? t(visibleErrors.code) : undefined
                  }
                />
                <p className='text-muted-foreground text-xs'>
                  {t('Existing codes cannot be changed.')}
                </p>
              </div>
            ) : null}
            <div className='grid gap-2'>
              <Label htmlFor='discount-form-name'>{t('Name')}</Label>
              <Input
                id='discount-form-name'
                value={form.name}
                onChange={(event) =>
                  setForm((state) => ({ ...state, name: event.target.value }))
                }
                onBlur={() => markTouched('name')}
                maxLength={120}
                aria-invalid={Boolean(visibleErrors.name)}
                aria-describedby={
                  visibleErrors.name ? 'discount-form-name-error' : undefined
                }
              />
              <FieldError
                id='discount-form-name-error'
                message={visibleErrors.name ? t(visibleErrors.name) : undefined}
              />
            </div>
            {!editing ? (
              <div className='grid gap-2'>
                <Label htmlFor='discount-form-count'>{t('Quantity')}</Label>
                <Input
                  id='discount-form-count'
                  type='number'
                  min='1'
                  max='100'
                  step='1'
                  value={form.count}
                  onChange={(event) =>
                    setForm((state) => ({
                      ...state,
                      count: event.target.value,
                    }))
                  }
                  onBlur={() => markTouched('count')}
                  aria-invalid={Boolean(visibleErrors.count)}
                  aria-describedby={
                    visibleErrors.count
                      ? 'discount-form-count-error'
                      : 'discount-form-count-hint'
                  }
                />
                <FieldError
                  id='discount-form-count-error'
                  message={
                    visibleErrors.count ? t(visibleErrors.count) : undefined
                  }
                />
                <p
                  id='discount-form-count-hint'
                  className='text-muted-foreground text-xs'
                >
                  {t('Number of discount codes to generate.')}
                </p>
              </div>
            ) : null}
            <div className='grid grid-cols-2 gap-4'>
              <div className='grid gap-2'>
                <Label htmlFor='discount-form-percent'>
                  {t('Discount percent')}
                </Label>
                <Input
                  id='discount-form-percent'
                  type='number'
                  min='1'
                  max='99'
                  value={form.discount_percent}
                  onChange={(event) =>
                    setForm((state) => ({
                      ...state,
                      discount_percent: event.target.value,
                    }))
                  }
                  onBlur={() => markTouched('discount_percent')}
                  aria-invalid={Boolean(visibleErrors.discount_percent)}
                  aria-describedby={
                    visibleErrors.discount_percent
                      ? 'discount-form-percent-error'
                      : undefined
                  }
                />
                <FieldError
                  id='discount-form-percent-error'
                  message={
                    visibleErrors.discount_percent
                      ? t(visibleErrors.discount_percent)
                      : undefined
                  }
                />
              </div>
              <div className='grid gap-2'>
                <Label htmlFor='discount-form-min'>{t('Minimum amount')}</Label>
                <Input
                  id='discount-form-min'
                  type='number'
                  min='0'
                  step='1'
                  value={form.min_amount}
                  onChange={(event) =>
                    setForm((state) => ({
                      ...state,
                      min_amount: event.target.value,
                    }))
                  }
                  onBlur={() => markTouched('min_amount')}
                  aria-invalid={Boolean(visibleErrors.min_amount)}
                  aria-describedby={
                    visibleErrors.min_amount
                      ? 'discount-form-min-error'
                      : undefined
                  }
                />
                <FieldError
                  id='discount-form-min-error'
                  message={
                    visibleErrors.min_amount
                      ? t(visibleErrors.min_amount)
                      : undefined
                  }
                />
              </div>
            </div>
            <div className='grid gap-2'>
              <Label htmlFor='discount-form-max-uses'>
                {t('Usages per code')}
              </Label>
              <Input
                id='discount-form-max-uses'
                type='number'
                min='0'
                step='1'
                value={form.max_uses}
                onChange={(event) =>
                  setForm((state) => ({
                    ...state,
                    max_uses: event.target.value,
                  }))
                }
                onBlur={() => markTouched('max_uses')}
                aria-invalid={
                  visibleErrors.max_uses ? true : maxUses === undefined
                }
                aria-describedby={
                  visibleErrors.max_uses
                    ? 'discount-form-max-uses-error'
                    : 'discount-form-max-uses-hint'
                }
              />
              <FieldError
                id='discount-form-max-uses-error'
                message={
                  visibleErrors.max_uses ? t(visibleErrors.max_uses) : undefined
                }
              />
              <p
                id='discount-form-max-uses-hint'
                className='text-muted-foreground text-xs'
              >
                {t('Usage limit applies to each generated code.')}{' '}
                {t('0 means unlimited')}
              </p>
            </div>
            <div className='grid grid-cols-2 gap-4'>
              <div className='grid gap-2'>
                <Label htmlFor='discount-form-start'>{t('Starts')}</Label>
                <Input
                  id='discount-form-start'
                  type='datetime-local'
                  value={form.starts_time}
                  onChange={(event) =>
                    setForm((state) => ({
                      ...state,
                      starts_time: event.target.value,
                    }))
                  }
                  onBlur={() => markTouched('starts_time')}
                  aria-invalid={Boolean(visibleErrors.starts_time)}
                  aria-describedby={
                    visibleErrors.starts_time
                      ? 'discount-form-start-error'
                      : undefined
                  }
                />
                <FieldError
                  id='discount-form-start-error'
                  message={
                    visibleErrors.starts_time
                      ? t(visibleErrors.starts_time)
                      : undefined
                  }
                />
              </div>
              <div className='grid gap-2'>
                <Label htmlFor='discount-form-expire'>{t('Expires')}</Label>
                <Input
                  id='discount-form-expire'
                  type='datetime-local'
                  value={form.expired_time}
                  onChange={(event) =>
                    setForm((state) => ({
                      ...state,
                      expired_time: event.target.value,
                    }))
                  }
                  onBlur={() => markTouched('expired_time')}
                  aria-invalid={Boolean(visibleErrors.expired_time)}
                  aria-describedby={
                    visibleErrors.expired_time
                      ? 'discount-form-expire-error'
                      : undefined
                  }
                />
                <FieldError
                  id='discount-form-expire-error'
                  message={
                    visibleErrors.expired_time
                      ? t(visibleErrors.expired_time)
                      : undefined
                  }
                />
                <p className='text-muted-foreground text-xs'>
                  {t('Leave empty for no expiration.')}
                </p>
              </div>
            </div>
          </div>
          <SheetFooter>
            <SheetClose
              render={<Button variant='outline'>{t('Cancel')}</Button>}
            />
            <Button disabled={isSaving} onClick={submit}>
              {isSaving ? t('Saving...') : t('Save changes')}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>

      <Dialog open={generatedCodesOpen} onOpenChange={setGeneratedCodesOpen}>
        <DialogContent className='sm:max-w-xl'>
          <DialogHeader>
            <DialogTitle>{t('Discount codes created')}</DialogTitle>
            <DialogDescription>
              {t('Copy these generated links now for distribution.')}
            </DialogDescription>
          </DialogHeader>
          <Textarea
            value={generatedCodes
              .map((code) => buildDiscountCodeLink(code))
              .join('\n')}
            readOnly
            rows={Math.min(12, Math.max(4, generatedCodes.length))}
            className='font-mono text-sm'
          />
          <DialogFooter>
            <DialogClose render={<Button variant='outline' />}>
              {t('Cancel')}
            </DialogClose>
            <Button onClick={() => void copyDiscountLinks(generatedCodes, t)}>
              <Copy className='size-4' />
              {t('Copy all generated links')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
