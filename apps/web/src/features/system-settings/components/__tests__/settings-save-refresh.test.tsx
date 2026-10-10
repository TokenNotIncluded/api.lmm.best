/*
Copyright (C) 2026 LIghtJUNction
*/
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock as moduleMock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, test } from 'node:test'

import { AxiosError, type AxiosAdapter, type AxiosResponse } from 'axios'
import { Window } from 'happy-dom'
import type { ReactNode } from 'react'
import type { Root } from 'react-dom/client'

const domWindow = new Window({
  url: 'https://console.example.test/system-settings/content/assistant',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLButtonElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})

type ShellProps = { children?: ReactNode }
const Shell = ({ children }: ShellProps) => <div>{children}</div>
moduleMock.module('@tanstack/react-router', () => ({
  useParams: () => ({ section: 'assistant' }),
}))
moduleMock.module('@/components/layout', () => ({
  SectionPageLayout: Object.assign(Shell, {
    Breadcrumb: Shell,
    Title: Shell,
    Actions: Shell,
    Content: Shell,
  }),
}))
moduleMock.module('@/components/layout/components/page-footer', () => ({
  PageFooterPortal: Shell,
}))
moduleMock.module('../settings-search', () => ({
  SettingsBreadcrumb: () => <span>Settings</span>,
  SettingsSearch: () => null,
}))
moduleMock.module('@/components/page-transition', () => ({ FadeIn: Shell }))

type ToastRecord = { message: string; description?: string }
const errors: ToastRecord[] = []
const warnings: ToastRecord[] = []
const successes: ToastRecord[] = []
moduleMock.module('sonner', () => ({
  toast: {
    error: (message: string, options?: { description?: string }) =>
      errors.push({ message, description: options?.description }),
    warning: (message: string, options?: { description?: string }) =>
      warnings.push({ message, description: options?.description }),
    success: (message: string, options?: { description?: string }) =>
      successes.push({ message, description: options?.description }),
    info: () => {},
  },
}))

const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider, notifyManager } =
  await import('@tanstack/react-query')
const { Controller, useForm } = await import('react-hook-form')
const { I18nextProvider } = await import('react-i18next')
const { default: i18n } = await import('@/i18n/config')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { SettingsPage } = await import('../settings-page')
const { useUpdateOptions } = await import('../../hooks/use-update-option')
notifyManager.setScheduler(queueMicrotask)
await i18n.changeLanguage('en')

type Settings = { AssistantPersona: string }
type Request = {
  method: string
  url: string
  skipBusinessError?: boolean
  skipErrorHandler?: boolean
  values?: Record<string, string>
}
const baseline: Settings = { AssistantPersona: 'Saved persona' }
const unavailable = 'Pricing currency units are unavailable until configured.'
const queryKey = ['system-options'] as const
const requests: Request[] = []
const originalAdapter = api.defaults.adapter
let read: AxiosAdapter
let write: AxiosAdapter
let root: Root
let container: HTMLDivElement
let queryClient: InstanceType<typeof QueryClient>

function response(
  config: Parameters<AxiosAdapter>[0],
  status: number,
  data: unknown
): AxiosResponse {
  return {
    config,
    status,
    data,
    headers: {},
    statusText: status === 200 ? 'OK' : 'Service Unavailable',
  }
}

function loaded(value = baseline.AssistantPersona) {
  return {
    success: true,
    message: '',
    data: [{ key: 'AssistantPersona', value }],
  }
}

function transportFailure(config: Parameters<AxiosAdapter>[0]) {
  return new AxiosError(
    'Request failed with status code 503',
    'ERR_BAD_RESPONSE',
    config,
    undefined,
    response(config, 503, { success: false, message: unavailable })
  )
}

