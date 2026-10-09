from pathlib import Path
import json
import re
import subprocess

changed = set()

def write(path, text):
    Path(path).write_text(text, encoding='utf-8')
    changed.add(path)

def patch(path, old, new, count=1):
    text = Path(path).read_text()
    assert text.count(old) == count, (path, old[:100], text.count(old))
    write(path, text.replace(old, new))

go = 'apps/api-go/'
web = 'apps/web/'
store = web + 'src/features/store/'
header = '/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */\n'

# Keep the buyer agreement sentinel compatible with moderation and checkout.
patch(go + 'model/merchant_store_terms.go',
      'var ErrMerchantStoreSellerTerms = errors.New("current seller terms must be configured and accepted")',
      '''var ErrMerchantStoreSellerTerms = errors.New("current seller terms must be configured and accepted")

// A seller configures terms; a buyer accepts them. Preserve errors.Is for
// existing moderation callers without presenting a buyer action to a seller.
var ErrMerchantStoreSellerTermsNotConfigured error = &merchantStoreSellerTermsNotConfigured{}

type merchantStoreSellerTermsNotConfigured struct{}

func (*merchantStoreSellerTermsNotConfigured) Error() string { return "configure seller terms before publishing" }
func (*merchantStoreSellerTermsNotConfigured) Unwrap() error { return ErrMerchantStoreSellerTerms }''')
p = go + 'model/merchant_store_terms.go'
s = Path(p).read_text()
start = s.index('func storeRequireConfiguredSellerTerms(')
end = s.index('\nfunc ', start + 5)
part = s[start:end]
part = part.replace('if e != nil {\n\t\treturn e\n\t}\n\tif row == nil {\n\t\treturn ErrMerchantStoreSellerTerms',
                    'if errors.Is(e, ErrMerchantStoreSellerTerms) {\n\t\treturn ErrMerchantStoreSellerTermsNotConfigured\n\t}\n\tif e != nil {\n\t\treturn e\n\t}\n\tif row == nil {\n\t\treturn ErrMerchantStoreSellerTermsNotConfigured')
assert part.count('return ErrMerchantStoreSellerTermsNotConfigured') == 2
write(p, s[:start] + part + s[end:])
patch(go + 'controller/merchant_store.go',
      '\tcase errors.Is(err, model.ErrMerchantStoreSellerTerms):',
      '\tcase errors.Is(err, model.ErrMerchantStoreSellerTermsNotConfigured):\n\t\tstatus, code, message = http.StatusConflict, "STORE_SELLER_TERMS_NOT_CONFIGURED", "Configure seller terms before publishing or relisting this product."\n\tcase errors.Is(err, model.ErrMerchantStoreSellerTerms):')
patch(go + 'model/merchant_store.go', 'type MerchantStoreProductInput struct {',
      'type MerchantStoreProductInput struct {\n\t// Create only. The first entry is the named default specification.\n\tVariants []MerchantStoreVariantInput `json:"variants,omitempty"`')
patch(go + 'model/merchant_store_products.go',
      'func SaveMerchantStoreProduct(actor int, id string, in MerchantStoreProductInput) (*MerchantStoreProduct, error) {\n',
      'func SaveMerchantStoreProduct(actor int, id string, in MerchantStoreProductInput) (*MerchantStoreProduct, error) {\n\tif e := storePrepareProductVariants(id, &in); e != nil {\n\t\treturn nil, e\n\t}\n')
patch(go + 'model/merchant_store_products.go',
      '\t\tif e := populateMerchantStoreCategory(tx, &p, false); e != nil {',
      '\t\tif e := storeCreateProductVariants(tx, &p, defaultVariant, in.Variants); e != nil {\n\t\t\treturn e\n\t\t}\n\t\tif e := populateMerchantStoreCategory(tx, &p, false); e != nil {')
patch(go + 'controller/merchant_store.go',
      '\t\t"fixed_content_supported":           model.MerchantStoreFixedContentSupported(),',
      '\t\t"fixed_content_supported":           model.MerchantStoreFixedContentSupported(),\n\t\t"product_variants_create_supported": model.MerchantStoreProductVariantsCreateSupported(),')
