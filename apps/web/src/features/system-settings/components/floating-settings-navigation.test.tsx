/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const view = new Window({ url: 'https://settings.example.test/' })
view.document.write('<!doctype html><html><head></head><body></body></html>')
const globals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
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
] as const
const previousGlobals = new Map(
  globals.map((key) => [key, Object.getOwnPropertyDescriptor(globalThis, key)])
)
for (const key of globals) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: view[key],
  })
}
const previousAct = Object.getOwnPropertyDescriptor(
  globalThis,
  'IS_REACT_ACT_ENVIRONMENT'
)
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const { act, StrictMode } = await import('react')
const { createRoot } = await import('react-dom/client')
const {
  createRootRoute,
  createRoute,
  createRouter,
  createMemoryHistory,
  RouterProvider,
  useLocation,
  useNavigate,
} = await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { SidebarProvider, SidebarTrigger, useSidebar } =
  await import('@/components/ui/sidebar')
const { FloatingSettingsNavigation } =
  await import('./floating-settings-navigation')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

after(() => {
  view.close()
  for (const key of globals) {
    const previous = previousGlobals.get(key)
    if (previous) Object.defineProperty(globalThis, key, previous)
    else Reflect.deleteProperty(globalThis, key)
  }
  if (previousAct) {
    Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', previousAct)
  } else {
    Reflect.deleteProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT')
  }
})

const settingsPath = '/system-settings/site/system-info'
const dashboardPath = '/dashboard/overview'
const overlaySelector = '[data-slot="sheet-overlay"]'
const delay = () => new Promise<void>((resolve) => setTimeout(resolve, 30))

