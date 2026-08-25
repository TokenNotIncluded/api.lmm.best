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
import { after, afterEach } from 'node:test'

import { Window } from 'happy-dom'

import type { AssistantCreateKeyAction } from './api'

const domWindow = new Window({ url: 'https://console.example.test/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'history',
  'location',
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

const react = await import('react')
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
const apiModule = await import('@/lib/api')
const { AssistantKeyTool } = await import('./assistant-key-tool')

export const act = react.act
export const api = apiModule.api
const originalPost = api.post
const originalGet = api.get
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

export type RenderedTool = {
  container: HTMLDivElement
  queryClient: InstanceType<typeof QueryClient>
  root: ReturnType<typeof createRoot>
}

export async function flushEffects() {
  await new Promise((resolve) => setTimeout(resolve, 20))
}

export async function waitFor(
  predicate: () => boolean,
  message: string,
  attempts = 30
) {
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    if (predicate()) return
    await act(async () => flushEffects())
  }
  assert.fail(message)
}

export async function renderTool(
  developerAccessGranted: boolean,
  onContinueSetup = () => {},
  confirmationAction?: AssistantCreateKeyAction,
  autoConfirm = false,
  onKeyPreparationInvalid = () => {}
): Promise<RenderedTool> {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <AssistantKeyTool
            baseUrl='https://api.example.test/v1'
            availableModels={['claude-sonnet-4-5']}
            modelsLoading={false}
            developerAccessGranted={developerAccessGranted}
            confirmationAction={confirmationAction}
            autoConfirm={autoConfirm}
            onKeyPreparationInvalid={onKeyPreparationInvalid}
            onContinueSetup={onContinueSetup}
          />
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
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  await act(async () => {
    root.render(<RouterProvider router={router} />)
    await flushEffects()
  })
  return { container, queryClient, root }
}

export function findButton(text: string): HTMLButtonElement {
  const button = [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
  ].find((candidate) => candidate.textContent?.includes(text))
  assert.ok(button, `Could not find button containing ${text}`)
  return button
}

export async function unmount(rendered: RenderedTool) {
  await act(async () => rendered.root.unmount())
  rendered.queryClient.clear()
  rendered.container.remove()
}

export function groupsPayload(
  groups: Record<
    string,
    {
      ratio: number
      warning?: {
        enabled: boolean
        message: string
        mode: 'modal' | 'banner' | 'inline'
        confirmations: number
      }
    }
  >
) {
  return {
    data: {
      success: true,
      data: Object.fromEntries(
        Object.entries(groups).map(([id, details]) => [
          id,
          { desc: `${id} group`, ...details },
        ])
      ),
    },
  }
}

afterEach(() => {
  api.post = originalPost
  api.get = originalGet
  document.body.replaceChildren()
})

after(() => domWindow.close())