write(go + 'model/merchant_store_product_variants.go', '''package model

import (
    "strings"
    "unicode/utf8"

    "github.com/google/uuid"
    "gorm.io/gorm"
)

func MerchantStoreProductVariantsCreateSupported() bool {
    floor, err := storeWriterGateRow(DB, "")
    return err == nil && floor >= 2 && floor <= MerchantStoreWriterCapability
}

// Existing editors retain their default-only contract. A create request may
// provide the complete initial set, but cannot use it to replace live stock IDs.
func storePrepareProductVariants(id string, in *MerchantStoreProductInput) error {
    if in.Variants == nil { return nil }
    if id != "" || len(in.Variants) == 0 || len(in.Variants) > MerchantStoreMaximumVariants {
        return ErrMerchantStoreInput
    }
    names := make(map[string]bool, len(in.Variants))
    enabled := false
    for i := range in.Variants {
        v := &in.Variants[i]
        if err := storeValidateVariant(v); err != nil { return err }
        name := strings.ToLower(v.Name)
        if !utf8.ValidString(v.Name) || strings.ContainsRune(v.Name, 0) || names[name] {
            return ErrMerchantStoreInput
        }
        names[name] = true
        enabled = enabled || v.Enabled
    }
    if !enabled { return ErrMerchantStoreInput }
    first := in.Variants[0]
    // Reject contradictory defaults rather than accepting two price sources.
    if in.PriceQuota != first.PriceQuota || in.Template != first.Template {
        return ErrMerchantStoreInput
    }
    if (in.FixedContent == nil) != (first.FixedContent == nil) ||
        (in.FixedContent != nil && *in.FixedContent != *first.FixedContent) {
        return ErrMerchantStoreInput
    }
    return nil
}

// Called only inside the owning product's creation transaction. A failed price,
// capability, or private-content check rolls back the product and every variant.
func storeCreateProductVariants(tx *gorm.DB, p *MerchantStoreProduct, first *MerchantStoreVariant, inputs []MerchantStoreVariantInput) error {
    if inputs == nil { return nil }
    if err := storeRequireVariantWriter(tx); err != nil { return err }
    for i, in := range inputs {
        if err := storeRequireMinimumUnitPrice(tx, in.PriceQuota); err != nil { return err }
        if i == 0 {
            if err := tx.Model(first).Updates(map[string]any{"name": in.Name, "enabled": in.Enabled}).Error; err != nil { return err }
            continue
        }
        variant := MerchantStoreVariant{
            ID: uuid.NewString(), ProductID: p.ID, Name: in.Name,
            PriceQuota: in.PriceQuota, Template: in.Template, Enabled: in.Enabled,
            CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
        }
        if err := tx.Create(&variant).Error; err != nil { return err }
        if err := storeSaveFixedContent(tx, p, &variant, in.FixedContent); err != nil { return err }
    }
    return nil
}
''')
write(go + 'model/merchant_store_publishing_test.go', '''package model

import (
    "encoding/json"
    "strings"
    "testing"

    "github.com/stretchr/testify/require"
)

func storePublishingInput() MerchantStoreProductInput {
    return MerchantStoreProductInput{
        Title: "Two specifications", Description: "Independent stock and prices",
        PriceQuota: 500000, Template: "card-key", DeliveryStrategy: "sequential",
        PaymentMethods: []string{"balance"},
        Variants: []MerchantStoreVariantInput{
            {Name: "Monthly", PriceQuota: 500000, Template: "card-key", Enabled: true},
            {Name: "Annual", PriceQuota: 6000000, Template: "text", Enabled: true},
        },
    }
}

func TestMerchantStorePublishingCreatesNamedVariantsAtomically(t *testing.T) {
    f := newVariantsFixture(t, "balance")
    raw, err := json.Marshal(storePublishingInput())
    require.NoError(t, err)
    var in MerchantStoreProductInput
    require.NoError(t, json.Unmarshal(raw, &in))
    require.Len(t, in.Variants, 2, "custom input decoding must preserve specifications")
    p, err := SaveMerchantStoreProduct(f.seller.Id, "", in)
    require.NoError(t, err)
    got, err := GetMerchantStoreProduct(f.seller.Id, p.ID)
    require.NoError(t, err)
    require.Len(t, got.Variants, 2)
    var first, other MerchantStoreVariant
    require.NoError(t, DB.First(&first, "id = ?", MerchantStoreDefaultVariantID(p.ID)).Error)
    require.Equal(t, "Monthly", first.Name)
    require.Equal(t, p.PriceQuota, first.PriceQuota)
    require.NoError(t, DB.First(&other, "product_id = ? AND id <> ?", p.ID, first.ID).Error)
    require.Equal(t, "Annual", other.Name)
    require.Equal(t, 6000000, other.PriceQuota)
    require.Equal(t, "text", other.Template)
    require.Equal(t, "draft", got.Status)
}

func TestMerchantStorePublishingRollsBackEveryVariantOnFailure(t *testing.T) {
    f := newVariantsFixture(t, "balance")
    in := storePublishingInput()
    forbidden := "private body on a stock template"
    in.Variants[1].FixedContent = &forbidden
    before := storeWriterSnapshot(t)
    _, err := SaveMerchantStoreProduct(f.seller.Id, "", in)
    require.ErrorIs(t, err, ErrMerchantStoreInput)
    require.Equal(t, before, storeWriterSnapshot(t), "no product, variant, or audit fragment survives")
}

func TestMerchantStorePublishingRejectsUnsafeVariantSets(t *testing.T) {
    f := newVariantsFixture(t, "balance")
    cases := map[string]func(*MerchantStoreProductInput){
        "empty": func(in *MerchantStoreProductInput) { in.Variants = []MerchantStoreVariantInput{} },
        "duplicate": func(in *MerchantStoreProductInput) { in.Variants[1].Name = " monthly " },
        "long name": func(in *MerchantStoreProductInput) { in.Variants[1].Name = strings.Repeat("规", 67) },
        "zero price": func(in *MerchantStoreProductInput) { in.Variants[1].PriceQuota = 0 },
        "all disabled": func(in *MerchantStoreProductInput) { for i := range in.Variants { in.Variants[i].Enabled = false } },
        "conflicting default": func(in *MerchantStoreProductInput) { in.PriceQuota++ },
        "over limit": func(in *MerchantStoreProductInput) { in.Variants = make([]MerchantStoreVariantInput, 201) },
    }
    for name, change := range cases {
        t.Run(name, func(t *testing.T) {
            before := storeWriterSnapshot(t)
            in := storePublishingInput()
            change(&in)
            _, err := SaveMerchantStoreProduct(f.seller.Id, "", in)
            require.ErrorIs(t, err, ErrMerchantStoreInput)
            require.Equal(t, before, storeWriterSnapshot(t))
        })
    }
    _, err := SaveMerchantStoreProduct(f.seller.Id, f.product.ID, storePublishingInput())
    require.ErrorIs(t, err, ErrMerchantStoreInput, "batch creation must not replace existing variant identities")
}

func TestMerchantStorePublishingLegacyGateCannotSilentlyDropVariants(t *testing.T) {
    f := newStoreFixture(t, "balance")
    require.False(t, MerchantStoreProductVariantsCreateSupported())
    before := storeWriterSnapshot(t)
    _, err := SaveMerchantStoreProduct(f.seller.Id, "", storePublishingInput())
    require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
    require.Equal(t, before, storeWriterSnapshot(t))
    in := storePublishingInput()
    in.Variants = nil
    _, err = SaveMerchantStoreProduct(f.seller.Id, "", in)
    require.NoError(t, err, "old single-price clients retain their create contract")
}

func TestMerchantStorePublishingTermsRecoveryDoesNotAcceptForBuyers(t *testing.T) {
    f := newVariantsFixture(t, "balance")
    storeAccessActivateTest(t)
    require.NoError(t, DB.Where("seller_id = ?", f.seller.Id).Delete(&MerchantStoreSellerTerms{}).Error)
    require.NoError(t, SetMerchantStoreProductListed(f.seller.Id, f.product.ID, false))
    err := SetMerchantStoreProductListed(f.seller.Id, f.product.ID, true)
    require.ErrorIs(t, err, ErrMerchantStoreSellerTermsNotConfigured)
    require.ErrorIs(t, err, ErrMerchantStoreSellerTerms)
    var status string
    require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Pluck("status", &status).Error)
    require.Equal(t, "off_shelf", status)
    // Persist terms through the real seller API, never manufacture acceptance.
    terms, err := SaveMerchantStoreSellerTerms(f.seller.Id, MerchantStoreTermsInput{Content: "Delivery and after-sales terms"})
    require.NoError(t, err)
    require.True(t, terms.Configured)
    require.False(t, terms.Accepted)
    require.NoError(t, SetMerchantStoreProductListed(f.seller.Id, f.product.ID, true))
}
''')
write(go + 'controller/merchant_store_publishing_test.go', '''package controller

import (
    "encoding/json"
    "net/http/httptest"
    "testing"

    "github.com/LIghtJUNction/api.lmm.best/model"
    "github.com/gin-gonic/gin"
    "github.com/stretchr/testify/require"
)

func TestMerchantStorePublishingSeparatesSellerConfigurationFromBuyerAcceptance(t *testing.T) {
    for _, item := range []struct{ err error; code string }{
        {model.ErrMerchantStoreSellerTermsNotConfigured, "STORE_SELLER_TERMS_NOT_CONFIGURED"},
        {model.ErrMerchantStoreSellerTerms, "STORE_SELLER_TERMS_REQUIRED"},
    } {
        recorder := httptest.NewRecorder()
        context, _ := gin.CreateTestContext(recorder)
        merchantStoreRespond(context, nil, item.err)
        require.Equal(t, 409, recorder.Code)
        var body map[string]any
        require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
        require.Equal(t, item.code, body["code"])
    }
}
''')