for (const scenario of [
  { name: 'expanded desktop sidebar', width: 1280, open: true },
  { name: 'collapsed desktop sidebar', width: 1280, open: false },
  { name: 'open mobile menu', width: 390, open: true, mobileOpen: true },
  { name: 'direct desktop load', width: 1280, open: true, direct: true },
  { name: 'direct mobile load', width: 390, open: true, direct: true },
]) {
  test(`settings starts closed and stays interactive after ${scenario.name}`, async () => {
    Object.defineProperty(window, 'innerWidth', {
      configurable: true,
      value: scenario.width,
    })
    window.matchMedia = ((query: string) => ({
      matches: scenario.width < 768,
      media: query,
      onchange: null,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      addListener: () => undefined,
      removeListener: () => undefined,
      dispatchEvent: () => false,
    })) as typeof window.matchMedia

    function Navigation() {
      const { pathname } = useLocation()
      const navigate = useNavigate()
      const { open, openMobile, setOpenMobile } = useSidebar()
      const settings = pathname.startsWith('/system-settings/')
      return (
        <>
          {settings ? (
            <FloatingSettingsNavigation />
          ) : (
            <button
              type='button'
              data-testid='enter-settings'
              onClick={() => {
                if (scenario.mobileOpen) setOpenMobile(true)
                void navigate({
                  to: '/system-settings/site/$section',
                  params: { section: 'system-info' },
                })
              }}
            >
              Enter settings
            </button>
          )}
          <SidebarTrigger />
          <input aria-label='Test field' />
          <button
            type='button'
            data-testid='leave-settings'
            onClick={() =>
              void navigate({
                to: '/dashboard/$section',
                params: { section: 'overview' },
              })
            }
          >
            Leave settings
          </button>
          <output
            data-testid='sidebar-state'
            data-open={open}
            data-mobile-open={openMobile}
          />
        </>
      )
    }

    function Layout() {
      const settings = useLocation().pathname.startsWith('/system-settings/')
      return (
        // As in AuthenticatedLayout, defaultOpen changes without remounting.
        <SidebarProvider
          defaultOpen={settings ? false : scenario.open}
          persistState={false}
        >
          <Navigation />
        </SidebarProvider>
      )
    }

    const route = createRootRoute({ component: Layout })
    const router = createRouter({
      routeTree: route.addChildren([
        createRoute({ getParentRoute: () => route, path: '$' }),
      ]),
      history: createMemoryHistory({
        initialEntries: [scenario.direct ? settingsPath : dashboardPath],
      }),
    })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    const createdOverlays = new Set<Element>()
    let requestedOpen = false
    const collectAddedNode = (node: unknown) => {
      if (!(node instanceof Element) || !node.isConnected) return
      const expandedHandle = '.settings-scrub-handle[aria-expanded="true"]'
      if (node.matches(expandedHandle) || node.querySelector(expandedHandle)) {
        requestedOpen = true
      }
      if (node.matches(overlaySelector)) createdOverlays.add(node)
      for (const overlay of node.querySelectorAll(overlaySelector)) {
        createdOverlays.add(overlay)
      }
    }
    const collectChanges = (records: MutationRecord[]) => {
      for (const record of records) {
        if (
          record.type === 'attributes' &&
          record.target instanceof Element &&
          record.target.matches('.settings-scrub-handle') &&
          (record.oldValue === 'true' ||
            record.target.getAttribute('aria-expanded') === 'true')
        ) {
          requestedOpen = true
        }
        for (const node of record.addedNodes) {
          collectAddedNode(node)
        }
      }
    }
    // Happy DOM 20.11.1 keeps its internal mutation callback weakly. Observe
    // real DOM writes synchronously too, so collection cannot disappear at GC.
    const originalSetAttribute = view.Element.prototype.setAttribute
    const originalAppendChild = view.Node.prototype.appendChild
    const originalInsertBefore = view.Node.prototype.insertBefore
    view.Element.prototype.setAttribute = function (name, value) {
      const previous = this.getAttribute(name)
      originalSetAttribute.call(this, name, value)
      if (
        this.isConnected &&
        name === 'aria-expanded' &&
        this.matches('.settings-scrub-handle') &&
        (previous === 'true' || value === 'true')
      ) {
        requestedOpen = true
      }
    }
    view.Node.prototype.appendChild = function (node) {
      const added = node.nodeType === 11 ? [...node.childNodes] : [node]
      const inserted = originalAppendChild.call(this, node)
      added.forEach(collectAddedNode)
      return inserted
    }
    view.Node.prototype.insertBefore = function (node, reference) {
      const added = node.nodeType === 11 ? [...node.childNodes] : [node]
      const inserted = originalInsertBefore.call(this, node, reference)
      added.forEach(collectAddedNode)
      return inserted
    }
    // Track open requests as well as portals: an immediately closed modal
    // may disappear before assertions or before its portal has mounted.
    const observer = new MutationObserver(collectChanges)
    observer.observe(document.body, {
      childList: true,
      subtree: true,
      attributes: true,
      attributeFilter: ['aria-expanded'],
      attributeOldValue: true,
    })
    const click = async (selector: string) => {
      const button = document.querySelector<HTMLButtonElement>(selector)
      assert.ok(button, `Missing button: ${selector}`)
      await act(async () => {
        button.click()
        await delay()
      })
      await act(delay)
      collectChanges(observer.takeRecords())
    }

    try {
      await act(async () => {
        root.render(
          <StrictMode>
            <I18nextProvider i18n={i18n}>
              <RouterProvider router={router} />
            </I18nextProvider>
          </StrictMode>
        )
        await delay()
      })
      await act(delay)
      if (!scenario.direct) await click('[data-testid="enter-settings"]')
      collectChanges(observer.takeRecords())
      assert.equal(requestedOpen, false, 'entry must not request a modal')
      assert.equal(createdOverlays.size, 0, 'entry must not open a modal')
      assert.equal(document.querySelector(overlaySelector), null)

      await click('.settings-scrub-handle')
      assert.ok(document.querySelector('[data-slot="sheet-content"]'))
      assert.equal(
        requestedOpen,
        true,
        'the observer must detect open requests'
      )
      assert.ok(createdOverlays.size > 0, 'the observer must detect real opens')
      await click('[data-slot="sheet-close"]')
      assert.equal(document.querySelector(overlaySelector), null)
      const input = document.querySelector<HTMLInputElement>(
        'input[aria-label="Test field"]'
      )
      assert.ok(input)
      input.focus()
      assert.equal(document.activeElement, input)
      assert.equal(input.closest('[inert]'), null)

      await click('[data-slot="sidebar-trigger"]')
      assert.ok(document.querySelector('[data-slot="sheet-content"]'))
      await click(
        '.settings-directory-list details[open] button:nth-of-type(2)'
      )
      assert.notEqual(router.state.location.pathname, settingsPath)
      assert.equal(document.querySelector(overlaySelector), null)
      await click('[data-testid="leave-settings"]')
      const state = document.querySelector('[data-testid="sidebar-state"]')
      assert.equal(
        state?.getAttribute('data-open'),
        String(scenario.direct ? false : scenario.open)
      )
      assert.equal(state?.getAttribute('data-mobile-open'), 'false')

      createdOverlays.clear()
      requestedOpen = false
      await click('[data-testid="enter-settings"]')
      assert.equal(requestedOpen, false, 're-entry must not request a modal')
      assert.equal(createdOverlays.size, 0, 're-entry must also start closed')
      await click('.settings-scrub-handle')
      assert.ok(document.querySelector('[data-slot="sheet-content"]'))
      await click('[data-slot="sheet-close"]')
    } finally {
      observer.disconnect()
      try {
        await act(async () => root.unmount())
      } finally {
        view.Element.prototype.setAttribute = originalSetAttribute
        view.Node.prototype.appendChild = originalAppendChild
        view.Node.prototype.insertBefore = originalInsertBefore
        container.remove()
      }
    }
  })
}