function DraftSection({ settings }: { settings: Settings }) {
  const form = useForm<Settings>({ defaultValues: settings })
  const mutation = useUpdateOptions()
  const { isDirty } = form.formState
  const [outcome, setOutcome] = useState('idle')
  const [saved, setSaved] = useState(settings.AssistantPersona)
  const submit = form.handleSubmit(async (values) => {
    if (!isDirty) {
      setOutcome('unchanged')
      return
    }
    try {
      const receipt = await mutation.mutateAsync(values)
      if (receipt.success) {
        form.reset(values)
        setSaved(values.AssistantPersona)
        setOutcome('saved')
      }
    } catch {
      setOutcome('rejected')
    }
  })
  return (
    <form data-testid='draft-form' onSubmit={submit}>
      <Controller
        control={form.control}
        name='AssistantPersona'
        render={({ field }) => (
          <input aria-label='Assistant persona' {...field} />
        )}
      />
      <button type='submit'>Save</button>
      <output data-testid='dirty'>{isDirty ? 'dirty' : 'clean'}</output>
      <output data-testid='outcome'>{outcome}</output>
      <output data-testid='saved'>{saved}</output>
    </form>
  )
}

async function renderPage(cached = true) {
  if (cached) queryClient.setQueryData(queryKey, loaded())
  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <SettingsPage
            routePath='/system-settings/content/$section'
            defaultSettings={baseline}
            defaultSection='assistant'
            getSectionMeta={() => ({ titleKey: 'AI assistant' })}
            getSectionContent={(_section, settings) => (
              <DraftSection settings={settings} />
            )}
          />
        </I18nextProvider>
      </QueryClientProvider>
    )
  })
}

async function until(check: () => boolean, label: string) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    if (check()) return
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 5))
    })
  }
  assert.ok(check(), label)
}

async function edit(value: string) {
  const input = container.querySelector<HTMLInputElement>('input')
  assert.ok(input)
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(
      domWindow.HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(setter)
    setter.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await until(
    () =>
      container.querySelector('[data-testid="dirty"]')?.textContent === 'dirty',
    'editing a controlled input must mark the real form dirty'
  )
}

async function save() {
  const button = container.querySelector<HTMLButtonElement>(
    'button[type="submit"]'
  )
  assert.ok(button)
  await act(async () => {
    button.click()
  })
}

function postRequests() {
  return requests.filter((request) => request.method === 'post')
}

beforeEach(() => {
  errors.length = 0
  warnings.length = 0
  successes.length = 0
  requests.length = 0
  useAuthStore.getState().auth.reset('idle')
  read = async (config) => response(config, 200, loaded())
  write = async (config) =>
    response(config, 200, { success: true, message: '' })
  api.defaults.adapter = async (config) => {
    const method = config.method ?? 'get'
    const body = config.data ? JSON.parse(String(config.data)) : undefined
    requests.push({
      method,
      url: config.url ?? '',
      skipBusinessError: config.skipBusinessError,
      skipErrorHandler: config.skipErrorHandler,
      values: body?.values,
    })
    assert.ok(
      ['/api/option/', '/api/option/bulk'].includes(config.url ?? ''),
      'the fixture must never perform an unexpected network request'
    )
    return method === 'get' ? read(config) : write(config)
  }
  queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Infinity },
      mutations: { retry: false },
    },
  })
  container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
})

afterEach(async () => {
  await act(async () => root.unmount())
  queryClient.clear()
  container.remove()
  api.defaults.adapter = originalAdapter
})
after(() => domWindow.close())

test('a failed background refresh retains the same form node and its editable draft', async () => {
  await renderPage()
  await edit('Unsaved draft')
  const input = container.querySelector('input')
  const form = container.querySelector('form')
  read = async (config) => {
    throw transportFailure(config)
  }
  await act(async () => {
    await queryClient.invalidateQueries({ queryKey })
  })
  await until(
    () => queryClient.getQueryState(queryKey)?.status === 'error',
    'refresh must settle'
  )
  assert.equal(container.querySelector('form'), form)
  assert.equal(container.querySelector('input'), input)
  assert.equal(input?.value, 'Unsaved draft')
  assert.equal(
    container.querySelector('[data-testid="dirty"]')?.textContent,
    'dirty'
  )
  assert.match(container.textContent ?? '', /Unable to refresh settings/)
  assert.match(
    container.textContent ?? '',
    /Pricing currency units are unavailable/
  )
  assert.doesNotMatch(
    container.textContent ?? '',
    /Your settings have not been changed/
  )
  assert.equal(
    errors.length,
    0,
    'a page-owned refresh error must not also toast'
  )
  assert.deepEqual(
    requests.map(({ skipBusinessError, skipErrorHandler }) => ({
      skipBusinessError,
      skipErrorHandler,
    })),
    [{ skipBusinessError: true, skipErrorHandler: true }]
  )
})