# UI copy remains in the normal locale resources, not hard-coded by language.
write(store + 'publishing-copy.ts', header + '''export const STORE_PUBLISHING_COPY = {
  setup: 'Configure seller terms before publishing or relisting this product.',
  setupHelp: 'Save your delivery and after-sales terms here. Buyers accept them separately when ordering.',
  continue: 'Continue publishing',
  offShelf: 'Temporarily unlist',
  offShelfHelp: 'Temporarily unlisting hides the product and keeps its approval. Existing orders remain available.',
  withdraw: 'Withdraw from review',
  withdrawTitle: 'Withdraw this product from review?',
  withdrawHelp: 'This clears the approval. Edit and submit the product again before publishing. Existing orders remain available.',
  variantsTitle: 'Specifications and prices',
  variantsHelp: 'Give each specification its own name, price and delivery content. All specifications are saved with this draft.',
  invalidVariants: 'Enter unique variant names and valid prices.',
  variantsLimit: 'A product can have at most 200 specifications.',
} as const
''')
patch(store + 'api.ts', "import { STORE_ACCESS_COPY as accessCopy } from './access-copy'",
      "import { STORE_ACCESS_COPY as accessCopy } from './access-copy'\nimport { STORE_PUBLISHING_COPY as publishingCopy } from './publishing-copy'")
