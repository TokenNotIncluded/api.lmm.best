/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

import type { NavGroup, NavLink } from '../types'

const domWindow = new Window({ url: 'https://workspace.example.test/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'KeyboardEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'localStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createMemoryHistory, createRootRoute, createRouter, RouterProvider } =
  await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { SidebarProvider } = await import('@/components/ui/sidebar')
const { ModelPlazaProvider } = await import('@/context/model-plaza-provider')
const { WorkspaceLaunchpad } = await import(
  '@/features/dashboard/components/overview/workspace-launchpad'
)
const { getWorkspaceCopy } = await import('../lib/workspace-copy')
const { SidebarNavigation } = await import('./sidebar-navigation')

const testGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
testGlobals.IS_REACT_ACT_ENVIRONMENT = true
const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})
after(() => domWindow.close())

const navigation: NavGroup[] = [
  {
    id: 'general',
    title: 'General',
    items: [
      { title: 'API Keys', url: '/keys' },
      { title: 'Wallet', url: '/wallet' },
      {
        title: 'Billing',
        items: [{ title: 'Rates', url: '/pricing' }],
      },
    ],
  },
]

function required<T>(value: T | null | undefined): T {
  assert.ok(value)
  return value
}

type ViewState = {
  scope: string
  groups: NavGroup[]
  shortcuts: NavLink[]
}

async function setup(shortcuts: NavLink[] = []) {
  let searchCalls = 0
  let modelTrigger: HTMLElement | null = null
  let update: ((value: ViewState) => void) | undefined
  function Screen() {
    const [view, setView] = useState<ViewState>({
      scope: 'first-account',
      groups: navigation,
      shortcuts,
    })
    update = setView
    return (
      <I18nextProvider i18n={i18n}>
        <ModelPlazaProvider>
          <SidebarProvider defaultOpen>
            <SidebarNavigation key={view.scope} groups={view.groups} />
            <WorkspaceLaunchpad
              name='<img src=x onerror=alert(1)>'
              copy={getWorkspaceCopy('en')}
              shortcuts={view.shortcuts}
              onSearch={() => searchCalls++}
              onModelPanel={(trigger) => {
                modelTrigger = trigger
              }}
            />
          </SidebarProvider>
        </ModelPlazaProvider>
      </I18nextProvider>
    )
  }
  const router = createRouter({
    routeTree: createRootRoute({ component: Screen }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => root.render(<RouterProvider router={router} />))
  const input = () =>
    required(container.querySelector<HTMLInputElement>('input[type="search"]'))
  return {
    container,
    input,
    getSearchCalls: () => searchCalls,
    getModelTrigger: () => modelTrigger,
    async change(value: string) {
      await act(async () => {
        const element = input()
        required(
          Object.getOwnPropertyDescriptor(
            domWindow.HTMLInputElement.prototype,
            'value'
          )?.set
        ).call(element, value)
        element.dispatchEvent(new domWindow.Event('input', { bubbles: true }))
      })
    },
    async update(value: ViewState) {
      await act(async () => required(update)(value))
    },
    async cleanup() {
      await act(async () => root.unmount())
      container.remove()
    },
  }
}

test('nested navigation matches are visible and Escape clears the filter', async () => {
  const ctx = await setup()
  try {
    await ctx.change('rates')
    const nav = required(ctx.container.querySelector('.workspace-navigation'))
    assert.equal(nav.querySelectorAll('a').length, 1)
    assert.match(nav.textContent ?? '', /Billing \/ Rates/)
    assert.equal(nav.querySelector('a')?.getAttribute('href'), '/pricing')
    await act(async () => {
      ctx.input().dispatchEvent(
        new domWindow.KeyboardEvent('keydown', {
          key: 'Escape',
          bubbles: true,
          cancelable: true,
        })
      )
    })
    assert.equal(ctx.input().value, '')
    assert.equal(document.activeElement, ctx.input())
    assert.ok(nav.querySelector('a[href="/keys"]'))
  } finally {
    await ctx.cleanup()
  }
})

test('empty results are announced and can be cleared', async () => {
  const ctx = await setup()
  try {
    await ctx.change('nothing-matches-this')
    const status = required(ctx.container.querySelector('[role="status"]'))
    assert.match(status.textContent ?? '', /No matching destinations/)
    await act(async () => required(status.querySelector('button')).click())
    assert.equal(ctx.input().value, '')
    assert.equal(document.activeElement, ctx.input())
    assert.equal(ctx.container.querySelector('[role="status"]'), null)
  } finally {
    await ctx.cleanup()
  }
})

test('permission changes remove results and a new scope resets search', async () => {
  const ctx = await setup()
  try {
    await ctx.change('keys')
    await ctx.update({ scope: 'first-account', groups: [], shortcuts: [] })
    assert.equal(ctx.container.querySelector('a[href="/keys"]'), null)
    await ctx.update({
      scope: 'second-account',
      groups: navigation,
      shortcuts: [],
    })
    assert.equal(ctx.input().value, '')
  } finally {
    await ctx.cleanup()
  }
})

test('launchpad preserves links, panel triggers and command search', async () => {
  const ctx = await setup([
    { title: 'API Keys', url: '/keys' },
    { title: 'Models', url: '/pricing', interaction: 'model-panel' },
    { title: 'Wallet', url: '/wallet' },
  ])
  try {
    const nav = required(ctx.container.querySelector('.workspace-shortcuts'))
    assert.equal(nav.querySelectorAll('a').length, 2)
    assert.equal(nav.querySelector('a')?.getAttribute('href'), '/keys')
    const model = required(nav.querySelector('button'))
    await act(async () => model.click())
    assert.equal(ctx.getModelTrigger(), model)
    const search = ctx.container.querySelector<HTMLButtonElement>(
      '.workspace-command'
    )
    await act(async () => required(search).click())
    assert.equal(ctx.getSearchCalls(), 1)
    assert.equal(ctx.container.querySelector('img'), null)
    assert.match(ctx.container.querySelector('h2')?.textContent ?? '', /<img/)
  } finally {
    await ctx.cleanup()
  }
})

test('empty permitted shortcuts cannot fabricate a launch destination', async () => {
  const ctx = await setup()
  try {
    const section = required(
      ctx.container.querySelector('.workspace-launchpad')
    )
    assert.equal(section.querySelectorAll('a').length, 0)
    assert.match(section.textContent ?? '', /Only tools available/)
  } finally {
    await ctx.cleanup()
  }
})
