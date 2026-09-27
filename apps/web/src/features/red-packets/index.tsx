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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { TFunction } from 'i18next'
import { Gift, ImagePlus, Plus, Sparkles } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { listDiscountCodes } from '@/features/discount-codes/api'
import type { DiscountCode } from '@/features/discount-codes/types'
import { getRedemptions } from '@/features/redemption-codes/api'
import type { Redemption } from '@/features/redemption-codes/types'
import { api } from '@/lib/api'
import { formatQuota } from '@/lib/format'

import { createRedPacket, deleteRedPacket, listRedPackets } from './api'
import { RedPacketCard } from './red-packet-card'
import type {
  RedPacket,
  RedPacketDrawMode,
  RedPacketItemInput,
  RedPacketItemType,
} from './types'

type Candidate = {
  key: string
  itemType: RedPacketItemType
  sourceId: number
  title: string
  detail: string
}

type FormState = {
  title: string
  description: string
  coverImage: string
  coverPrompt: string
  coverGroup: string
  coverModel: string
  drawMode: RedPacketDrawMode
  perUserLimit: number
  startAt: string
  endAt: string
}

const emptyForm: FormState = {
  title: '',
  description: '',
  coverImage: '',
  coverPrompt:
    '设计一张简洁、高级、具有节日感的数字红包封面，不要出现具体金额，适合 AI API 开发者社区。',
  coverGroup: 'image-2',
  coverModel: 'image-2',
  drawMode: 'random',
  perUserLimit: 1,
  startAt: '',
  endAt: '',
}

function toTimestamp(value: string) {
  if (!value) return 0
  const timestamp = Math.floor(new Date(value).getTime() / 1000)
  return Number.isFinite(timestamp) ? timestamp : 0
}

async function loadRedemptions(): Promise<Redemption[]> {
  const result: Redemption[] = []
  for (let page = 1; page <= 10; page += 1) {
    const response = await getRedemptions({ p: page, page_size: 100 })
    const items = response.data?.items ?? []
    result.push(...items)
    if (items.length < 100) break
  }
  return result
}

async function loadDiscountCodes(): Promise<DiscountCode[]> {
  const result: DiscountCode[] = []
  for (let page = 1; page <= 10; page += 1) {
    const response = await listDiscountCodes({ page, pageSize: 100 })
    const items = response.data?.items ?? []
    result.push(...items)
    if (items.length < 100) break
  }
  return result
}

type ReadImageErrorCode = 'invalid-type' | 'too-large' | 'read-failed'

function readImage(file: File): Promise<string> {
  if (!file.type.startsWith('image/')) {
    const code: ReadImageErrorCode = 'invalid-type'
    return Promise.reject(new Error(code))
  }
  if (file.size > 3 * 1024 * 1024) {
    const code: ReadImageErrorCode = 'too-large'
    return Promise.reject(new Error(code))
  }
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result ?? ''))
    reader.onerror = () => {
      const code: ReadImageErrorCode = 'read-failed'
      reject(reader.error ?? new Error(code))
    }
    reader.readAsDataURL(file)
  })
}

function readImageErrorMessage(error: unknown, t: TFunction): string {
  const code = error instanceof Error ? error.message : ''
  if (code === 'invalid-type') return t('Please choose an image file')
  if (code === 'too-large') return t('Cover image must be smaller than 3 MB')
  return t('Unable to read image')
}

function candidateFromRedemption(row: Redemption, t: TFunction): Candidate {
  const rewardType = row.reward_type ?? 'quota'
  const detail =
    rewardType === 'reset_voucher'
      ? t('Banked reset voucher for plan #{{plan}}', {
          plan: row.reset_plan_id,
        })
      : formatQuota(row.quota)
  return {
    key: `redemption:${row.id}`,
    itemType: 'redemption',
    sourceId: row.id,
    title: row.name || `#${row.id}`,
    detail,
  }
}

function candidateFromDiscount(row: DiscountCode, t: TFunction): Candidate {
  const percentOff = t('{{percent}}% off', { percent: row.discount_percent })
  return {
    key: `discount:${row.id}`,
    itemType: 'discount',
    sourceId: row.id,
    title: row.name || row.code,
    detail: `${percentOff} · ${row.code}`,
  }
}