p = store + 'api.ts'
s = Path(p).read_text()
match = re.search(r"(\s+if \(body\??\.code === 'STORE_VARIANT_REQUIRED'\))", s)
assert match, 'API error mapping anchor'
s = s[:match.start()] + "\n  if (body?.code === 'STORE_SELLER_TERMS_NOT_CONFIGURED') return publishingCopy.setup" + s[match.start():]
write(p, s)
patch(store + 'types.ts', '> & { fixed_content?: string }\nexport interface StoreOrder',
      '> & { fixed_content?: string; variants?: StoreVariantInput[] }\nexport interface StoreOrder')
patch(store + 'types.ts', 'export interface StoreConfig {',
      'export interface StoreConfig {\n  product_variants_create_supported?: boolean')
patch(store + 'money.ts', '      setDraft({ input, key, quota })',
      '      setDraft({ input, key, quota })\n      return quota')
write(store + 'new-variants.ts', header + '''import { STORE_PUBLISHING_COPY as copy } from './publishing-copy'
import type { StoreVariantInput } from './types'

export interface StoreNewVariantDraft extends StoreVariantInput { key: string }
export const STORE_MAX_VARIANTS = 200

export function storeNewVariantsError(variants: StoreVariantInput[], minimum: number | undefined): string | undefined {
  if (variants.length < 1 || variants.length > STORE_MAX_VARIANTS) return copy.variantsLimit
  const names = new Set<string>()
  for (const variant of variants) {
    const name = variant.name.trim()
    if (!name || name.includes('\\0') || new TextEncoder().encode(name).length > 200 ||
        names.has(name.toLowerCase()) || !Number.isSafeInteger(variant.price_quota) ||
        variant.price_quota <= 0 || minimum === undefined || variant.price_quota < minimum) return copy.invalidVariants
    names.add(name.toLowerCase())
    if (variant.template === 'fixed-content' && (!variant.fixed_content?.trim() ||
        variant.fixed_content.includes('\\0') || new TextEncoder().encode(variant.fixed_content).length > 128 * 1024)) {
      return 'Enter delivery content within 128 KiB.'
    }
  }
  if (!variants.some((variant) => variant.enabled)) return copy.invalidVariants
  return undefined
}

// Never send local row keys or hidden draft content for a stock-backed template.
export function storeNewVariantInput(variant: StoreVariantInput): StoreVariantInput {
  return {
    name: variant.name.trim(), price_quota: variant.price_quota,
    template: variant.template, enabled: variant.enabled,
    ...(variant.template === 'fixed-content' ? { fixed_content: variant.fixed_content } : {}),
  }
}
''')
write(store + 'new-variants-editor.tsx', header + '''import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { Plus, Trash2 } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

import { DELIVERY_TEMPLATES } from './delivery-template'
import { useStoreMoneyDraft } from './money'
import { STORE_MAX_VARIANTS, type StoreNewVariantDraft } from './new-variants'
import { STORE_PUBLISHING_COPY as copy } from './publishing-copy'

export function StoreNewVariantsEditor({ variants, disabled, fixedContentSupported, onChange }: {
  variants: StoreNewVariantDraft[]
  disabled: boolean
  fixedContentSupported: boolean
  onChange: (variants: StoreNewVariantDraft[]) => void
}) {
  const { t } = useTranslation()
  return (
    <section className='space-y-4 rounded-2xl bg-muted/35 p-4 sm:p-5' aria-label={t(copy.variantsTitle)}>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div className='space-y-1'>
          <h3 className='font-semibold'>{t(copy.variantsTitle)}</h3>
          <p className='text-muted-foreground max-w-prose text-xs leading-5'>{t(copy.variantsHelp)}</p>
        </div>
        <Button type='button' variant='secondary' disabled={disabled || variants.length >= STORE_MAX_VARIANTS - 1}
          onClick={() => onChange([...variants, { key: crypto.randomUUID(), name: '', price_quota: Number.NaN, template: 'card-key', enabled: true }])}>
          <Plus className='size-4' aria-hidden='true' />{t('Add variant')}
        </Button>
      </div>
      {variants.map((variant, index) => (
        <StoreNewVariantRow key={variant.key} value={variant} index={index + 2}
          disabled={disabled} fixedContentSupported={fixedContentSupported}
          onChange={(value) => onChange(variants.map((row) => row.key === variant.key ? value : row))}
          onRemove={() => onChange(variants.filter((row) => row.key !== variant.key))} />
      ))}
    </section>
  )
}

function StoreNewVariantRow({ value, index, disabled, fixedContentSupported, onChange, onRemove }: {
  value: StoreNewVariantDraft
  index: number
  disabled: boolean
  fixedContentSupported: boolean
  onChange: (value: StoreNewVariantDraft) => void
  onRemove: () => void
}) {
  const { t } = useTranslation()
  const id = useId()
  const money = useStoreMoneyDraft(value.price_quota)
  return (
    <fieldset className='min-w-0 space-y-4 rounded-xl bg-background p-4' data-store-new-variant={index} disabled={disabled}>
      <legend className='sr-only'>{t('Product variant')} {index}</legend>
      <div className='flex items-center justify-between gap-3'>
        <span className='text-muted-foreground text-xs'>{t('Product variant')} {index}</span>
        <Button type='button' size='sm' variant='ghost' onClick={onRemove} disabled={disabled}
          aria-label={`${t('Remove')} ${t('Product variant')} ${index}`}>
          <Trash2 className='size-4' aria-hidden='true' />{t('Remove')}
        </Button>
      </div>
      <div className='grid gap-4 sm:grid-cols-2'>
        <div className='space-y-2'>
          <Label htmlFor={`${id}-name`}>{t('Variant name')}</Label>
          <Input id={`${id}-name`} data-variant-name required maxLength={200} value={value.name}
            onChange={(event) => onChange({ ...value, name: event.target.value })} placeholder={t('For example: Plus · 2 months')} />
        </div>
        <div className='space-y-2'>
          <Label htmlFor={`${id}-price`}>{t('Unit price')}</Label>
          <div className='flex gap-2'>
            <select aria-label={t('Price currency')} className='h-11 rounded-xl bg-muted px-3 text-sm' value={money.currency}
              onChange={(event) => money.setCurrency(event.target.value as 'USD' | 'CNY' | 'CREDIT')}>
              {(['USD', 'CNY', 'CREDIT'] as const).map((currency) => <option key={currency} value={currency}>{currency}</option>)}
            </select>
            <Input id={`${id}-price`} data-variant-price inputMode='decimal' required value={money.input}
              onChange={(event) => onChange({ ...value, price_quota: money.setInput(event.target.value) ?? Number.NaN })} />
          </div>
        </div>
        <div className='space-y-2'>
          <Label htmlFor={`${id}-template`}>{t('Delivery template')}</Label>
          <select id={`${id}-template`} data-variant-template className='h-11 w-full rounded-xl bg-muted px-3 text-sm'
            value={value.template} onChange={(event) => onChange({ ...value, template: event.target.value as StoreNewVariantDraft['template'] })}>
            {Object.entries(DELIVERY_TEMPLATES).filter(([key]) => key !== 'fixed-content' || fixedContentSupported)
              .map(([key, template]) => <option key={key} value={key}>{t(template.label)}</option>)}
          </select>
        </div>
        <label className='flex items-center justify-between gap-3 text-sm'>
          {t('Variant enabled')}
          <Switch checked={value.enabled} disabled={disabled} onCheckedChange={(enabled) => onChange({ ...value, enabled })} />
        </label>
      </div>
      {value.template === 'fixed-content' && (
        <div className='space-y-2'>
          <Label htmlFor={`${id}-content`}>{t('Delivery content')}</Label>
          <Textarea id={`${id}-content`} data-variant-content rows={4} required value={value.fixed_content ?? ''}
            onChange={(event) => onChange({ ...value, fixed_content: event.target.value })} />
          <p className='text-muted-foreground text-xs leading-5'>{t('Only the seller and eligible paid buyers can read this content. Existing orders keep the content saved at purchase.')}</p>
        </div>
      )}
    </fieldset>
  )
}
''')
write(store + 'seller-terms-dialog.tsx', header + '''import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'

import { storeAccessApi } from './access-api'
import { STORE_ACCESS_COPY as accessCopy } from './access-copy'
import { StoreMerchantTermsForm } from './merchant-terms'
import { STORE_PUBLISHING_COPY as copy } from './publishing-copy'
import { StoreError, StoreLoading } from './shared'

export function StoreSellerTermsDialog({ sellerId, onClose, onContinue }: {
  sellerId: number
  onClose: () => void
  onContinue?: () => void
}) {
  const { t } = useTranslation()
  const query = useQuery({ queryKey: ['store', 'my-terms', sellerId], queryFn: storeAccessApi.myTerms, retry: false, staleTime: 0 })
  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent className='max-h-[85dvh] overflow-y-auto sm:max-w-2xl'>
        <DialogTitle>{t(accessCopy.termsTitle)}</DialogTitle>
        <DialogDescription>{t(onContinue ? copy.setup : copy.setupHelp)}</DialogDescription>
        {query.isPending && <StoreLoading />}
        <StoreError error={query.error} retry={() => void query.refetch()} />
        {query.data && (
          <StoreMerchantTermsForm key={query.data.version} terms={query.data}
            onReload={async () => { await query.refetch() }}
            onSaved={async () => { await query.refetch() }} />
        )}
        {onContinue && (
          <Button type='button' disabled={!query.data?.configured || query.isFetching || !!query.error} onClick={onContinue}>
            {t(copy.continue)}
          </Button>
        )}
        <p className='text-muted-foreground text-xs leading-5'>{t(copy.setupHelp)}</p>
      </DialogContent>
    </Dialog>
  )
}
''')

