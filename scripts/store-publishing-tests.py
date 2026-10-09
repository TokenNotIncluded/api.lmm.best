# Executed inside the temporary workbench patch before its targeted formatting.
p = store + 'new-variants.ts'
s = Path(p).read_text().replace("import { STORE_PUBLISHING_COPY as copy }", "import { STORE_FIXED_CONTENT_COPY as fixedCopy } from './fixed-content-copy'\nimport { STORE_PUBLISHING_COPY as copy }")
s = s.replace("return 'Enter delivery content within 128 KiB.'", 'return fixedCopy.invalid')
write(p, s)
p = store + 'new-variants-editor.tsx'
s = Path(p).read_text().replace("placeholder={t('For example: Plus · 2 months')}", "placeholder={t('Variant name')}")
s = s.replace("t('Only the seller and eligible paid buyers can read this content. Existing orders keep the content saved at purchase.')", 't(DELIVERY_TEMPLATES[value.template].help)')
write(p, s)

p = store + 'interactions.test.tsx'
s = Path(p).read_text()
s += '''

async function storePublishingSelect(node: HTMLSelectElement, value: string) {
  assert.ok(node)
  await act(async () => {
    node.value = value
    node.dispatchEvent(new Event('change', { bubbles: true }))
    await flush()
  })
}

test('new products save named specifications in one request and retain them after a failed save', async () => {
  owner(9)
  const requests: { url: string; body: Record<string, unknown> }[] = []
  let saved = 0
  api.post = (async (url: string, body: Record<string, unknown>) => {
    requests.push({ url, body })
    if (requests.length === 1) throw new Error('temporary fixture failure')
    return result(product)
  }) as typeof api.post
  await mount(<StoreProductEditor allowedMethods={['balance']} minimumPriceQuota={0}
    variantsCreateSupported onClose={() => {}} onSaved={async () => { saved++ }} />)
  await input(document.querySelector<HTMLInputElement>('#store-title')!, 'Subscription')
  await storePublishingSelect(document.querySelector<HTMLSelectElement>('select[aria-label="Price currency"]')!, 'CREDIT')
  await input(document.querySelector<HTMLInputElement>('#store-price')!, '100')
  await input(document.querySelector<HTMLInputElement>('#store-default-variant-name')!, 'Monthly')
  await click(button('Add variant'))
  const row = document.querySelector<HTMLElement>('[data-store-new-variant]')!
  assert.ok(row)
  await input(row.querySelector<HTMLInputElement>('[data-variant-name]')!, 'Annual')
  await storePublishingSelect(row.querySelector<HTMLSelectElement>('select[aria-label="Price currency"]')!, 'CREDIT')
  await input(row.querySelector<HTMLInputElement>('[data-variant-price]')!, '250')
  await click(button('Save draft'))
  assert.equal(requests.length, 1)
  assert.equal(saved, 0)
  assert.equal(row.querySelector<HTMLInputElement>('[data-variant-name]')!.value, 'Annual')
  await click(button('Save draft'))
  assert.equal(saved, 1)
  assert.equal(requests.length, 2)
  assert.equal(requests[1].url, '/api/store/products')
  assert.deepEqual(requests[1].body, requests[0].body)
  const variants = requests[1].body.variants as Record<string, unknown>[]
  assert.equal(variants.length, 2)
  assert.equal(variants[0].name, 'Monthly')
  assert.equal(variants[1].name, 'Annual')
  assert.equal(variants[0].price_quota, requests[1].body.price_quota)
  assert.equal(variants[1].price_quota, Number(variants[0].price_quota) * 2.5)
  assert.equal(variants[0].key, undefined)
  assert.equal(variants[1].key, undefined)
})

test('initial specification validation blocks duplicate names and removing a row restores a valid draft', async () => {
  owner(9)
  const requests: Record<string, unknown>[] = []
  api.post = (async (_url: string, body: Record<string, unknown>) => {
    requests.push(body)
    return result(product)
  }) as typeof api.post
  await mount(<StoreProductEditor allowedMethods={['balance']} minimumPriceQuota={0}
    variantsCreateSupported onClose={() => {}} onSaved={async () => {}} />)
  await input(document.querySelector<HTMLInputElement>('#store-title')!, 'Subscription')
  await input(document.querySelector<HTMLInputElement>('#store-price')!, '2')
  await input(document.querySelector<HTMLInputElement>('#store-default-variant-name')!, 'Monthly')
  await click(button('Add variant'))
  const row = document.querySelector<HTMLElement>('[data-store-new-variant]')!
  await input(row.querySelector<HTMLInputElement>('[data-variant-name]')!, ' monthly ')
  await input(row.querySelector<HTMLInputElement>('[data-variant-price]')!, '5')
  await click(button('Save draft'))
  assert.equal(requests.length, 0)
  assert.ok(document.body.textContent?.includes('Enter unique variant names and valid prices.'))
  await click(row.querySelector<HTMLButtonElement>('button')!)
  await click(button('Save draft'))
  assert.equal(requests.length, 1)
  assert.equal((requests[0].variants as unknown[]).length, 1)
})

test('legacy creation does not silently send specification fields to an older backend', async () => {
  owner(9)
  const requests: Record<string, unknown>[] = []
  api.post = (async (_url: string, body: Record<string, unknown>) => {
    requests.push(body)
    return result(product)
  }) as typeof api.post
  await mount(<StoreProductEditor allowedMethods={['balance']} minimumPriceQuota={0}
    onClose={() => {}} onSaved={async () => {}} />)
  assert.equal(document.querySelector('#store-default-variant-name'), null)
  assert.equal([...document.querySelectorAll('button')].some((node) => node.textContent?.trim() === 'Add variant'), false)
  await input(document.querySelector<HTMLInputElement>('#store-title')!, 'Legacy draft')
  await input(document.querySelector<HTMLInputElement>('#store-price')!, '2')
  await click(button('Save draft'))
  assert.equal(requests.length, 1)
  assert.equal(requests[0].variants, undefined)
})

test('seller terms recovery saves configuration and requires an explicit publishing retry', async () => {
  owner(9)
  const { StoreSellerTermsDialog } = await import('./seller-terms-dialog')
  let terms = { content: '', version: '', configured: false, accepted: false, required: true, updated_at: 0 }
  const writes: { url: string; body: Record<string, unknown> }[] = []
  let continued = 0
  api.get = (async (url: string) => {
    assert.equal(url, '/api/store/my/terms')
    return result(terms)
  }) as typeof api.get
  api.put = (async (url: string, body: Record<string, unknown>) => {
    writes.push({ url, body })
    terms = { ...terms, content: String(body.content), version: 'terms-v1', configured: true, updated_at: 1 }
    return result(terms)
  }) as typeof api.put
  await mount(<StoreSellerTermsDialog sellerId={9} onClose={() => {}}
    onContinue={() => { continued++ }} />)
  assert.equal(button('Continue publishing').disabled, true)
  assert.equal(document.querySelector('#store-accept-seller-terms'), null)
  await input(document.querySelector<HTMLTextAreaElement>('#store-seller-terms')!, 'Delivery and after-sales policy')
  await click(button('Save'))
  assert.deepEqual(writes, [{ url: '/api/store/my/terms', body: { content: 'Delivery and after-sales policy', expected_version: '' } }])
  assert.equal(continued, 0)
  assert.equal(terms.accepted, false)
  assert.equal(button('Continue publishing').disabled, false)
  await click(button('Continue publishing'))
  assert.equal(continued, 1)
})
'''
write(p, s)
