/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({
  url: 'https://console.example.test/company',
})
domWindow.document.write(
  '<!doctype html><html><head></head><body></body></html>'
)
Object.defineProperty(domWindow.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'HTMLFormElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'PointerEvent',
  'FocusEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.defineProperties(globalThis, {
  requestAnimationFrame: {
    configurable: true,
    value: (callback: FrameRequestCallback) => setTimeout(() => callback(0), 0),
  },
  cancelAnimationFrame: {
    configurable: true,
    value: (handle: number) => clearTimeout(handle),
  },
  getComputedStyle: {
    configurable: true,
    value: domWindow.getComputedStyle.bind(domWindow),
  },
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { CompanyBillingProfileCard } =
  await import('./company-billing-profile-card')
const { companyBillingProfileQueryKey } =
  await import('./use-company-billing-profile')

const originalGet = api.get
const originalPut = api.put
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const mounted: Array<{
  root: ReturnType<typeof createRoot>
  queryClient: InstanceType<typeof QueryClient>
}> = []

function profile(overrides: Record<string, unknown> = {}) {
  return {
    country: 'US',
    isBusiness: true,
    postcode: '10001',
    state: 'NY',
    businessName: 'Example Company',
    taxId: 'TEST-TAX-ID',
    useForInvoices: false,
    createdAt: 1_767_225_600,
    updatedAt: 1_767_312_000,
    ...overrides,
  }
}

async function flushEffects() {
  await new Promise((resolve) => setTimeout(resolve, 20))
}

async function waitForCondition(
  condition: () => boolean,
  failureMessage: string
) {
  const deadline = Date.now() + 2000
  while (!condition()) {
    if (Date.now() >= deadline) {
      throw new Error(`${failureMessage}: ${document.body.textContent}`)
    }
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 5))
    })
  }
}

function setAuthenticatedUser(id: number) {
  useAuthStore.getState().auth.setUser({
    id,
    username: `company-user-${id}`,
    role: 1,
  })
}

async function renderCard() {
  if (!useAuthStore.getState().auth.user) setAuthenticatedUser(7)
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  mounted.push({ root, queryClient })

  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <CompanyBillingProfileCard />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flushEffects()
  })
  return container
}

function input(field: string) {
  const element = document.querySelector<HTMLInputElement>(
    `#company-billing-${field}`
  )
  assert.ok(element, `Missing input ${field}`)
  return element
}

async function setInput(field: string, value: string) {
  const element = input(field)
  const setValue = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setValue)
  await act(async () => {
    setValue.call(element, value)
    element.dispatchEvent(new Event('input', { bubbles: true }))
    element.dispatchEvent(new Event('change', { bubbles: true }))
    await flushEffects()
  })
}

function switchControl(field: string) {
  const labelId = `company-billing-${field}-label`
  const element = document.querySelector<HTMLElement>(
    `[role="switch"][aria-labelledby="${labelId}"]`
  )
  assert.ok(element, `Missing visible switch ${field}`)
  return element
}

async function clickSwitch(field: string) {
  await act(async () => {
    switchControl(field).click()
    await flushEffects()
  })
}

async function submitForm() {
  const form = document.querySelector('form')
  assert.ok(form)
  await act(async () => {
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await flushEffects()
  })
}

function button(text: string) {
  const element = [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
  ].find((candidate) => candidate.textContent?.trim() === text)
  assert.ok(element, `Missing button ${text}`)
  return element
}