# Preserve existing callbacks and inventory UI while making publishing repairable.
p = store + 'seller-page.tsx'
patch(p, "import { storeApi } from './api'", "import { storeApi, StoreAPIError } from './api'\nimport { StoreSellerTermsDialog } from './seller-terms-dialog'\nimport { STORE_PUBLISHING_COPY as publishingCopy } from './publishing-copy'\nimport { StoreNewVariantsEditor } from './new-variants-editor'\nimport { storeNewVariantInput, storeNewVariantsError, type StoreNewVariantDraft } from './new-variants'")
patch(p, "  const [busy, setBusy] = useState<string | null>(null)", "  const [termsSetup, setTermsSetup] = useState<{ product?: StoreProduct; run?: () => Promise<unknown> } | null>(null)\n  const [busy, setBusy] = useState<string | null>(null)")
patch(p, '    if (busy !== null) return', '    if (busy !== null || useAuthStore.getState().auth.user?.id !== user.id) return')
s = Path(p).read_text()
a = s.index('  async function action(')
b = s.index('  function confirmLifecycle()', a)
part = s[a:b].replace('    } catch (issue) {\n      setError(issue)', '''    } catch (issue) {
      if (issue instanceof StoreAPIError &&
          ['STORE_SELLER_TERMS_NOT_CONFIGURED', 'STORE_SELLER_TERMS_REQUIRED'].includes(issue.code) &&
          useAuthStore.getState().auth.user?.id === user.id) {
        setTermsSetup({ product, run: fn })
      } else {
        setError(issue)
      }''')
