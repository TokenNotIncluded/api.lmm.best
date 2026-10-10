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
  const aside = card.match(/<aside\b[^>]*>/)?.[0]
  const link = card.match(/<Link\b[^>]*>/)?.[0]
  const title = card.match(/<strong\b[^>]*>[\s\S]*?<\/strong>/)?.[0]
  assert.ok(aside, 'the homepage entry has an outer region')
  assert.ok(link, 'the directory remains a link')
  assert.ok(title, 'the directory link has a visible title')
  for (const element of [aside, link, title.split('>')[0]]) {
    assert.doesNotMatch(element, /className='[^']*\b(?:hidden|invisible)\b/)
    assert.doesNotMatch(element, /aria-hidden=['"]true['"]|\binert\b/)
  }
  assert.match(link, /to='\/ai-directory'/)
  assert.match(link, /focus-visible:ring-2/)
  assert.doesNotMatch(link, /\bdisabled\b|tabIndex=\{?-1/)
  assert.match(title, /t\('AI directory'\)/)
  assert.match(card, /min-w-0 flex-1/)
  assert.doesNotMatch(card, /useAuthStore|isConsoleActivated/)
})
