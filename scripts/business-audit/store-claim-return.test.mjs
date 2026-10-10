// Node's built-in test runner executes the actual frontend helper. These are
// URL-contract checks, not browser, cookie, account, or database acceptance.
import assert from 'node:assert/strict'
import test from 'node:test'
import {
  sanitizeAuthRedirect,
  signInHref,
} from '../../apps/web/src/features/auth/lib/auth-redirect.ts'

const origin = 'https://store.example.test'
const claimPath = `/store/claim/${'a'.repeat(43)}`

test('claim sign-in and reauthentication retain the complete local destination', () => {
  for (const suffix of ['', '?order=BI08&source=receipt#delivery', '?label=%E4%B8%AD%E6%96%87&value=a%2Bb%26c#item-2']) {
    for (const reauthenticate of [false, true]) {
      const location = new URL(claimPath + suffix, origin)
      const signIn = new URL(signInHref(location, reauthenticate), origin)
      assert.equal(signIn.origin, origin)
      assert.equal(signIn.pathname, '/sign-in')
      assert.equal(signIn.searchParams.get('reauth'), reauthenticate ? '1' : null)
      const target = signIn.searchParams.get('redirect')
      assert.equal(target, claimPath + suffix)
      assert.equal(sanitizeAuthRedirect(target, origin), claimPath + suffix)
    }
  }
})

test('claim return is stable on retry and sign-in does not nest its own redirect', () => {
  const location = new URL(`${claimPath}?order=BI08#delivery`, origin)
  const first = signInHref(location, true)
  assert.equal(signInHref(location, true), first)
  const current = new URL(first, origin)
  assert.equal(signInHref(current, true), '/sign-in?reauth=1')
  assert.equal(sanitizeAuthRedirect(location.href, origin), `${claimPath}?order=BI08#delivery`)
})