assert 'setTermsSetup({ product, run: fn })' in part
write(p, s[:a] + part + s[b:])
# Existing compatibility code for /unlist remains, but the UI names its actual effect.
s = Path(p).read_text().replace("'Unlist product?'", 'publishingCopy.withdrawTitle').replace("'Unlist product'", 'publishingCopy.withdraw')
s = s.replace("'Remove {{title}} from the store. You can edit and submit it for review again. Existing orders remain accessible.'", 'publishingCopy.withdrawHelp')
s = s.replace('salesCopy.offShelf)', 'publishingCopy.offShelf)')
s = s.replace(': salesCopy.offShelf', ': publishingCopy.offShelf')
s = s.replace("<DialogContent className='sm:max-w-3xl'>", "<DialogContent className='max-h-[90dvh] overflow-y-auto rounded-3xl sm:max-w-3xl'>")
# Keep normal product actions readable and reachable on a small screen.
s = s.replace("<div className='flex flex-wrap gap-2'>\n                    <Button", "<div className='flex flex-wrap gap-2 [&_button]:min-h-10 [&_button]:rounded-full'>\n                    <Button")
anchor = '      {editing && ('
assert anchor in s
s = s.replace(anchor, '''      {termsSetup && (
        <StoreSellerTermsDialog sellerId={user.id} onClose={() => setTermsSetup(null)}
          onContinue={termsSetup.product && termsSetup.run ? () => {
            const pending = termsSetup
            setTermsSetup(null)
            if (pending.product && pending.run) void action(pending.product, pending.run)
          } : undefined} />
      )}
''' + anchor, 1)
# Add capabilities only to the product editor, not the saved-variant manager.
a = s.index('      {editing && (')
b = s.index('export function StoreProductEditor(', a)
part = s[a:b]
anchor = '          fixedContentSupported={config.data?.fixed_content_supported === true}'
assert anchor in part
part = part.replace(anchor, anchor + '\n          variantsCreateSupported={config.data?.product_variants_create_supported === true}', 1)
s = s[:a] + part + s[b:]
# Add editor state and compose a single create body. Existing editors omit variants.
a = s.index('export function StoreProductEditor(')
head, editor = s[:a], s[a:]
assert '  fixedContentSupported = false,' in editor
editor = editor.replace('  fixedContentSupported = false,', '  fixedContentSupported = false,\n  variantsCreateSupported = false,', 1)
assert '  fixedContentSupported?: boolean' in editor
editor = editor.replace('  fixedContentSupported?: boolean', '  fixedContentSupported?: boolean\n  variantsCreateSupported?: boolean', 1)
anchor = '  const initialProduct = product ?? importedDraft?.fields'
assert anchor in editor
editor = editor.replace(anchor, "  const [defaultVariantName, setDefaultVariantName] = useState(t('Default variant'))\n  const [newVariants, setNewVariants] = useState<StoreNewVariantDraft[]>([])\n" + anchor, 1)
anchor = '      if (product) await storeApi.updateProduct(product.id, body)'
assert anchor in editor
editor = editor.replace(anchor, '''      if (!product && variantsCreateSupported) {
        const variants = [storeNewVariantInput({
          name: defaultVariantName, price_quota: body.price_quota,
          template: body.template, enabled: true, fixed_content: body.fixed_content,
        }), ...newVariants.map(storeNewVariantInput)]
        const problem = storeNewVariantsError(variants, minimum)
        if (problem) throw new Error(t(problem))
        body.variants = variants
      }
''' + anchor, 1)
anchor = "              <Label htmlFor='store-price'>"
assert anchor in editor
editor = editor.replace(anchor, '''              {!product && variantsCreateSupported && (
                <div className='space-y-2 pb-2'>
                  <Label htmlFor='store-default-variant-name'>{t('Variant name')} · {t('Default variant')}</Label>
                  <Input id='store-default-variant-name' required maxLength={200} value={defaultVariantName}
                    disabled={busy} onChange={(event) => setDefaultVariantName(event.target.value)} />
                </div>
              )}
''' + anchor, 1)
anchor = "          <fieldset className='space-y-2 border-t pt-4'>"
assert anchor in editor
editor = editor.replace(anchor, '''          {!product && variantsCreateSupported && (
            <StoreNewVariantsEditor variants={newVariants} disabled={busy}
              fixedContentSupported={fixedContentSupported} onChange={setNewVariants} />
          )}
''' + anchor, 1)
editor = editor.replace("<div className='flex justify-end gap-2 border-t pt-4'>", "<div className='sticky bottom-0 -mx-1 flex justify-end gap-3 bg-background/95 px-1 py-4 backdrop-blur-sm'>", 1)
write(p, head + editor)
# Explain state effects beside the existing controls, without adding a second state.
p = store + 'seller-page.tsx'
patch(p, '                <StoreInventoryTotals product={product} />', '''                <details className='text-muted-foreground text-xs leading-5'>
                  <summary className='cursor-pointer py-1'>{t(publishingCopy.offShelf)} / {t(publishingCopy.withdraw)}</summary>
                  <p className='py-1'>{t(publishingCopy.offShelfHelp)}</p>
                  <p className='py-1'>{t(publishingCopy.withdrawHelp)}</p>
                </details>
                <StoreInventoryTotals product={product} />''')
