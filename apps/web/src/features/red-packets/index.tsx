import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Copy, Gift, ImagePlus, Plus, Sparkles } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

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

import { createRedPacket, listRedPackets } from './api'
import type {
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
  drawMode: RedPacketDrawMode
  perUserLimit: number
  startAt: string
  endAt: string
}

const emptyForm: FormState = {
  title: '',
  description: '',
  coverImage: '',
  coverPrompt: '设计一张简洁、高级、具有节日感的数字红包封面，不要出现具体金额，适合 AI API 开发者社区。',
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

function readImage(file: File): Promise<string> {
  if (!file.type.startsWith('image/')) {
    return Promise.reject(new Error('Please choose an image file'))
  }
  if (file.size > 3 * 1024 * 1024) {
    return Promise.reject(new Error('Cover image must be smaller than 3 MB'))
  }
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result ?? ''))
    reader.onerror = () => reject(reader.error ?? new Error('Unable to read image'))
    reader.readAsDataURL(file)
  })
}

function candidateFromRedemption(row: Redemption): Candidate {
  const rewardType = row.reward_type ?? 'quota'
  const detail =
    rewardType === 'reset_voucher'
      ? `Banked reset · plan #${row.reset_plan_id}`
      : formatQuota(row.quota)
  return {
    key: `redemption:${row.id}`,
    itemType: 'redemption',
    sourceId: row.id,
    title: row.name || `#${row.id}`,
    detail,
  }
}

function candidateFromDiscount(row: DiscountCode): Candidate {
  return {
    key: `discount:${row.id}`,
    itemType: 'discount',
    sourceId: row.id,
    title: row.name || row.code,
    detail: `${row.discount_percent}% off · ${row.code}`,
  }
}

