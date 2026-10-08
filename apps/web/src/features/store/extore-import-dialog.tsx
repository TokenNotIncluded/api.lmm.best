/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { useAuthStore } from '@/stores/auth-store'

import { StoreAPIError, storeApi } from './api'
import { EXTORE_DEFAULT_BASE_URL, EXTORE_IMPORT_MAX_BYTES, extoreDraft, normalizeExtoreBaseUrl, parseExtoreImport, type ExtoreImportDocument } from './extore-import'
import { useStoreMoneyDraft } from './money'
import { StoreError } from './shared'

const copy = {
  title: ['Import from Extore', '从 Extore 导入'],
  description: ['Import an Extore JSON file or paste product data. Review the selected variant and selling price before saving a draft.', '导入 Extore JSON 文件，或粘贴商品资料。选择规格并确认售价后保存为草稿。'],
  address: ['Extore address', 'Extore 地址'],
  addressHelp: ['The base URL is saved for this account in this browser. No password or API key is needed for file import.', '地址按账号保存在此浏览器中。文件导入不需要密码或 API Key。'],
  file: ['Extore JSON file', 'Extore JSON 文件'],
  paste: ['Paste product data', '粘贴商品资料'],
  preview: ['Preview import', '预览导入'],
  product: ['Product', '商品'],
  variant: ['Variant', '规格'],
  reference: ['Extore reference price', 'Extore 参考价'],
  unknown: ['Unknown', '未知'],
  disabled: ['Disabled in Extore', '已在 Extore 停用'],
  name: ['Product title', '商品标题'],
  price: ['Your selling price', '本站售价'],
  notice: ['One selected variant becomes one draft. Reference prices and Extore card counts are not copied as selling prices or inventory. Configure payment and inventory in the seller center before publishing.', '每次将所选规格导入为一个草稿。参考价和 Extore 卡密数量不会变成本站售价或库存。上架前需在卖家中心配置支付与库存。'],
  source: ['Source details', '来源详情'],
  unmapped: ['These fields remain in Extore and the original JSON; they are not converted into local delivery fields:', '以下字段仍由 Extore 和原始 JSON 保留，不会转换成本地交付配置：'],
  download: ['Save original Extore data', '保存 Extore 原始资料'],
  save: ['Import as draft', '导入为草稿'],
  saving: ['Importing…', '正在导入…'],
  saved: ['Draft saved. Open it in the seller center to finish configuration.', '草稿已保存。请在卖家中心打开并完成配置。'],
  empty: ['No products are present in this file.', '文件中没有商品。'],
  invalid: ['Check the Extore data, address, enabled variant, title, and selling price.', '请检查 Extore 资料、地址、可用规格、标题与售价。'],
  large: ['The JSON file must not exceed 2 MiB.', 'JSON 文件不能超过 2 MiB。'],
  uncertain: ['The save result is uncertain. Check the seller center before importing again. This request will not retry automatically.', '保存结果尚不确定。再次导入前请先检查卖家中心。本次请求不会自动重试。'],
  online: ['This option imports native JSON data. Online OAuth catalog fetching is not connected yet.', '此选项导入原生 JSON 资料，尚未接通在线 OAuth 商品拉取。'],
} as const

export function StoreExtoreImport() {
  const user = useAuthStore((state) => state.auth.user)
  return user ? <ExtoreImport key={user.id} userId={user.id} /> : null
}

