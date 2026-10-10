/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'
import type { ComponentProps, ReactElement } from 'react'

import type { BillingSettings, UpdateOptionRequest } from '../types'

const domWindow = new Window({
  url: 'https://console.example.test/system-settings/billing/quota',
})
domWindow.document.write('<!doctype html><html><body></body></html>')
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
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
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'scrollTo',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} = await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { SettingsPageProvider } =
  await import('../components/settings-page-context')
const { QuotaSettingsSection } = await import('./quota-settings-section')
const { getBillingSectionContent } = await import('../billing/section-registry')

type QuotaDefaults = ComponentProps<
  typeof QuotaSettingsSection
>['defaultValues']

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

after(() => domWindow.close())

function quotaDefaults(inviteEnabled: boolean): QuotaDefaults {
  return {
    QuotaForNewUser: 0,
    PreConsumedQuota: 0,
    QuotaForInviter: 0,
    ReferralRegistrationRewardQuota: 0,
    ReferralMinTopUpAmounts: '{"USD":"10","CNY":"70"}',
    ReferralMinTopUpQuota: 0,
    ReferralMaxRewardQuota: 0,
    ReferralPenaltyPercent: 20,
    ReferralMaxPenaltyQuota: 0,
    OpenSourceBountyFeeRate: 0,
    TopUpLink: '',
    general_setting: { docs_link: '' },
    quota_setting: { enable_free_model_pre_consume: true },
    developer_access_setting: {
      invite_registration_enabled: inviteEnabled,
    },
  }
}

async function renderQuota(defaultValues: QuotaDefaults) {
  const container = document.createElement('div')
  const actionsContainer = document.createElement('div')
  container.append(actionsContainer)
  document.body.append(container)
  const root = createRoot(container)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <SettingsPageProvider actionsContainer={actionsContainer}>
            <QuotaSettingsSection defaultValues={defaultValues} />
          </SettingsPageProvider>
        </I18nextProvider>
      </QueryClientProvider>
    ),
  })
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/',
    component: () => null,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await act(async () => {
    root.render(<RouterProvider router={router} />)
    await new Promise((resolve) => setTimeout(resolve, 20))
  })
  return {
    container,
    async cleanup() {
      await act(async () => root.unmount())
      queryClient.clear()
      container.remove()
    },
  }
}

function getSwitch(container: HTMLElement, label: string) {
  const formLabel = Array.from(container.querySelectorAll('label')).find(
    (element) => element.textContent === label
  )
  assert.ok(formLabel)
  const control = document.getElementById(formLabel.htmlFor)
  assert.ok(control && container.contains(control))
  const switchControl = control.matches('[role="switch"]')
    ? control
    : control
        .closest('[data-slot="form-item"]')
        ?.querySelector<HTMLElement>('[role="switch"]')
  assert.ok(switchControl)
  return switchControl
}

test('missing invitation settings default to disabled and preserve explicit saved values', () => {
  for (const enabled of [undefined, false, true]) {
    const settings = {
      'developer_access_setting.invite_registration_enabled': enabled,
    } as unknown as BillingSettings
    const content = getBillingSectionContent(
      'quota',
      settings
    ) as ReactElement<{
      defaultValues: QuotaDefaults
    }>
    assert.equal(
      content.props.defaultValues.developer_access_setting
        .invite_registration_enabled,
      enabled ?? false
    )
  }
})

test('saves invitation access without retaining a second recharge-threshold editor', async () => {
  const originalPut = api.put
  const updates: UpdateOptionRequest[] = []
  api.put = (async (url: string, request: UpdateOptionRequest) => {
    assert.equal(url, '/api/option/')
    updates.push(request)
    return { data: { success: true, message: '' } }
  }) as typeof api.put

  try {
    for (const initiallyEnabled of [false, true]) {
      const rendered = await renderQuota(quotaDefaults(initiallyEnabled))
      try {
        const invitationSwitch = getSwitch(
          rendered.container,
          'Grant L1 when registering through an invitation'
        )
        const threshold = rendered.container.querySelector<HTMLInputElement>(
          'input[name="developer_access_setting.paid_activation_min_amount"]'
        )
        const form = rendered.container.querySelector('form')
        assert.ok(form)
        assert.equal(threshold, null)
        assert.doesNotMatch(
          rendered.container.textContent ?? '',
          /Let a recharge unlock the console/
        )
        assert.equal(
          invitationSwitch.getAttribute('aria-checked'),
          String(initiallyEnabled)
        )
        await act(async () => {
          invitationSwitch.dispatchEvent(
            new MouseEvent('click', { bubbles: true })
          )
        })
        assert.equal(
          invitationSwitch.getAttribute('aria-checked'),
          String(!initiallyEnabled)
        )
        await act(async () => {
          form.dispatchEvent(
            new Event('submit', { bubbles: true, cancelable: true })
          )
          await new Promise((resolve) => setTimeout(resolve, 40))
        })
      } finally {
        await rendered.cleanup()
      }
    }
    assert.deepEqual(updates, [
      {
        key: 'developer_access_setting.invite_registration_enabled',
        value: true,
      },
      {
        key: 'developer_access_setting.invite_registration_enabled',
        value: false,
      },
    ])
  } finally {
    api.put = originalPut
  }
})