# Entry point is visible immediately, rather than hidden inside a closed disclosure.
p = store + 'variants-manager.tsx'
s = Path(p).read_text()
s = s.replace("    <details className='space-y-3 border-t pt-3'>", "    <details open className='space-y-3 rounded-2xl bg-muted/25 p-4'>", 1)
write(p, s)

# Align existing assertions with the intentionally clarified labels.
p = store + 'interactions.test.tsx'
s = Path(p).read_text().replace("'Unlist product?'", "'Withdraw this product from review?'").replace("'Unlist product'", "'Withdraw from review'")
sales = Path(store + 'sales-limit-copy.ts').read_text()
old_off = re.search(r"offShelf:\s*'([^']+)'", sales)
assert old_off
s = s.replace(repr(old_off.group(1)), "'Temporarily unlist'")
write(p, s)

# Mobile filters now use the actual accessible select, not the retired native one.
p = store + 'store-mobile.test.tsx'
s = Path(p).read_text()
a = s.index('  const guest = required(')
b = s.index('  assert.equal(', a)
s = s[:a] + '''  const guest = required(document.querySelector<HTMLButtonElement>('#store-catalogue-guestPurchase'))
  await click(guest)
  const options = [...document.querySelectorAll<HTMLElement>('[role="option"]')]
  // The third option is the explicit false choice, after any and true.
  assert.equal(options.length, 3)
  await click(required(options[2]))
''' + s[b:]
write(p, s)

write(store + 'new-variants.test.ts', header + '''import assert from 'node:assert/strict'
import { test } from 'node:test'
import { storeNewVariantInput, storeNewVariantsError } from './new-variants'
import type { StoreVariantInput } from './types'

const variant = (name = 'Monthly'): StoreVariantInput => ({ name, price_quota: 500000, template: 'card-key', enabled: true })
test('new specification validation preserves exact prices and detects ambiguous names', () => {
  assert.equal(storeNewVariantsError([variant(), { ...variant('Annual'), price_quota: 6000000 }], 0), undefined)
  for (const variants of [[], [variant(), variant(' monthly ')], [{ ...variant(), price_quota: Number.NaN }], [{ ...variant(), name: '规'.repeat(67) }], [{ ...variant(), enabled: false }]]) {
    assert.ok(storeNewVariantsError(variants, 0))
  }
  assert.ok(storeNewVariantsError([variant()], undefined))
  assert.ok(storeNewVariantsError([variant()], 500001))
  assert.ok(storeNewVariantsError(Array.from({ length: 201 }, (_, i) => variant(String(i))), 0))
})
test('new fixed specifications require private content and never leak it to stock templates', () => {
  assert.ok(storeNewVariantsError([{ ...variant(), template: 'fixed-content' }], 0))
  assert.ok(storeNewVariantsError([{ ...variant(), template: 'fixed-content', fixed_content: 'a\\0b' }], 0))
  const fixed = { ...variant(), template: 'fixed-content' as const, fixed_content: 'private delivery' }
  assert.equal(storeNewVariantsError([fixed], 0), undefined)
  assert.equal(storeNewVariantInput(fixed).fixed_content, 'private delivery')
  assert.deepEqual(storeNewVariantInput({ ...fixed, template: 'card-key' }), variant())
})
''')