afterEach(async () => {
  for (const entry of mounted.splice(0)) {
    await act(async () => entry.root.unmount())
    entry.queryClient.clear()
  }
  api.get = originalGet
  api.put = originalPut
  useAuthStore.getState().auth.reset('complete')
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('company billing profile form', () => {
  test('renders loading and a null response as an empty, opt-in form', async () => {
    let resolveGet: ((value: unknown) => void) | undefined
    api.get = (() =>
      new Promise((resolve) => {
        resolveGet = resolve as (value: unknown) => void
      })) as typeof api.get

    const container = await renderCard()
    assert.ok(container.querySelector('[aria-busy="true"]'))

    await act(async () => {
      resolveGet?.({ data: { success: true, message: '', data: null } })
      await flushEffects()
    })
    await waitForCondition(
      () => document.querySelector('#company-billing-country') !== null,
      'empty company form did not render'
    )

    assert.equal(input('country').value, '')
    assert.equal(
      switchControl('useForInvoices').getAttribute('aria-checked'),
      'false'
    )
    assert.match(document.body.textContent ?? '', /No company billing profile/)
  })

  test('shows a safe load error and retries the GET request', async () => {
    let calls = 0
    api.get = (async () => {
      calls += 1
      if (calls === 1) throw new Error('private upstream detail')
      return { data: { success: true, message: '', data: null } }
    }) as typeof api.get

    await renderCard()
    await waitForCondition(
      () => document.body.textContent?.includes('Unable to load') === true,
      'load error did not render'
    )
    assert.equal(
      document.body.textContent?.includes('private upstream detail'),
      false
    )
    assert.equal(
      document
        .querySelector('[role="alert"]')
        ?.textContent?.includes('Unable to load'),
      true
    )

    await act(async () => {
      button('Retry').click()
      await flushEffects()
    })
    await waitForCondition(
      () => document.querySelector('#company-billing-country') !== null,
      'retry did not render the form'
    )
    assert.equal(calls, 2)
  })

  test('normalizes country, shows saving and adopts the server response', async () => {
    api.get = (async () => ({
      data: { success: true, message: '', data: null },
    })) as typeof api.get
    let putCall: { body: Record<string, unknown> } | undefined
    let resolvePut: ((value: unknown) => void) | undefined
    api.put = ((_: string, body: Record<string, unknown>) => {
      putCall = { body }
      return new Promise((resolve) => {
        resolvePut = resolve as (value: unknown) => void
      })
    }) as typeof api.put

    await renderCard()
    await waitForCondition(
      () => document.querySelector('#company-billing-country') !== null,
      'company form did not render'
    )
    await setInput('country', ' us ')
    await setInput('businessName', '  Example Company  ')
    await submitForm()

    assert.equal(input('country').value, 'US')
    assert.equal(putCall?.body.country, 'US')
    assert.equal(putCall?.body.businessName, 'Example Company')
    assert.equal('requiredFields' in (putCall?.body ?? {}), false)
    assert.match(document.body.textContent ?? '', /Saving/)

    await act(async () => {
      resolvePut?.({
        data: {
          success: true,
          message: '',
          data: profile({ businessName: 'Canonical Example Company' }),
        },
      })
      await flushEffects()
    })
    await waitForCondition(
      () => document.body.textContent?.includes('Saved') === true,
      'saved state did not render'
    )
    assert.equal(input('businessName').value, 'Canonical Example Company')
  })

  test('maps 422 fields without rendering server details or sensitive values', async () => {
    api.get = (async () => ({
      data: { success: true, message: '', data: null },
    })) as typeof api.get
    api.put = (async () => {
      throw {
        isAxiosError: true,
        response: {
          status: 422,
          data: {
            fieldErrors: {
              taxId: 'invalid TEST-TAX-VALUE',
            },
          },
        },
      }
    }) as typeof api.put

    await renderCard()
    await waitForCondition(
      () => document.querySelector('#company-billing-country') !== null,
      'company form did not render'
    )
    await setInput('country', 'US')
    await setInput('taxId', 'TEST-TAX-VALUE')
    await submitForm()
    await waitForCondition(
      () =>
        document.querySelector('#company-billing-taxId-error')?.textContent
          ?.length !== 0,
      'tax field error did not render'
    )

    assert.equal(
      document.querySelector('#company-billing-taxId-error')?.textContent,
      'Tax ID must be 64 characters or fewer.'
    )
    assert.equal(document.activeElement?.id, 'company-billing-taxId')
    assert.equal(
      document.body.textContent?.includes('invalid TEST-TAX-VALUE'),
      false
    )
    assert.equal(document.body.textContent?.includes('TEST-TAX-VALUE'), false)
  })

  test('saves the profile when automatic invoice use is switched off', async () => {
    api.get = (async () => ({
      data: {
        success: true,
        message: '',
        data: profile({ useForInvoices: true }),
      },
    })) as typeof api.get
    let submitted: Record<string, unknown> | undefined
    api.put = (async (_: string, body: Record<string, unknown>) => {
      submitted = body
      return {
        data: {
          success: true,
          message: '',
          data: profile({ useForInvoices: false }),
        },
      }
    }) as typeof api.put

    await renderCard()
    await waitForCondition(
      () =>
        document
          .querySelector(
            '[role="switch"][aria-labelledby="company-billing-useForInvoices-label"]'
          )
          ?.getAttribute('aria-checked') === 'true',
      'saved toggle state did not load'
    )
    await clickSwitch('useForInvoices')
    await submitForm()
    await waitForCondition(() => submitted !== undefined, 'PUT was not sent')

    assert.equal(submitted?.useForInvoices, false)
  })

  test('renders a generic save error without exposing the thrown message', async () => {
    api.get = (async () => ({
      data: { success: true, message: '', data: null },
    })) as typeof api.get
    api.put = (async () => {
      throw new Error('private payment processor detail')
    }) as typeof api.put

    await renderCard()
    await waitForCondition(
      () => document.querySelector('#company-billing-country') !== null,
      'company form did not render'
    )
    await setInput('country', 'US')
    await submitForm()
    await waitForCondition(
      () => document.body.textContent?.includes('Unable to save') === true,
      'generic save error did not render'
    )

    assert.equal(
      document.body.textContent?.includes('private payment processor detail'),
      false
    )
    assert.equal(
      document
        .querySelector('[role="alert"]')
        ?.textContent?.includes('Unable to save'),
      true
    )
  })

  test('isolates cached PII and form state across authenticated user changes', async () => {
    setAuthenticatedUser(41)
    let calls = 0
    let resolveSecond: ((value: unknown) => void) | undefined
    api.get = (() => {
      calls += 1
      if (calls === 1) {
        return Promise.resolve({
          data: {
            success: true,
            message: '',
            data: profile({ taxId: 'FIRST-USER-TAX' }),
          },
        })
      }
      return new Promise((resolve) => {
        resolveSecond = resolve as (value: unknown) => void
      })
    }) as typeof api.get

    await renderCard()
    await waitForCondition(
      () =>
        document.querySelector<HTMLInputElement>('#company-billing-taxId')
          ?.value === 'FIRST-USER-TAX',
      'first user profile did not load'
    )
    const queryClient = mounted.at(-1)?.queryClient
    assert.ok(queryClient)
    assert.equal(
      queryClient.getQueryData<{ taxId: string }>(
        companyBillingProfileQueryKey(41)
      )?.taxId,
      'FIRST-USER-TAX'
    )

    await act(async () => {
      setAuthenticatedUser(42)
      await flushEffects()
    })
    assert.equal(document.body.textContent?.includes('FIRST-USER-TAX'), false)
    assert.equal(document.querySelector('#company-billing-taxId'), null)

    await act(async () => {
      resolveSecond?.({
        data: {
          success: true,
          message: '',
          data: profile({ taxId: 'SECOND-USER-TAX' }),
        },
      })
      await flushEffects()
    })
    await waitForCondition(
      () =>
        document.querySelector<HTMLInputElement>('#company-billing-taxId')
          ?.value === 'SECOND-USER-TAX',
      'second user profile did not load'
    )
    assert.equal(
      queryClient.getQueryData<{ taxId: string }>(
        companyBillingProfileQueryKey(42)
      )?.taxId,
      'SECOND-USER-TAX'
    )
  })

  test('connects every label and description and focuses the first invalid field', async () => {
    api.get = (async () => ({
      data: { success: true, message: '', data: null },
    })) as typeof api.get

    await renderCard()
    await waitForCondition(
      () => document.querySelector('#company-billing-country') !== null,
      'company form did not render'
    )

    for (const field of [
      'country',
      'isBusiness',
      'postcode',
      'state',
      'businessName',
      'taxId',
      'useForInvoices',
    ]) {
      const isSwitch = field === 'isBusiness' || field === 'useForInvoices'
      const control = isSwitch
        ? document.querySelector(
            `[role="switch"][aria-labelledby="company-billing-${field}-label"]`
          )
        : document.querySelector(`#company-billing-${field}`)
      const label = isSwitch
        ? document.querySelector(`#company-billing-${field}-label`)
        : document.querySelector(`label[for="company-billing-${field}"]`)
      const description = document.querySelector(
        `#company-billing-${field}-description`
      )
      assert.ok(control, field)
      assert.ok(label, field)
      assert.ok(description, field)
      assert.match(
        control.getAttribute('aria-describedby') ?? '',
        new RegExp(`company-billing-${field}-description`)
      )
    }

    await submitForm()
    assert.equal(document.activeElement?.id, 'company-billing-country')
    assert.equal(input('country').getAttribute('aria-invalid'), 'true')
    assert.match(
      input('country').getAttribute('aria-describedby') ?? '',
      /company-billing-country-error/
    )
  })
})