export function RedPackets() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState<FormState>(emptyForm)
  const [selected, setSelected] = useState<Record<string, number>>({})
  const [generatingCover, setGeneratingCover] = useState(false)

  const packetsQuery = useQuery({ queryKey: ['red-packets', 'admin'], queryFn: listRedPackets })
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
      .filter((row) => row.status === 1 && (!row.expired_time || row.expired_time >= now))
      .map(candidateFromRedemption)
    const discounts = (discountsQuery.data ?? [])
      .filter(
        (row) =>
          row.status === 1 &&
          (!row.starts_time || row.starts_time <= now) &&
          (!row.expired_time || row.expired_time >= now) &&
          (!row.max_uses || row.used_count < row.max_uses)
      )
      .map(candidateFromDiscount)
    return [...redemptions, ...discounts]
  }, [redemptionsQuery.data, discountsQuery.data])

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
      await queryClient.invalidateQueries({ queryKey: ['red-packets', 'admin'] })
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
    setGeneratingCover(true)
    try {
      const response = await api.post<{
        data?: Array<{ url?: string; b64_json?: string }>
      }>('/pg/images/generations?group=image-2', {
        model: 'image-2',
        prompt,
        n: 1,
        size: '1536x1024',
        response_format: 'b64_json',
      })
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
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Red Packets')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button onClick={() => setOpen(true)}>
          <Plus className='mr-2 size-4' />
          {t('Create red packet')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='mx-auto w-full max-w-6xl space-y-4'>
          <p className='text-muted-foreground text-sm'>
            {t('Distribute redemption and discount codes fairly through a shareable draw link.')}
          </p>
          <div className='grid gap-3 md:grid-cols-2 xl:grid-cols-3'>
            {packets.map((packet) => {
              const shareUrl = `${window.location.origin}/red-packet/${packet.slug}`
              return (
                <div key={packet.id} className='bg-card overflow-hidden rounded-xl border'>
                  {packet.cover_image ? (
                    <img src={packet.cover_image} alt='' className='aspect-[3/1] w-full object-cover' />
                  ) : (
                    <div className='from-primary/15 to-muted flex aspect-[3/1] items-center justify-center bg-gradient-to-br'>
                      <Gift className='text-muted-foreground size-8' />
                    </div>
                  )}
                  <div className='space-y-3 p-4'>
                    <div>
                      <div className='font-medium'>{packet.title}</div>
                      <div className='text-muted-foreground mt-1 text-xs'>
                        {packet.remaining_items}/{packet.total_items} {t('remaining')} · {packet.claim_count} {t('claims')}
                      </div>
                    </div>
                    <div className='flex gap-2'>
                      <Input value={shareUrl} readOnly className='h-8 text-xs' />
                      <Button
                        size='sm'
                        variant='outline'
                        onClick={async () => {
                          await navigator.clipboard?.writeText(shareUrl)
                          toast.success(t('Copied to clipboard'))
                        }}
                      >
                        <Copy className='size-4' />
                      </Button>
                    </div>
                  </div>
                </div>
              )
            })}
            {!packetsQuery.isLoading && packets.length === 0 ? (
              <div className='text-muted-foreground rounded-xl border border-dashed p-8 text-sm md:col-span-2 xl:col-span-3'>
                {t('No red packets yet.')}
              </div>
            ) : null}
          </div>
        </div>
      </SectionPageLayout.Content>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className='max-h-[90vh] overflow-y-auto sm:max-w-3xl'>
          <DialogHeader>
            <DialogTitle>{t('Create red packet')}</DialogTitle>
            <DialogDescription>
              {t('Choose existing codes, set the draw rule, then share one link instead of publishing every code.')}
            </DialogDescription>
          </DialogHeader>

          <div className='grid gap-5 py-2'>
            <div className='grid gap-3 sm:grid-cols-2'>
              <div className='space-y-2'>
                <Label>{t('Title')}</Label>
                <Input value={form.title} onChange={(event) => setForm((v) => ({ ...v, title: event.target.value }))} />
              </div>
              <div className='space-y-2'>
                <Label>{t('Per-user draws')}</Label>
                <Input type='number' min={1} max={100} value={form.perUserLimit} onChange={(event) => setForm((v) => ({ ...v, perUserLimit: Number(event.target.value) || 1 }))} />
              </div>
            </div>

            <div className='space-y-2'>
              <Label>{t('Description')}</Label>
              <Textarea value={form.description} onChange={(event) => setForm((v) => ({ ...v, description: event.target.value }))} />
            </div>

            <div className='grid gap-3 sm:grid-cols-3'>
              <div className='space-y-2'>
                <Label>{t('Draw rule')}</Label>
                <select className='border-input bg-background h-9 w-full rounded-md border px-3 text-sm' value={form.drawMode} onChange={(event) => setForm((v) => ({ ...v, drawMode: event.target.value as RedPacketDrawMode }))}>
                  <option value='random'>{t('Random')}</option>
                  <option value='weighted'>{t('Weighted random')}</option>
                  <option value='sequence'>{t('Sequence')}</option>
                </select>
              </div>
              <div className='space-y-2'>
                <Label>{t('Starts at')}</Label>
                <Input type='datetime-local' value={form.startAt} onChange={(event) => setForm((v) => ({ ...v, startAt: event.target.value }))} />
              </div>
              <div className='space-y-2'>
                <Label>{t('Ends at')}</Label>
                <Input type='datetime-local' value={form.endAt} onChange={(event) => setForm((v) => ({ ...v, endAt: event.target.value }))} />
              </div>
            </div>

            <div className='space-y-3 rounded-xl border p-4'>
              <div className='flex items-center gap-2 font-medium'>
                <ImagePlus className='size-4' /> {t('Cover')}
              </div>
              {form.coverImage ? <img src={form.coverImage} alt='' className='aspect-[3/1] w-full rounded-lg object-cover' /> : null}
              <Textarea value={form.coverPrompt} onChange={(event) => setForm((v) => ({ ...v, coverPrompt: event.target.value }))} placeholder={t('Describe the cover you want')} />
              <div className='flex flex-wrap gap-2'>
                <Button type='button' variant='outline' disabled={generatingCover} onClick={() => void generateCover()}>
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
                        .then((coverImage) => setForm((v) => ({ ...v, coverImage })))
                        .catch((error: unknown) => toast.error(error instanceof Error ? error.message : t('Unable to read image')))
                    }}
                  />
                </label>
              </div>
            </div>

            <div className='space-y-3'>
              <div className='flex items-center justify-between'>
                <Label>{t('Rewards')}</Label>
                <span className='text-muted-foreground text-xs'>{Object.keys(selected).length} {t('selected')}</span>
              </div>
              <div className='max-h-80 space-y-2 overflow-y-auto rounded-xl border p-2'>
                {candidates.map((candidate) => {
                  const checked = selected[candidate.key] !== undefined
                  return (
                    <label key={candidate.key} className='hover:bg-muted/50 flex cursor-pointer items-center gap-3 rounded-lg p-2'>
                      <input type='checkbox' checked={checked} onChange={(event) => toggleCandidate(candidate, event.target.checked)} />
                      <div className='min-w-0 flex-1'>
                        <div className='truncate text-sm font-medium'>{candidate.title}</div>
                        <div className='text-muted-foreground truncate text-xs'>{candidate.itemType} · {candidate.detail}</div>
                      </div>
                      {form.drawMode === 'weighted' && checked ? (
                        <Input
                          className='h-8 w-20'
                          type='number'
                          min={1}
                          value={selected[candidate.key]}
                          onChange={(event) => setSelected((previous) => ({ ...previous, [candidate.key]: Math.max(1, Number(event.target.value) || 1) }))}
                          onClick={(event) => event.preventDefault()}
                        />
                      ) : null}
                    </label>
                  )
                })}
                {!redemptionsQuery.isLoading && !discountsQuery.isLoading && candidates.length === 0 ? (
                  <div className='text-muted-foreground p-5 text-center text-sm'>{t('No available codes')}</div>
                ) : null}
              </div>
            </div>
          </div>

          <DialogFooter>
            <Button variant='outline' onClick={() => setOpen(false)}>{t('Cancel')}</Button>
            <Button onClick={submit} disabled={createMutation.isPending}>
              {createMutation.isPending ? t('Creating...') : t('Create and copy link')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </SectionPageLayout>
  )
}