export function RedPackets() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [deleting, setDeleting] = useState<RedPacket | null>(null)
  const deletePendingRef = useRef(false)
  const [form, setForm] = useState<FormState>(emptyForm)
  const [selected, setSelected] = useState<Record<string, number>>({})
  const [generatingCover, setGeneratingCover] = useState(false)

  const userGroupsQuery = useQuery({
    queryKey: ['userGroups'],
    queryFn: async () => {
      const response = await api.get<{
        success?: boolean
        data?: Record<string, { ratio: number; desc?: string }>
      }>('/api/user/groups')
      return response.data.data ?? {}
    },
  })
  const coverModelsQuery = useQuery({
    queryKey: ['red-packet-cover-models', form.coverGroup],
    queryFn: async () => {
      const response = await api.get<{
        success?: boolean
        data?: string[]
      }>('/api/user/models', { params: { group: form.coverGroup } })
      return (response.data.data ?? [])
        .filter(Boolean)
        .sort((a, b) => a.localeCompare(b))
    },
    enabled: open && Boolean(form.coverGroup),
    staleTime: 60_000,
  })

  useEffect(() => {
    const models = coverModelsQuery.data ?? []
    if (models.length > 0 && !models.includes(form.coverModel)) {
      setForm((previous) => ({ ...previous, coverModel: models[0] }))
    }
  }, [coverModelsQuery.data, form.coverModel])

  const packetsQuery = useQuery({
    queryKey: ['red-packets', 'admin'],
    queryFn: listRedPackets,
    refetchOnWindowFocus: true,
  })
  const redemptionsQuery = useQuery({
    queryKey: ['red-packets', 'redemptions'],
    queryFn: loadRedemptions,
    enabled: open,
  })
  const discountsQuery = useQuery({
    queryKey: ['red-packets', 'discounts'],
    queryFn: loadDiscountCodes,
    enabled: open,
  })

  const candidates = useMemo(() => {
    const now = Math.floor(Date.now() / 1000)
    const redemptions = (redemptionsQuery.data ?? [])
      .filter(
        (row) =>
          row.status === 1 && (!row.expired_time || row.expired_time >= now)
      )
      .map((row) => candidateFromRedemption(row, t))
    const discounts = (discountsQuery.data ?? [])
      .filter(
        (row) =>
          row.status === 1 &&
          (!row.starts_time || row.starts_time <= now) &&
          (!row.expired_time || row.expired_time >= now) &&
          (!row.max_uses || row.used_count < row.max_uses)
      )
      .map((row) => candidateFromDiscount(row, t))
    return [...redemptions, ...discounts]
  }, [redemptionsQuery.data, discountsQuery.data, t])

  const createMutation = useMutation({
    mutationFn: createRedPacket,
    onSuccess: async (response) => {
      if (!response.success || !response.data) {
        toast.error(response.message || t('Failed to create red packet'))
        return
      }
      const link = `${window.location.origin}/red-packet/${response.data.slug}`
      await navigator.clipboard?.writeText(link)
      toast.success(t('Red packet created and share link copied'))
      setOpen(false)
      setForm(emptyForm)
      setSelected({})
      await queryClient.invalidateQueries({
        queryKey: ['red-packets', 'admin'],
      })
    },
    onError: (error) => {
      toast.error(
        error instanceof Error
          ? error.message
          : t('Failed to create red packet')
      )
    },
  })

  const deleteMutation = useMutation({
    mutationFn: async (id: number) => {
      const response = await deleteRedPacket(id)
      if (!response.success) {
        throw new Error(response.message || t('Failed to delete red packet'))
      }
    },
    onSuccess: async () => {
      setDeleting(null)
      toast.success(t('Red packet deleted'))
      await queryClient.invalidateQueries({ queryKey: ['red-packets'] })
    },
    onError: (error) => {
      toast.error(error.message || t('Failed to delete red packet'))
    },
    onSettled: () => {
      deletePendingRef.current = false
    },
  })

  const toggleCandidate = (candidate: Candidate, checked: boolean) => {
    setSelected((previous) => {
      const next = { ...previous }
      if (checked) next[candidate.key] = previous[candidate.key] || 1
      else delete next[candidate.key]
      return next
    })
  }

  const selectedCount = Object.keys(selected).length
  const canSubmit = form.title.trim().length > 0 && selectedCount > 0

  const submit = () => {
    const items: RedPacketItemInput[] = candidates
      .filter((candidate) => selected[candidate.key] !== undefined)
      .map((candidate) => ({
        item_type: candidate.itemType,
        source_id: candidate.sourceId,
        weight: Math.max(1, selected[candidate.key] || 1),
      }))
    if (!form.title.trim()) {
      toast.error(t('Please enter a title'))
      return
    }
    if (items.length === 0) {
      toast.error(t('Select at least one redemption or discount code'))
      return
    }
    createMutation.mutate({
      title: form.title.trim(),
      description: form.description.trim(),
      cover_image: form.coverImage,
      cover_prompt: form.coverPrompt.trim(),
      draw_mode: form.drawMode,
      per_user_limit: Math.max(1, form.perUserLimit),
      start_at: toTimestamp(form.startAt),
      end_at: toTimestamp(form.endAt),
      enabled: true,
      items,
    })
  }

  const generateCover = async () => {
    const prompt = form.coverPrompt.trim()
    if (!prompt) return
    if (!form.coverModel || !coverModelsQuery.data?.includes(form.coverModel)) {
      toast.error(t('Choose an available image model'))
      return
    }
    setGeneratingCover(true)
    try {
      const response = await api.post<{
        data?: Array<{ url?: string; b64_json?: string }>
      }>(
        `/red-packet-cover/images/generations?group=${encodeURIComponent(form.coverGroup)}`,
        {
          model: form.coverModel,
          prompt,
          n: 1,
          size: '1536x1024',
          response_format: 'b64_json',
        }
      )
      const image = response.data.data?.[0]
      const cover = image?.b64_json
        ? `data:image/png;base64,${image.b64_json}`
        : image?.url || ''
      if (!cover) throw new Error('Image generation returned no image')
      setForm((previous) => ({ ...previous, coverImage: cover }))
      toast.success(t('Cover generated'))
    } catch {
      toast.error(t('Unable to generate the image'))
    } finally {
      setGeneratingCover(false)
    }
  }

  const packets = packetsQuery.data?.data ?? []

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Red Packets')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button type='button' onClick={() => setOpen(true)}>
            <Plus className='mr-2 size-4' />
            {t('Create red packet')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='mx-auto w-full max-w-6xl space-y-4'>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Distribute redemption and discount codes fairly through a shareable draw link.'
              )}
            </p>
            <div className='grid gap-3 md:grid-cols-2 xl:grid-cols-3'>
              {packets.map((packet) => (
                <RedPacketCard
                  key={packet.id}
                  packet={packet}
                  onDelete={() => setDeleting(packet)}
                />
              ))}
              {!packetsQuery.isLoading && packets.length === 0 ? (
                <div className='text-muted-foreground flex flex-col items-center gap-3 rounded-xl border border-dashed p-8 text-center text-sm md:col-span-2 xl:col-span-3'>
                  <Gift className='size-7 opacity-60' aria-hidden='true' />
                  <p>{t('No red packets yet.')}</p>
                  <Button type='button' size='sm' onClick={() => setOpen(true)}>
                    <Plus className='mr-2 size-4' />
                    {t('Create the first one')}
                  </Button>
                </div>
              ) : null}
            </div>
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(next) => {
          if (!next && !deletePendingRef.current) setDeleting(null)
        }}
        title={t('Delete red packet')}
        desc={
          <>
            <span className='text-foreground font-medium break-words'>
              {deleting?.title}
            </span>
            <p className='mt-2'>
              {t(
                'Remove this red packet from the list and stop new claims. Claim history and already received rewards are kept.'
              )}
            </p>
          </>
        }
        confirmText={t('Delete')}
        destructive
        isLoading={deleteMutation.isPending}
        handleConfirm={() => {
          if (deleting && !deletePendingRef.current) {
            deletePendingRef.current = true
            deleteMutation.mutate(deleting.id)
          }
        }}
      />

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className='max-h-[90vh] overflow-y-auto sm:max-w-3xl'>
          <DialogHeader>
            <DialogTitle>{t('Create red packet')}</DialogTitle>
            <DialogDescription>
              {t(
                'Choose existing codes, set the draw rule, then share one link instead of publishing every code.'
              )}
            </DialogDescription>
          </DialogHeader>

          <div className='grid gap-5 py-2'>
            <div className='grid gap-3 sm:grid-cols-2'>
              <div className='space-y-2'>
                <Label>{t('Title')}</Label>
                <Input
                  value={form.title}
                  onChange={(event) =>
                    setForm((v) => ({ ...v, title: event.target.value }))
                  }
                />
              </div>
              <div className='space-y-2'>
                <Label>{t('Per-user draws')}</Label>
                <Input
                  type='number'
                  min={1}
                  max={100}
                  value={form.perUserLimit}
                  onChange={(event) =>
                    setForm((v) => ({
                      ...v,
                      perUserLimit: Number(event.target.value) || 1,
                    }))
                  }
                />
              </div>
            </div>

            <div className='space-y-2'>
              <Label>{t('Description')}</Label>
              <Textarea
                value={form.description}
                onChange={(event) =>
                  setForm((v) => ({ ...v, description: event.target.value }))
                }
              />
            </div>

            <div className='grid gap-3 sm:grid-cols-3'>
              <div className='space-y-2'>
                <Label>{t('Draw rule')}</Label>
                <select
                  className='border-input bg-background h-9 w-full rounded-md border px-3 text-sm'
                  value={form.drawMode}
                  onChange={(event) =>
                    setForm((v) => ({
                      ...v,
                      drawMode: event.target.value as RedPacketDrawMode,
                    }))
                  }
                >
                  <option value='random'>{t('Random')}</option>
                  <option value='weighted'>{t('Weighted random')}</option>
                  <option value='sequence'>{t('Sequence')}</option>
                </select>
              </div>
              <div className='space-y-2'>
                <Label>{t('Starts at')}</Label>
                <Input
                  type='datetime-local'
                  value={form.startAt}
                  onChange={(event) =>
                    setForm((v) => ({ ...v, startAt: event.target.value }))
                  }
                />
              </div>
              <div className='space-y-2'>
                <Label>{t('Ends at')}</Label>
                <Input
                  type='datetime-local'
                  value={form.endAt}
                  onChange={(event) =>
                    setForm((v) => ({ ...v, endAt: event.target.value }))
                  }
                />
              </div>
            </div>

            <div className='space-y-3 rounded-xl border p-4'>
              <div className='flex items-center gap-2 font-medium'>
                <ImagePlus className='size-4' /> {t('Cover')}
              </div>
              {form.coverImage ? (
                <img
                  src={form.coverImage}
                  alt=''
                  className='aspect-[3/1] w-full rounded-lg object-cover'
                />
              ) : null}
              <div className='grid gap-3 sm:grid-cols-2'>
                <div className='space-y-2'>
                  <Label>{t('Group')}</Label>
                  <select
                    className='border-input bg-background h-9 w-full rounded-md border px-3 text-sm'
                    value={form.coverGroup}
                    onChange={(event) =>
                      setForm((v) => ({ ...v, coverGroup: event.target.value }))
                    }
                  >
                    {Object.keys(userGroupsQuery.data ?? {}).map((group) => (
                      <option key={group} value={group}>
                        {group}
                      </option>
                    ))}
                    {!userGroupsQuery.data ||
                    Object.keys(userGroupsQuery.data).length === 0 ? (
                      <option value='image-2'>image-2</option>
                    ) : null}
                  </select>
                </div>
                <div className='space-y-2'>
                  <Label>{t('Model')}</Label>
                  <select
                    className='border-input bg-background h-9 w-full rounded-md border px-3 text-sm'
                    value={form.coverModel}
                    disabled={
                      coverModelsQuery.isPending ||
                      !coverModelsQuery.data?.length
                    }
                    onChange={(event) =>
                      setForm((v) => ({ ...v, coverModel: event.target.value }))
                    }
                  >
                    {coverModelsQuery.data?.map((model) => (
                      <option key={model} value={model}>
                        {model}
                      </option>
                    ))}
                    {!coverModelsQuery.data?.length && (
                      <option value=''>
                        {coverModelsQuery.isPending
                          ? t('Loading…')
                          : t('No available image models')}
                      </option>
                    )}
                  </select>
                </div>
              </div>
              <Textarea
                value={form.coverPrompt}
                onChange={(event) =>
                  setForm((v) => ({ ...v, coverPrompt: event.target.value }))
                }
                placeholder={t('Describe the cover you want')}
              />
              <div className='flex flex-wrap gap-2'>
                <Button
                  type='button'
                  variant='outline'
                  disabled={
                    generatingCover ||
                    coverModelsQuery.isPending ||
                    !coverModelsQuery.data?.length ||
                    !form.coverModel
                  }
                  onClick={() => void generateCover()}
                >
                  <Sparkles className='mr-2 size-4' />
                  {generatingCover ? t('Generating...') : t('Generate with AI')}
                </Button>
                <label className='border-input hover:bg-accent inline-flex h-9 cursor-pointer items-center rounded-md border px-3 text-sm'>
                  <ImagePlus className='mr-2 size-4' /> {t('Upload image')}
                  <input
                    type='file'
                    accept='image/*'
                    className='hidden'
                    onChange={(event) => {
                      const file = event.target.files?.[0]
                      if (!file) return
                      void readImage(file)
                        .then((coverImage) =>
                          setForm((v) => ({ ...v, coverImage }))
                        )
                        .catch((error: unknown) =>
                          toast.error(readImageErrorMessage(error, t))
                        )
                    }}
                  />
                </label>
              </div>
            </div>

            <div className='space-y-3'>
              <div className='flex items-center justify-between'>
                <Label>{t('Rewards')}</Label>
                <span className='text-muted-foreground text-xs'>
                  {selectedCount} {t('selected')}
                </span>
              </div>
              <div className='max-h-80 space-y-2 overflow-y-auto rounded-xl border p-2'>
                {candidates.map((candidate) => {
                  const checked = selected[candidate.key] !== undefined
                  return (
                    <label
                      key={candidate.key}
                      className='hover:bg-muted/50 flex cursor-pointer items-center gap-3 rounded-lg p-2'
                    >
                      <input
                        type='checkbox'
                        checked={checked}
                        onChange={(event) =>
                          toggleCandidate(candidate, event.target.checked)
                        }
                      />
                      <div className='min-w-0 flex-1'>
                        <div className='truncate text-sm font-medium'>
                          {candidate.title}
                        </div>
                        <div className='text-muted-foreground truncate text-xs'>
                          {t(
                            candidate.itemType === 'redemption'
                              ? 'Redemption Code'
                              : 'Discount code'
                          )}{' '}
                          · {candidate.detail}
                        </div>
                      </div>
                      {form.drawMode === 'weighted' && checked ? (
                        <Input
                          className='h-8 w-20'
                          type='number'
                          min={1}
                          value={selected[candidate.key]}
                          onChange={(event) =>
                            setSelected((previous) => ({
                              ...previous,
                              [candidate.key]: Math.max(
                                1,
                                Number(event.target.value) || 1
                              ),
                            }))
                          }
                          onClick={(event) => event.preventDefault()}
                        />
                      ) : null}
                    </label>
                  )
                })}
                {!redemptionsQuery.isLoading &&
                !discountsQuery.isLoading &&
                candidates.length === 0 ? (
                  <div className='text-muted-foreground p-5 text-center text-sm'>
                    {t('No available codes')}
                  </div>
                ) : null}
              </div>
            </div>
          </div>

          <DialogFooter className='items-center gap-2 sm:items-center'>
            {!canSubmit ? (
              <span className='text-muted-foreground mr-auto text-xs'>
                {!form.title.trim()
                  ? t('Please enter a title')
                  : t('Select at least one redemption or discount code')}
              </span>
            ) : null}
            <Button
              type='button'
              variant='outline'
              onClick={() => setOpen(false)}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='button'
              onClick={submit}
              disabled={!canSubmit || createMutation.isPending}
            >
              {createMutation.isPending
                ? t('Creating...')
                : t('Create and copy link')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