function ExtoreImport({ userId }: { userId: number }) {
  const { t, i18n } = useTranslation()
  const text = (key: keyof typeof copy) => t(copy[key][0], { defaultValue: copy[key][i18n.language.startsWith('zh') ? 1 : 0] })
  const id = useId()
  const client = useQueryClient()
  const storageKey = `store:extore:base-url:${userId}`
  const [baseUrl, setBaseUrl] = useState(() => {
    try { return normalizeExtoreBaseUrl(localStorage.getItem(storageKey) ?? '') } catch { return EXTORE_DEFAULT_BASE_URL }
  })
  const [open, setOpen] = useState(false)
  const [source, setSource] = useState('')
  const [document, setDocument] = useState<ExtoreImportDocument | null>(null)
  const [productKey, setProductKey] = useState('')
  const [variantId, setVariantId] = useState('')
  const [title, setTitle] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [saved, setSaved] = useState(false)
  const [uncertain, setUncertain] = useState(false)
  const lock = useRef(false)
  const fileVersion = useRef(0)
  const price = useStoreMoneyDraft(0)
  const config = useQuery({ queryKey: ['store', 'config'], queryFn: storeApi.config, enabled: open, retry: false })
  const product = document?.products.find((item) => item.key === productKey)
  const variant = product?.variants.find((item) => item.id === variantId)

  function resetPreview() {
    setDocument(null)
    setProductKey('')
    setVariantId('')
    setSaved(false)
    setError(null)
    price.setInput('')
  }
  function selectVariant(nextProduct: NonNullable<typeof product>, nextId: string) {
    const nextVariant = nextProduct.variants.find((item) => item.id === nextId)
    setVariantId(nextId)
    setTitle(nextVariant ? `${nextProduct.name} · ${nextVariant.name}` : nextProduct.name)
    price.setInput('')
    setSaved(false)
    setError(null)
  }
  function selectProduct(next: NonNullable<typeof product>) {
    setProductKey(next.key)
    selectVariant(next, next.variants.find((item) => item.enabled)?.id ?? '')
  }
  function preview(raw = source) {
    resetPreview()
    try {
      const parsed = parseExtoreImport(raw, baseUrl, i18n.language)
      setBaseUrl(parsed.issuer)
      try { localStorage.setItem(storageKey, parsed.issuer) } catch { /* Import still works when browser storage is unavailable. */ }
      setDocument(parsed)
      if (parsed.products[0]) selectProduct(parsed.products[0])
    } catch {
      setError(new Error(text('invalid')))
    }
  }
  async function readFile(file?: File) {
    const version = ++fileVersion.current
    resetPreview()
    if (!file) return
    if (file.size > EXTORE_IMPORT_MAX_BYTES) { setError(new Error(text('large'))); return }
    try {
      const raw = await file.text()
      if (version !== fileVersion.current || useAuthStore.getState().auth.user?.id !== userId) return
      setSource(raw)
      preview(raw)
    } catch { setError(new Error(text('invalid'))) }
  }
  async function save() {
    if (lock.current || saved || uncertain || !product || !variant?.enabled || !config.data) return
    const quota = price.quota
    if (!quota || quota < (config.data.minimum_unit_price_quota ?? 0)) { setError(new Error(text('invalid'))); return }
    if (useAuthStore.getState().auth.user?.id !== userId) return
    lock.current = true
    setBusy(true)
    setError(null)
    try {
      const draft = extoreDraft(product, variant.id, title, quota)
      await storeApi.createProduct(draft)
      if (useAuthStore.getState().auth.user?.id !== userId) return
      setSaved(true)
      void client.invalidateQueries({ queryKey: ['store', 'my-products', userId] })
    } catch (issue) {
      if (useAuthStore.getState().auth.user?.id !== userId) return
      setError(issue)
      if (issue instanceof StoreAPIError && !issue.code) setUncertain(true)
    } finally { lock.current = false; setBusy(false) }
  }
  function downloadSource() {
    if (!product) return
    const original = new Blob([JSON.stringify(product.source, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(original)
    const link = window.document.createElement('a')
    link.href = url
    link.download = 'extore-product.json'
    link.click()
    window.setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  return (
    <div className='flex justify-end pb-4'>
      <Button variant='ghost' onClick={() => setOpen(true)}>{text('title')}</Button>
      <Dialog open={open} onOpenChange={(next) => { if (!lock.current) setOpen(next) }}>
        <DialogContent className='max-h-[90dvh] overflow-y-auto sm:max-w-2xl'>
          <DialogTitle>{text('title')}</DialogTitle>
          <DialogDescription>{text('description')}</DialogDescription>
          <fieldset disabled={busy || uncertain} className='min-w-0 space-y-6 border-0 p-0'>
            <details>
              <summary className='text-muted-foreground cursor-pointer py-2 text-sm'>{text('address')}</summary>
              <div className='space-y-2 pt-3'>
                <Label htmlFor={`${id}-base`}>Extore base URL</Label>
                <Input id={`${id}-base`} value={baseUrl} onChange={(event) => { ++fileVersion.current; setBaseUrl(event.target.value); resetPreview() }} placeholder={EXTORE_DEFAULT_BASE_URL} />
                <p className='text-muted-foreground text-xs leading-5'>{text('addressHelp')}</p>
              </div>
            </details>
            <div className='space-y-2'>
              <Label htmlFor={`${id}-file`}>{text('file')}</Label>
              <Input id={`${id}-file`} type='file' accept='.json,application/json' onChange={(event) => void readFile(event.target.files?.[0])} />
            </div>
            <details>
              <summary className='text-muted-foreground cursor-pointer py-2 text-sm'>{text('paste')}</summary>
              <div className='space-y-3 pt-3'>
                <Label htmlFor={`${id}-source`} className='sr-only'>{text('paste')}</Label>
                <Textarea id={`${id}-source`} rows={6} maxLength={EXTORE_IMPORT_MAX_BYTES} value={source} spellCheck={false} onChange={(event) => { ++fileVersion.current; setSource(event.target.value); resetPreview() }} />
                <Button variant='secondary' onClick={() => preview()} disabled={!source.trim()}>{text('preview')}</Button>
              </div>
            </details>
            {document?.products.length === 0 && <p role='status'>{text('empty')}</p>}
            {product && <>
              <div className='grid gap-5 sm:grid-cols-2'>
                <div className='space-y-2'>
                  <Label htmlFor={`${id}-product`}>{text('product')}</Label>
                  <select id={`${id}-product`} className='bg-muted/40 h-11 w-full rounded-md px-3 text-sm' value={productKey} onChange={(event) => { const next = document?.products.find((item) => item.key === event.target.value); if (next) selectProduct(next) }}>
                    {document?.products.map((item) => <option key={item.key} value={item.key}>{item.name}</option>)}
                  </select>
                </div>
                <div className='space-y-2'>
                  <Label htmlFor={`${id}-variant`}>{text('variant')}</Label>
                  <select id={`${id}-variant`} className='bg-muted/40 h-11 w-full rounded-md px-3 text-sm' value={variantId} onChange={(event) => selectVariant(product, event.target.value)}>
                    {!variantId && <option value=''>{text('disabled')}</option>}
                    {product.variants.map((item) => <option key={item.id} value={item.id} disabled={!item.enabled}>{item.name}{!item.enabled && ` (${text('disabled')})`}</option>)}
                  </select>
                </div>
              </div>
              <p className='text-muted-foreground text-sm'>{text('reference')}: {variant?.referencePrice == null ? text('unknown') : `${variant.referencePrice} ${variant.currency}`}</p>
              <div className='space-y-2'>
                <Label htmlFor={`${id}-title`}>{text('name')}</Label>
                <Input id={`${id}-title`} value={title} onChange={(event) => setTitle(event.target.value)} maxLength={200} />
              </div>
              <div className='space-y-2'>
                <Label htmlFor={`${id}-price`}>{text('price')} ({price.currency})</Label>
                <Input id={`${id}-price`} value={price.input} onChange={(event) => price.setInput(event.target.value)} inputMode='decimal' autoComplete='off' />
                <p className='text-muted-foreground text-xs leading-5'>{text('notice')}</p>
              </div>
              <details>
                <summary className='text-muted-foreground cursor-pointer py-2 text-sm'>{text('source')}</summary>
                <div className='space-y-3 pt-3'>
                  <p className='text-muted-foreground text-xs break-all'>{[document?.issuer, product.shopId, product.id, variantId, product.revision].filter(Boolean).join(' / ')}</p>
                  {product.unmappedFields.length > 0 && <p className='text-muted-foreground text-xs leading-5 break-words'>{text('unmapped')} {product.unmappedFields.join(', ')}</p>}
                  <Button variant='ghost' onClick={downloadSource}>{text('download')}</Button>
                </div>
              </details>
            </>}
          </fieldset>
          <StoreError error={error || config.error} />
          {uncertain && <p role='alert' className='text-sm leading-6'>{text('uncertain')}</p>}
          {saved && <p role='status' className='text-sm leading-6'>{text('saved')}</p>}
          <Button onClick={() => void save()} disabled={busy || saved || uncertain || !product || !variant?.enabled || !price.quota || !config.data}>{text(busy ? 'saving' : 'save')}</Button>
          <p className='text-muted-foreground text-xs leading-5'>{text('online')}</p>
        </DialogContent>
      </Dialog>
    </div>
  )
}