# Add translations without reserializing unrelated 1 MiB locale documents.
translations = {
'Configure seller terms before publishing or relisting this product.': ['请先配置卖家条款，再发布或重新上架商品。', '請先設定賣家條款，再發佈或重新上架商品。', 'Configurez les conditions du vendeur avant de publier ou remettre ce produit en vente.', '公開または再出品する前に販売者の規約を設定してください。', 'Перед публикацией или повторным размещением настройте условия продавца.', 'Hãy thiết lập điều khoản người bán trước khi đăng hoặc đăng bán lại sản phẩm.'],
'Save your delivery and after-sales terms here. Buyers accept them separately when ordering.': ['在这里保存交付和售后条款。买家会在下单时单独确认。', '在這裡儲存交付和售後條款。買家會在下單時另外確認。', 'Enregistrez ici vos conditions de livraison et de service après-vente. Les acheteurs les acceptent lors de la commande.', '配送と購入後の対応に関する規約を保存します。購入者は注文時に別途同意します。', 'Сохраните условия доставки и обслуживания. Покупатель принимает их отдельно при заказе.', 'Lưu điều khoản giao hàng và hậu mãi tại đây. Người mua sẽ xác nhận riêng khi đặt hàng.'],
'Continue publishing': ['继续上架', '繼續上架', 'Continuer la publication', '公開を続ける', 'Продолжить публикацию', 'Tiếp tục đăng bán'],
'Temporarily unlist': ['暂时下架', '暫時下架', 'Retirer temporairement', '一時的に出品を停止', 'Временно снять с продажи', 'Tạm ngừng niêm yết'],
'Temporarily unlisting hides the product and keeps its approval. Existing orders remain available.': ['暂时隐藏商品，保留审核结果，可再次上架。已有订单不受影响。', '暫時隱藏商品，保留審核結果，可再次上架。已有訂單不受影響。', 'Le produit est masqué et son approbation est conservée. Les commandes existantes restent accessibles.', '商品を非表示にし、承認は保持します。既存の注文には影響しません。', 'Товар скрывается, одобрение сохраняется. Существующие заказы остаются доступны.', 'Ẩn sản phẩm và giữ kết quả duyệt. Các đơn hàng hiện có vẫn truy cập được.'],
'Withdraw from review': ['撤回审核', '撤回審核', 'Retirer de la validation', '審査を取り下げる', 'Отозвать с проверки', 'Rút khỏi xét duyệt'],
'Withdraw this product from review?': ['撤回此商品的审核？', '撤回此商品的審核？', 'Retirer ce produit de la validation ?', 'この商品の審査を取り下げますか？', 'Отозвать этот товар с проверки?', 'Rút sản phẩm này khỏi xét duyệt?'],
'This clears the approval. Edit and submit the product again before publishing. Existing orders remain available.': ['清除审核结果。编辑并重新提交审核后才能上架。已有订单不受影响。', '清除審核結果。編輯並重新提交審核後才能上架。已有訂單不受影響。', 'L’approbation est annulée. Modifiez le produit et soumettez-le à nouveau avant publication. Les commandes existantes restent accessibles.', '承認を取り消します。編集して再審査を申請してから公開してください。既存の注文には影響しません。', 'Одобрение будет отменено. Измените товар и отправьте на проверку перед публикацией. Существующие заказы останутся доступны.', 'Xóa kết quả duyệt. Chỉnh sửa và gửi duyệt lại trước khi đăng bán. Các đơn hàng hiện có vẫn truy cập được.'],
'Specifications and prices': ['规格与价格', '規格與價格', 'Variantes et prix', '仕様と価格', 'Варианты и цены', 'Biến thể và giá'],
'Give each specification its own name, price and delivery content. All specifications are saved with this draft.': ['每个规格可设置独立名称、价格和交付内容，所有规格与商品草稿一起保存。', '每個規格可設定獨立名稱、價格和交付內容，所有規格與商品草稿一起儲存。', 'Définissez un nom, un prix et un contenu par variante. Toutes les variantes sont enregistrées avec ce brouillon.', '仕様ごとに名前、価格、納品内容を設定します。すべて下書きと一緒に保存されます。', 'Задайте каждому варианту название, цену и содержимое. Все варианты сохраняются вместе с черновиком.', 'Đặt tên, giá và nội dung giao riêng cho từng biến thể. Tất cả được lưu cùng bản nháp.'],
'Enter unique variant names and valid prices.': ['请填写不重复的规格名称和有效价格。', '請填寫不重複的規格名稱和有效價格。', 'Saisissez des noms de variantes distincts et des prix valides.', '重複しない仕様名と有効な価格を入力してください。', 'Введите уникальные названия вариантов и допустимые цены.', 'Nhập tên biến thể không trùng và giá hợp lệ.'],
'A product can have at most 200 specifications.': ['每个商品最多支持 200 个规格。', '每個商品最多支援 200 個規格。', 'Un produit peut avoir au maximum 200 variantes.', '商品には最大200の仕様を設定できます。', 'Товар может иметь не более 200 вариантов.', 'Mỗi sản phẩm có tối đa 200 biến thể.'],
}
for locale in ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi']:
    p = web + 'src/i18n/locales/' + locale + '.json'
    s = Path(p).read_text()
    known = json.loads(s)['translation']
    entries = {}
    for key, values in translations.items():
        value = key if locale == 'en' else values[['zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi'].index(locale)]
        if key not in known: entries[key] = value
    insertion = ''.join('    ' + json.dumps(key, ensure_ascii=False) + ': ' + json.dumps(value, ensure_ascii=False) + ',\n' for key, value in entries.items())
    anchor = '  "translation": {\n'
    assert anchor in s
    write(p, s.replace(anchor, anchor + insertion, 1))

# Format only intended source files; never rewrite legacy license headers.
gofiles = sorted(p for p in changed if p.endswith('.go'))
subprocess.run(['gofmt', '-w', *gofiles], check=True)
front = sorted(p.removeprefix(web) for p in changed if p.startswith(web) and p.endswith(('.ts', '.tsx')))
subprocess.run(['bun', 'x', '--no-install', 'oxfmt', '-c', '.oxfmtrc.json', '--write', *front], cwd=web, check=True)
Path('/tmp/store-publishing-paths.json').write_text(json.dumps(sorted(changed)))
print('Prepared reviewed changes in', len(changed), 'files')
