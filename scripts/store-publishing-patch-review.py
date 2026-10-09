write(store + 'new-variants.ts', header + '''import { STORE_FIXED_CONTENT_COPY as fixedCopy } from './fixed-content-copy'
import { STORE_PUBLISHING_COPY as copy } from './publishing-copy'
import type { StoreVariantInput } from './types'

export interface StoreNewVariantDraft extends StoreVariantInput { key: string }
export const STORE_MAX_VARIANTS = 200

export function storeNewVariantsError(variants: StoreVariantInput[], minimum: number | undefined): string | undefined {
  if (variants.length < 1 || variants.length > STORE_MAX_VARIANTS) {
    return copy.variantsLimit
  }
  const names = new Set<string>()
  for (const variant of variants) {
    const name = variant.name.trim()
    if (!name || name.includes('\\0') || new TextEncoder().encode(name).length > 200 ||
        names.has(name.toLowerCase()) || !Number.isSafeInteger(variant.price_quota) ||
        variant.price_quota <= 0 || minimum === undefined || variant.price_quota < minimum) {
      return copy.invalidVariants
    }
    names.add(name.toLowerCase())
    if (variant.template === 'fixed-content' && (!variant.fixed_content?.trim() ||
        variant.fixed_content.includes('\\0') || new TextEncoder().encode(variant.fixed_content).length > 128 * 1024)) {
      return fixedCopy.invalid
    }
  }
  if (!variants.some((variant) => variant.enabled)) {
    return copy.invalidVariants
  }
  return undefined
}

export function storeNewVariantInput(variant: StoreVariantInput): StoreVariantInput {
  return {
    name: variant.name.trim(), price_quota: variant.price_quota,
    template: variant.template, enabled: variant.enabled,
    ...(variant.template === 'fixed-content' ? { fixed_content: variant.fixed_content } : {}),
  }
}
''')
patch(store + 'api.ts', "if (body?.code === 'STORE_SELLER_TERMS_NOT_CONFIGURED') return publishingCopy.setup", "if (body?.code === 'STORE_SELLER_TERMS_NOT_CONFIGURED') { return publishingCopy.setup }")
patch(store + 'seller-page.tsx', "if (busy !== null || useAuthStore.getState().auth.user?.id !== user.id) return", "if (busy !== null || useAuthStore.getState().auth.user?.id !== user.id) { return }")
patch(store + 'seller-page.tsx', 'if (pending.product && pending.run) void action(pending.product, pending.run)', 'if (pending.product && pending.run) { void action(pending.product, pending.run) }')
patch(go + 'model/merchant_store_product_variants.go',
      '        if err := tx.Create(&variant).Error; err != nil { return err }',
      '''        if err := tx.Create(&variant).Error; err != nil { return err }
        // GORM fills false with the schema's true default on insert. Preserve
        // the seller's explicit choice, as the existing variant editor does.
        if !in.Enabled {
            if err := tx.Model(&variant).UpdateColumn("enabled", false).Error; err != nil { return err }
            variant.Enabled = false
        }''')
p = go + 'model/merchant_store_publishing_test.go'
write(p, Path(p).read_text() + '''
func TestMerchantStorePublishingKeepsDisabledSpecificationsDisabled(t *testing.T) {
    f := newVariantsFixture(t, "balance")
    in := storePublishingInput()
    in.Variants[1].Enabled = false
    p, err := SaveMerchantStoreProduct(f.seller.Id, "", in)
    require.NoError(t, err)
    var variant MerchantStoreVariant
    require.NoError(t, DB.First(&variant, "product_id = ? AND name = ?", p.ID, "Annual").Error)
    require.False(t, variant.Enabled)
    in.Variants[0].Enabled = false
    in.Variants[1].Enabled = true
    p, err = SaveMerchantStoreProduct(f.seller.Id, "", in)
    require.NoError(t, err)
    var first MerchantStoreVariant
    require.NoError(t, DB.First(&first, "id = ?", MerchantStoreDefaultVariantID(p.ID)).Error)
    require.False(t, first.Enabled)
}
''')

# These focused creation fixtures deliberately omit exchange-rate configuration.
# Select integral Credits so they test the requested behavior, not missing rates.
p = store + 'interactions.test.tsx'
s = Path(p).read_text()
a = s.index("test('initial specification validation blocks duplicate names")
b = s.index("test('seller terms recovery saves configuration", a)
part = s[a:b]
anchor = "  await input(document.querySelector<HTMLInputElement>('#store-price')!, '2')"
assert part.count(anchor) == 2
part = part.replace(anchor, '''  await storePublishingSelect(document.querySelector<HTMLSelectElement>('select[aria-label="Price currency"]')!, 'CREDIT')
''' + anchor)
anchor = "  await input(row.querySelector<HTMLInputElement>('[data-variant-price]')!, '5')"
assert part.count(anchor) == 1
part = part.replace(anchor, '''  await storePublishingSelect(row.querySelector<HTMLSelectElement>('select[aria-label="Price currency"]')!, 'CREDIT')
''' + anchor)
part = part.replace("  await click(button('Save draft'))", "  assert.equal(button('Save draft').disabled, false)\n  await click(button('Save draft'))")
write(p, s[:a] + part + s[b:])
