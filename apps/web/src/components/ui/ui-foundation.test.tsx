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
import { readFileSync } from 'node:fs'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'
import type { ReactNode } from 'react'

const dom = new Window({ url: 'http://localhost/' })
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
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.defineProperty(globalThis, 'getComputedStyle', {
  configurable: true,
  value: dom.getComputedStyle.bind(dom),
})
;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true
const { act, createRef, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider } = await import('react-i18next')
const { Button } = await import('./button')
const { Input } = await import('./input')
const { Textarea } = await import('./textarea')
const { InputOTP, InputOTPGroup, InputOTPSlot } = await import('./input-otp')
const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  keySeparator: false,
})
after(() => dom.happyDOM.abort())

async function mount(children: ReactNode) {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  await act(async () =>
    root.render(<I18nextProvider i18n={i18n}>{children}</I18nextProvider>)
  )
  return {
    host,
    cleanup: async () => {
      await act(async () => root.unmount())
      host.remove()
    },
  }
}

function OTPExample({
  disabled = false,
  readOnly = false,
  onChange,
  onComplete,
}: {
  disabled?: boolean
  readOnly?: boolean
  onChange?: (value: string) => void
  onComplete?: () => void
}) {
  const [value, setValue] = useState('')
  return (
    <form>
      <label htmlFor='test-otp'>Verification code</label>
      <InputOTP
        id='test-otp'
        name='otp'
        length={6}
        value={value}
        disabled={disabled}
        readOnly={readOnly}
        aria-invalid='true'
        aria-describedby='otp-help'
        autoSubmit={false}
        onValueChange={(next) => {
          setValue(next)
          onChange?.(next)
        }}
        onValueComplete={onComplete}
      >
        <InputOTPGroup>
          {Array.from({ length: 6 }, (_, index) => (
            <InputOTPSlot key={index} index={index} />
          ))}
        </InputOTPGroup>
      </InputOTP>
      <p id='otp-help'>Six digits</p>
    </form>
  )
}

async function paste(input: HTMLInputElement, value: string) {
  const event = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'clipboardData', {
    value: { getData: () => value },
  })
  await act(async () => input.dispatchEvent(event))
}

test('Luma buttons retain disabled behavior, native refs and anchor rendering', async () => {
  let clicks = 0
  const ref = createRef<HTMLButtonElement>()
  const view = await mount(
    <>
      <Button ref={ref} disabled onClick={() => clicks++}>
        Disabled
      </Button>
      <Button render={<a href='#target' />}>Link</Button>
    </>
  )
  try {
    assert.equal(ref.current?.tagName, 'BUTTON')
    assert.equal(ref.current?.disabled, true)
    ref.current?.click()
    assert.equal(clicks, 0)
    assert.equal(view.host.querySelector('a')?.getAttribute('href'), '#target')
    assert.equal(view.host.querySelectorAll('button').length, 1)
  } finally {
    await view.cleanup()
  }
})

test('input wrappers preserve native form values, refs, errors and multiline sizing', async () => {
  const ref = createRef<HTMLInputElement>()
  const view = await mount(
    <form>
      <Input ref={ref} name='name' defaultValue='Example' aria-invalid='true' />
      <Textarea name='notes' defaultValue={'a\nb'} />
    </form>
  )
  try {
    assert.equal(ref.current, view.host.querySelector('input'))
    assert.equal(ref.current?.value, 'Example')
    assert.equal(ref.current?.getAttribute('aria-invalid'), 'true')
    assert.equal(view.host.querySelector('textarea')?.value, 'a\nb')
    assert.ok(
      view.host.querySelector('textarea')?.className.includes('max-h-96')
    )
  } finally {
    await view.cleanup()
  }
})

test('OTP uses six labelled native inputs and propagates RHF error descriptions', async () => {
  const view = await mount(<OTPExample />)
  try {
    const slots = view.host.querySelectorAll<HTMLInputElement>(
      '[data-slot=input-otp-slot]'
    )
    assert.equal(slots.length, 6)
    assert.equal(slots[0].id, 'test-otp')
    assert.equal(slots[0].labels?.[0]?.textContent, 'Verification code')
    assert.equal(slots[0].autocomplete, 'one-time-code')
    for (const slot of slots) {
      assert.equal(slot.tagName, 'INPUT')
      assert.equal(slot.getAttribute('aria-invalid'), 'true')
      assert.equal(slot.getAttribute('aria-describedby'), 'otp-help')
    }
    assert.ok(slots[1].getAttribute('aria-label')?.includes('2 / 6'))
  } finally {
    await view.cleanup()
  }
})

test('OTP paste normalizes whitespace and completion never submits the form', async () => {
  let value = ''
  let complete = 0
  let submits = 0
  const view = await mount(
    <OTPExample
      onChange={(next) => (value = next)}
      onComplete={() => complete++}
    />
  )
  try {
    const form = view.host.querySelector('form')
    const first = view.host.querySelector<HTMLInputElement>(
      '[data-slot=input-otp-slot]'
    )
    assert.ok(first && form)
    form.requestSubmit = () => {
      submits++
    }
    await paste(first, '12 34 56')
    assert.equal(value, '123456')
    assert.equal(complete, 1)
    assert.equal(submits, 0)
    assert.equal(
      view.host.querySelector<HTMLInputElement>('input[name=otp]')?.value,
      '123456'
    )
  } finally {
    await view.cleanup()
  }
})

test('disabled and read-only OTP slots cannot change via paste', async () => {
  for (const props of [{ disabled: true }, { readOnly: true }]) {
    let calls = 0
    const view = await mount(<OTPExample {...props} onChange={() => calls++} />)
    try {
      const first = view.host.querySelector<HTMLInputElement>(
        '[data-slot=input-otp-slot]'
      )
      assert.ok(first)
      await paste(first, '654321')
      assert.equal(calls, 0)
      assert.equal(first.value, '')
    } finally {
      await view.cleanup()
    }
  }
})

test('migration removes legacy runtimes and keeps the preview out of production', () => {
  const pkg = JSON.parse(
    readFileSync(new URL('../../../package.json', import.meta.url), 'utf8')
  )
  assert.equal(pkg.dependencies['@base-ui/react'], '1.8.0')
  assert.equal(pkg.dependencies.vaul, undefined)
  assert.equal(pkg.dependencies['input-otp'], undefined)
  const main = readFileSync(new URL('../../main.tsx', import.meta.url), 'utf8')
  const routes = readFileSync(
    new URL('../../routeTree.gen.ts', import.meta.url),
    'utf8'
  )
  assert.doesNotMatch(main + routes, /ui-foundation-preview/)
  const drawer = readFileSync(new URL('./drawer.tsx', import.meta.url), 'utf8')
  assert.match(drawer, /@base-ui\/react\/drawer/)
  assert.match(drawer, /VirtualKeyboardProvider/)
})
