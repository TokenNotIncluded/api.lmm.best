/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

import type { PlanRecord } from '../../types'

const domWindow = new Window({
  url: 'https://console.example.test/wallet',
  width: 390,
  height: 844,
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { SubscriptionPurchaseDialog } =
  await import('./subscription-purchase-dialog')

const originalPost = api.post
const originalOpen = domWindow.open
const originalFormSubmit = domWindow.HTMLFormElement.prototype.submit
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const plan: PlanRecord = {
  plan: {
    id: 7,
    title: 'Mobile Starter',
    price_amount: 5,
    currency: 'USD',
    duration_unit: 'month',
    duration_value: 1,
    quota_reset_period: 'monthly',
    enabled: true,
    sort_order: 1,
    max_purchase_per_user: 0,
    total_amount: 100,
    stripe_price_id: 'price_mobile_starter',
    allow_balance_pay: true,
    allow_wallet_overflow: true,
  },
}

async function flushEffects() {
  await new Promise((resolve) => setTimeout(resolve, 20))
}

type Rendered = {
  container: HTMLDivElement
  root: ReturnType<typeof createRoot>
}

type DialogOptions = {
  enableStripe?: boolean
  enableWaffoPancake?: boolean
  enableOnlineTopUp?: boolean
  epayMethods?: Array<{ type: string; name?: string }>
  paymentMethods?: string[]
  userQuota?: number
  onCheckoutStarted?: () => void
}

async function renderDialog(options: DialogOptions = {}): Promise<Rendered> {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <SubscriptionPurchaseDialog
          open
          onOpenChange={() => undefined}
          plan={
            options.enableWaffoPancake
              ? {
                  ...plan,
                  plan: {
                    ...plan.plan,
                    waffo_pancake_product_id: 'pancake-mobile-starter',
                  },
                }
              : plan
          }
          enableStripe={options.enableStripe ?? true}
          enableWaffoPancake={options.enableWaffoPancake}
          enableOnlineTopUp={options.enableOnlineTopUp}
          epayMethods={options.epayMethods}
          paymentMethods={options.paymentMethods}
          userQuota={options.userQuota ?? 0}
          onCheckoutStarted={options.onCheckoutStarted}
        />
      </I18nextProvider>
    )
    await flushEffects()
  })
  return { container, root }
}

async function unmount(rendered: Rendered) {
  await act(async () => rendered.root.unmount())
  rendered.container.remove()
}