test('the first failed read shows a full error state with the server reason', async () => {
  read = async (config) => {
    throw transportFailure(config)
  }
  await renderPage(false)
  await until(
    () => queryClient.getQueryState(queryKey)?.status === 'error',
    'initial read must settle'
  )
  assert.equal(container.querySelector('form'), null)
  assert.match(container.textContent ?? '', /Unable to load settings/)
  assert.match(
    container.textContent ?? '',
    /Pricing currency units are unavailable/
  )
  assert.doesNotMatch(
    container.textContent ?? '',
    /Request failed with status code 503/
  )
  assert.equal(errors.length, 0)
})

test('a successful save is acknowledged before its failing refresh and does not write twice', async () => {
  let releaseRefresh!: () => void
  const refreshGate = new Promise<void>((resolve) => {
    releaseRefresh = resolve
  })
  read = async (config) => {
    await refreshGate
    throw transportFailure(config)
  }
  await renderPage()
  await edit('Accepted persona')
  const form = container.querySelector('form')
  await save()
  await until(
    () =>
      container.querySelector('[data-testid="outcome"]')?.textContent ===
      'saved',
    'mutateAsync must resolve while the independent refresh remains pending'
  )
  assert.equal(
    container.querySelector('[data-testid="dirty"]')?.textContent,
    'clean'
  )
  assert.equal(
    container.querySelector('[data-testid="saved"]')?.textContent,
    'Accepted persona'
  )
  assert.equal(queryClient.getQueryState(queryKey)?.fetchStatus, 'fetching')
  assert.equal(postRequests().length, 1)
  assert.deepEqual(postRequests()[0]?.values, {
    AssistantPersona: 'Accepted persona',
  })
  await act(async () => releaseRefresh())
  await until(
    () => queryClient.getQueryState(queryKey)?.status === 'error',
    'post-save refresh must settle'
  )
  await until(
    () => warnings.length > 0,
    'refresh failure must be reported independently'
  )
  assert.equal(container.querySelector('form'), form)
  assert.equal(
    container.querySelector('[data-testid="outcome"]')?.textContent,
    'saved'
  )
  assert.equal(
    container.querySelector('[data-testid="dirty"]')?.textContent,
    'clean'
  )
  assert.equal(errors.length, 0)
  assert.equal(successes.length, 1)
  assert.match(
    JSON.stringify(warnings),
    /Pricing currency units are unavailable/
  )
  await save()
  await until(
    () =>
      container.querySelector('[data-testid="outcome"]')?.textContent ===
      'unchanged',
    'an unchanged form must not resubmit a successful write'
  )
  assert.equal(postRequests().length, 1)
  assert.ok(
    requests
      .filter(({ method }) => method === 'get')
      .every(
        ({ skipBusinessError, skipErrorHandler }) =>
          skipBusinessError && skipErrorHandler
      )
  )
})

for (const failure of ['HTTP 503', 'business failure'] as const) {
  test(`${failure} rejects the write, retains the dirty draft, and reports the body reason once`, async () => {
    write = async (config) => {
      if (failure === 'HTTP 503') throw transportFailure(config)
      return response(config, 200, { success: false, message: unavailable })
    }
    await renderPage()
    await edit('Keep this draft')
    const input = container.querySelector('input')
    await save()
    await until(
      () =>
        container.querySelector('[data-testid="outcome"]')?.textContent ===
        'rejected',
      'transport and business failures must both reject mutateAsync'
    )
    assert.equal(container.querySelector('input'), input)
    assert.equal(input?.value, 'Keep this draft')
    assert.equal(
      container.querySelector('[data-testid="dirty"]')?.textContent,
      'dirty'
    )
    assert.equal(
      container.querySelector('[data-testid="saved"]')?.textContent,
      baseline.AssistantPersona
    )
    assert.equal(postRequests().length, 1)
    assert.equal(
      errors.length,
      1,
      'one write failure must produce one error toast'
    )
    assert.equal(errors[0]?.message, unavailable)
    assert.equal(successes.length, 0)
    assert.equal(warnings.length, 0)
    assert.equal(requests.filter(({ method }) => method === 'get').length, 0)
  })
}
