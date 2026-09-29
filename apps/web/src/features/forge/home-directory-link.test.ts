/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

test('the public navigation and homepage both link directly to the directory', () => {
  const shell = readFileSync(
    new URL('./forge-public-shell.tsx', import.meta.url),
    'utf8'
  )
  assert.match(shell, /title: 'AI directory', href: '\/ai-directory'/)
  assert.match(shell, /\{isHome && <HomeDirectoryLink \/>\}/)
  assert.ok(
    shell.indexOf('<HomeDirectoryLink />') < shell.indexOf('{props.children}')
  )
})

test('the homepage entry is a keyboard-accessible link on every screen size', () => {
  const card = readFileSync(
    new URL('./home-directory-link.tsx', import.meta.url),
    'utf8'
  )
  assert.match(card, /to='\/ai-directory'/)
  assert.match(card, /focus-visible:ring-2/)
  assert.match(card, /min-w-0 flex-1/)
  assert.doesNotMatch(card, /className='[^']*\bhidden\b/)
  assert.doesNotMatch(card, /useAuthStore|isConsoleActivated/)
})