afterEach(() => {
  api.post = originalPost
  domWindow.open = originalOpen
  domWindow.HTMLFormElement.prototype.submit = originalFormSubmit
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('subscription purchase checkout', () => {
  test('reserves the Stripe checkout before the async request on mobile', async () => {
    const events: string[] = []
    const popup = {
      closed: false,
      name: '',
      opener: {} as Window | null,
      close: () => undefined,
      focus: () => undefined,
      location: { href: '' },
    }
    const openCalls: unknown[][] = []
    domWindow.open = ((...args: unknown[]) => {
      events.push('reserve')
      openCalls.push(args)
      return popup as unknown as Window
    }) as typeof domWindow.open
    api.post = (async (url) => {
      events.push(`request:${url}`)
      return {
        data: {
          success: true,
          message: 'success',
          data: { pay_link: 'https://pay.example.test/stripe' },
        },
      }
    }) as typeof api.post

    const rendered = await renderDialog()
    try {
      const stripeButton = [...document.querySelectorAll('button')].find(
        (button) => button.textContent?.trim() === 'Stripe'
      )
      assert.ok(stripeButton)

      await act(async () => {
        stripeButton.click()
        await flushEffects()
      })

      assert.deepEqual(events, [
        'reserve',
        'request:/api/subscription/stripe/pay',
      ])
      assert.deepEqual(openCalls, [[]])
      assert.match(popup.name, /^payment_checkout_/)
      assert.equal(popup.location.href, 'https://pay.example.test/stripe')
      assert.equal(popup.opener, null)
    } finally {
      await unmount(rendered)
    }
  })

  test('reserves an ePay checkout before the async request on mobile', async () => {
    const events: string[] = []
    const popup = {
      closed: false,
      name: '',
      opener: {} as Window | null,
      close: () => undefined,
      focus: () => undefined,
      location: { href: '' },
    }
    const openCalls: unknown[][] = []
    const formSubmissions: Array<{
      action: string
      target: string
      fields: Record<string, string>
    }> = []
    domWindow.open = ((...args: unknown[]) => {
      events.push('reserve')
      openCalls.push(args)
      return popup as unknown as Window
    }) as typeof domWindow.open
    domWindow.HTMLFormElement.prototype.submit = function () {
      events.push('submit')
      const fields: Record<string, string> = {}
      for (const input of this.querySelectorAll('input')) {
        fields[input.name] = input.value
      }
      formSubmissions.push({
        action: this.action,
        target: this.target,
        fields,
      })
    }
    api.post = (async (url) => {
      events.push(`request:${url}`)
      return {
        data: {
          success: true,
          message: 'success',
          url: 'https://pay.example.test/epay',
          data: { pid: 'subscription-7', sign: 'signed' },
        },
      }
    }) as typeof api.post

    const rendered = await renderDialog({
      enableStripe: false,
      enableOnlineTopUp: true,
      epayMethods: [{ type: 'alipay', name: 'Alipay' }],
    })
    try {
      const payButton = [...document.querySelectorAll('button')].find(
        (button) => button.textContent?.trim() === 'Pay'
      )
      assert.ok(payButton)

      await act(async () => {
        payButton.click()
        await flushEffects()
      })

      assert.deepEqual(events, [
        'reserve',
        'request:/api/subscription/epay/pay',
        'submit',
      ])
      assert.deepEqual(openCalls, [[]])
      assert.match(popup.name, /^payment_checkout_/)
      assert.deepEqual(formSubmissions, [
        {
          action: 'https://pay.example.test/epay',
          target: popup.name,
          fields: { pid: 'subscription-7', sign: 'signed' },
        },
      ])
    } finally {
      await unmount(rendered)
    }
  })

  test('keeps the subscription dialog open for an invalid Pancake checkout URL', async () => {
    let checkoutStarted = 0
    api.post = (async (url) => {
      assert.equal(url, '/api/subscription/waffo-pancake/pay')
      return {
        data: {
          success: true,
          message: 'success',
          data: { checkout_url: 'javascript:alert(1)' },
        },
      }
    }) as typeof api.post

    const rendered = await renderDialog({
      enableStripe: false,
      enableWaffoPancake: true,
      onCheckoutStarted: () => {
        checkoutStarted += 1
      },
    })
    try {
      const pancakeButton = [...document.querySelectorAll('button')].find(
        (button) => button.textContent?.trim() === 'Waffo Pancake'
      )
      assert.ok(pancakeButton)

      await act(async () => {
        pancakeButton.click()
        await flushEffects()
      })

      assert.equal(checkoutStarted, 0)
      assert.ok(
        [...document.querySelectorAll('button')].some(
          (button) => button.textContent?.trim() === 'Waffo Pancake'
        )
      )
    } finally {
      await unmount(rendered)
    }
  })

  test('explains when no external payment method can cover an insufficient balance', async () => {
    const rendered = await renderDialog({
      enableStripe: true,
      paymentMethods: [],
      userQuota: 0,
    })
    try {
      assert.match(
        document.body.textContent ?? '',
        /No payment methods available\. Please contact administrator\./
      )
      const balanceButton = [...document.querySelectorAll('button')].find(
        (button) => button.textContent?.trim() === 'Pay with Balance'
      )
      assert.equal(balanceButton?.disabled, true)
    } finally {
      await unmount(rendered)
    }
  })
})
